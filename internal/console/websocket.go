package console

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"controlpanel/internal/audit"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type GatewayOptions struct {
	Now             func() time.Time
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	NewAuditID      func() string
	TargetPolicy    *TargetPolicy
}

type WebSocketGateway struct {
	sessions Repository
	targets  *MemoryTargetStore
	audit    AuditAppender
	options  GatewayOptions
}

func NewWebSocketGateway(sessions Repository, targets *MemoryTargetStore, auditLog AuditAppender, options GatewayOptions) http.Handler {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = 5 * time.Minute
	}
	if options.AbsoluteTimeout <= 0 {
		options.AbsoluteTimeout = 30 * time.Minute
	}
	if options.NewAuditID == nil {
		options.NewAuditID = uuid.NewString
	}
	gateway := &WebSocketGateway{sessions: sessions, targets: targets, audit: auditLog, options: options}
	router := chi.NewRouter()
	router.Get("/console/{ticket}", gateway.open)
	return router
}

func (gateway *WebSocketGateway) open(response http.ResponseWriter, request *http.Request) {
	ticket := chi.URLParam(request, "ticket")
	hash := sha256.Sum256([]byte(ticket))
	openedAt := gateway.options.Now().UTC()
	session, err := gateway.sessions.ConsumeTicket(request.Context(), hash[:], openedAt)
	if err != nil {
		http.Error(response, "Console ticket is unavailable", http.StatusUnauthorized)
		return
	}
	target, ok := gateway.targets.Take(session.ID)
	if !ok {
		_ = gateway.sessions.Close(request.Context(), session.ID, ResultFailed, gateway.options.Now().UTC())
		http.Error(response, "Console target is unavailable", http.StatusGone)
		return
	}
	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = gateway.sessions.Close(request.Context(), session.ID, ResultFailed, gateway.options.Now().UTC())
		return
	}
	connection.SetReadLimit(1024 * 1024)
	ctx, cancel := context.WithTimeout(request.Context(), gateway.options.AbsoluteTimeout)
	defer cancel()
	result := gateway.proxy(ctx, connection, target)
	_ = connection.Close(websocket.StatusNormalClosure, "console closed")
	closedAt := gateway.options.Now().UTC()
	_ = gateway.sessions.Close(context.Background(), session.ID, result, closedAt)
	gateway.appendAudit(session, result, closedAt.Sub(openedAt))
}

func (gateway *WebSocketGateway) proxy(ctx context.Context, downstream *websocket.Conn, target *url.URL) Result {
	if target.Scheme == "vnc+tcp" {
		return gateway.runRawVNC(ctx, downstream, target)
	}
	if target.Scheme == "mock+ws" {
		return gateway.runMock(ctx, downstream)
	}
	upstream, _, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return ResultFailed
	}
	defer upstream.CloseNow()
	upstream.SetReadLimit(1024 * 1024)
	proxyContext, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 2)
	go gateway.copyMessages(proxyContext, upstream, downstream, errorsChannel)
	go gateway.copyMessages(proxyContext, downstream, upstream, errorsChannel)
	err = <-errorsChannel
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return ResultCompleted
	}
	return ResultFailed
}

func (gateway *WebSocketGateway) runRawVNC(ctx context.Context, downstream *websocket.Conn, target *url.URL) Result {
	if gateway.options.TargetPolicy == nil {
		return ResultFailed
	}
	upstream, err := gateway.options.TargetPolicy.DialTCP(ctx, target)
	if err != nil {
		return ResultFailed
	}
	proxyContext, cancel := context.WithCancel(ctx)
	defer cancel()
	defer upstream.Close()
	stop := context.AfterFunc(proxyContext, func() { _ = upstream.Close() })
	defer stop()
	results := make(chan error, 2)
	go func() { results <- gateway.copyTCPToWebSocket(proxyContext, downstream, upstream) }()
	go func() { results <- gateway.copyWebSocketToTCP(proxyContext, upstream, downstream) }()
	err = <-results
	cancel()
	_ = upstream.Close()
	<-results
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return ResultCompleted
	}
	return ResultFailed
}

func (gateway *WebSocketGateway) copyTCPToWebSocket(ctx context.Context, destination *websocket.Conn, source net.Conn) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := source.SetReadDeadline(time.Now().Add(gateway.options.IdleTimeout)); err != nil {
			return err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			writeContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
			err := destination.Write(writeContext, websocket.MessageBinary, buffer[:count])
			cancel()
			if err != nil {
				return err
			}
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (gateway *WebSocketGateway) copyWebSocketToTCP(ctx context.Context, destination net.Conn, source *websocket.Conn) error {
	for {
		readContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
		messageType, data, err := source.Read(readContext)
		cancel()
		if err != nil {
			return err
		}
		if messageType != websocket.MessageBinary {
			return ErrTargetRejected
		}
		if err := destination.SetWriteDeadline(time.Now().Add(gateway.options.IdleTimeout)); err != nil {
			return err
		}
		if _, err := io.Copy(destination, bytes.NewReader(data)); err != nil {
			return err
		}
	}
}

func (gateway *WebSocketGateway) runMock(ctx context.Context, connection *websocket.Conn) Result {
	if err := connection.Write(ctx, websocket.MessageText, []byte("Server Control Mock Console\r\n")); err != nil {
		return ResultFailed
	}
	for {
		readContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
		messageType, data, err := connection.Read(readContext)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
				return ResultCompleted
			}
			return ResultFailed
		}
		writeContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
		err = connection.Write(writeContext, messageType, data)
		cancel()
		if err != nil {
			return ResultFailed
		}
	}
}

func (gateway *WebSocketGateway) copyMessages(ctx context.Context, destination, source *websocket.Conn, result chan<- error) {
	for {
		readContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
		messageType, data, err := source.Read(readContext)
		cancel()
		if err != nil {
			result <- err
			return
		}
		writeContext, cancel := context.WithTimeout(ctx, gateway.options.IdleTimeout)
		err = destination.Write(writeContext, messageType, data)
		cancel()
		if err != nil {
			result <- err
			return
		}
	}
}

func (gateway *WebSocketGateway) appendAudit(session Session, result Result, duration time.Duration) {
	if gateway.audit == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]any{"mode": session.Mode, "result": result, "duration_seconds": int64(duration.Seconds())})
	_ = gateway.audit.Append(context.Background(), audit.Entry{ID: gateway.options.NewAuditID(), EventType: "console_session_closed", TargetType: "console", TargetID: session.ID, Metadata: metadata, CreatedAt: gateway.options.Now().UTC()})
}
