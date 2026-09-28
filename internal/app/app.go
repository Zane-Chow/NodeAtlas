package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/auth"
	"controlpanel/internal/backup"
	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	consoleapi "controlpanel/internal/console"
	"controlpanel/internal/database"
	"controlpanel/internal/events"
	"controlpanel/internal/httpapi"
	"controlpanel/internal/inventory"
	"controlpanel/internal/jobs"
	"controlpanel/internal/operations"
	"controlpanel/internal/providers"
	provideraws "controlpanel/internal/providers/aws"
	providergcp "controlpanel/internal/providers/gcp"
	providermock "controlpanel/internal/providers/mock"
	providersolusvm2 "controlpanel/internal/providers/solusvm2"
	providervirtfusion "controlpanel/internal/providers/virtfusion"
	providervirtualizor "controlpanel/internal/providers/virtualizor"
	"controlpanel/internal/secrets"
	"controlpanel/internal/webassets"
	"github.com/go-chi/chi/v5"
)

func Run(ctx context.Context, cfg config.Config) error {
	db, dialect, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := database.Migrate(ctx, db, dialect); err != nil {
		return err
	}
	handler, worker, err := compose(db, dialect, cfg)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	workerContext, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	workerErrors := make(chan error, 1)
	go func() {
		if err := worker.Run(workerContext); err != nil {
			workerErrors <- err
		}
	}()
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()

	select {
	case err := <-serverErrors:
		stopWorker()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-workerErrors:
		stopWorker()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
		return fmt.Errorf("job worker stopped: %w", err)
	case <-ctx.Done():
		stopWorker()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}

func compose(db *sql.DB, dialect database.Dialect, cfg config.Config) (http.Handler, *jobs.Worker, error) {
	authRepository := auth.NewSQLRepository(db, dialect)
	authService := auth.NewService(authRepository, auth.NewArgon2idHasher(auth.DefaultArgon2idParams()), auth.ServiceOptions{})
	if cfg.Auth.Username != "" {
		if err := authService.Initialize(context.Background(), cfg.Auth.Username, cfg.Auth.Password); err != nil && !errors.Is(err, auth.ErrAlreadyInitialized) {
			return nil, nil, err
		}
	}
	credentialCipher, err := secrets.NewCredentialCipher(cfg.Secrets.CredentialKeys, cfg.Secrets.ActiveKeyVersion)
	if err != nil {
		return nil, nil, err
	}
	registry := providers.NewRegistry()
	if err := registry.Register("mock", providermock.NewFactory()); err != nil {
		return nil, nil, err
	}
	if err := registry.Register("aws", provideraws.NewFactory()); err != nil {
		return nil, nil, err
	}
	allowedProviderCIDRs, err := parseAllowedCIDRs(cfg.Providers.AllowedPrivateCIDRs, "provider")
	if err != nil {
		return nil, nil, err
	}
	if err := registry.Register("gcp", providergcp.NewFactory()); err != nil {
		return nil, nil, err
	}
	if err := registry.Register("solusvm2", providersolusvm2.NewFactory(
		providersolusvm2.FactoryOptions{AllowedPrivateCIDRs: allowedProviderCIDRs},
	)); err != nil {
		return nil, nil, err
	}
	if err := registry.Register("virtualizor", providervirtualizor.NewFactory(
		providervirtualizor.FactoryOptions{AllowedPrivateCIDRs: allowedProviderCIDRs},
	)); err != nil {
		return nil, nil, err
	}
	if err := registry.Register("virtfusion", providervirtfusion.NewFactory(providervirtfusion.FactoryOptions{AllowedPrivateCIDRs: allowedProviderCIDRs})); err != nil {
		return nil, nil, err
	}
	connectionRepository := connections.NewSQLRepository(db, dialect)
	jobRepository := jobs.NewSQLRepository(db, dialect)
	queue := jobs.NewQueue(jobRepository, jobs.QueueOptions{})
	connectionService := connections.NewService(connectionRepository, credentialCipher, registry, queue, connections.ServiceOptions{})
	connectionHandler := connections.NewHTTPHandler(connectionService)
	inventoryRepository := inventory.NewSQLRepository(db, dialect)
	inventoryHandler := inventory.NewHTTPHandler(inventoryRepository, connectionService)
	eventBroker := events.NewBroker(32)
	eventHandler := events.NewHTTPHandler(eventBroker, events.HTTPOptions{})
	operationRepository := operations.NewSQLRepository(db, dialect)
	auditRepository := audit.NewSQLRepository(db, dialect)
	operationService := operations.NewService(operationRepository, inventoryRepository, queue, auditRepository, operations.ServiceOptions{Publisher: eventBroker})
	operationHandler := operations.NewHTTPHandler(operationService, operationRepository)
	consoleRepository := consoleapi.NewSQLRepository(db, dialect)
	consoleTargets := consoleapi.NewMemoryTargetStore()
	allowedConsoleCIDRs, err := parseAllowedCIDRs(cfg.Console.AllowedPrivateCIDRs, "console")
	if err != nil {
		return nil, nil, err
	}
	allowedConsoleCIDRs = append(allowedConsoleCIDRs, allowedProviderCIDRs...)
	consolePolicy := consoleapi.NewTargetPolicy(nil, consoleapi.TargetPolicyOptions{
		AllowMockTransport:  true,
		AllowedPrivateCIDRs: allowedConsoleCIDRs,
	})
	consoleService := consoleapi.NewService(consoleRepository, inventoryRepository, connectionRepository, auditRepository, credentialCipher, registry, consolePolicy, consoleTargets, consoleapi.ServiceOptions{})
	consoleHandler := consoleapi.NewHTTPHandler(consoleService)
	consoleGateway := consoleapi.NewWebSocketGateway(consoleRepository, consoleTargets, auditRepository, consoleapi.GatewayOptions{TargetPolicy: consolePolicy})
	backupRepository := backup.NewSQLRepository(db, dialect)
	backupService, err := backup.NewService(backupRepository, backup.NewSnapshotter(db, dialect, backup.SnapshotOptions{ApplicationVersion: "dev"}), auditRepository, backup.NewSQLActivityChecker(db), backup.ServiceOptions{Directory: cfg.Backup.Directory})
	if err != nil {
		return nil, nil, err
	}
	backupHandler := backup.NewHTTPHandler(backupService)
	featureHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestPath := chi.RouteContext(request.Context()).RoutePath
		switch {
		case requestPath == "/events":
			eventHandler.ServeHTTP(response, request)
		case requestPath == "/operations", strings.HasPrefix(requestPath, "/operations/"), strings.Contains(requestPath, "/actions/"):
			operationHandler.ServeHTTP(response, request)
		case strings.Contains(requestPath, "/console-"), strings.Contains(requestPath, "/console-sessions"), strings.Contains(requestPath, "/provider-portal"), strings.Contains(requestPath, "/mock-pages/"):
			consoleHandler.ServeHTTP(response, request)
		case requestPath == "/backups", strings.HasPrefix(requestPath, "/backups/"):
			backupHandler.ServeHTTP(response, request)
		case requestPath == "/provider-types", requestPath == "/connections", strings.HasPrefix(requestPath, "/connections/"):
			connectionHandler.ServeHTTP(response, request)
		case requestPath == "/servers", strings.HasPrefix(requestPath, "/servers/"):
			inventoryHandler.ServeHTTP(response, request)
		default:
			http.NotFound(response, request)
		}
	})
	syncer := inventory.NewSyncer(connectionRepository, inventoryRepository, credentialCipher, registry, inventory.SyncerOptions{})
	operationExecutor := operations.NewExecutor(operationRepository, inventoryRepository, connectionRepository, auditRepository, credentialCipher, registry, operations.ExecutorOptions{Publisher: eventBroker})
	worker := jobs.NewWorker(jobRepository, jobs.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		switch job.Kind {
		case jobs.KindSyncConnection:
			var payload struct {
				ConnectionID string `json:"connection_id"`
			}
			if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ConnectionID == "" {
				return errors.New("invalid sync job payload")
			}
			return syncer.SyncConnection(ctx, payload.ConnectionID)
		case jobs.KindPowerOperation:
			return operationExecutor.Execute(ctx, job)
		default:
			return errors.New("unsupported job kind")
		}
	}), jobs.WorkerOptions{})
	publicOrigin := ""
	if cfg.HTTP.PublicOrigin != nil {
		publicOrigin = cfg.HTTP.PublicOrigin.String()
	}
	authHandler := auth.NewHTTPHandler(authService, auth.HTTPOptions{
		PublicOrigin:  publicOrigin,
		SecureCookies: cfg.Environment == "production",
		Protected:     featureHandler,
	})
	handler := httpapi.NewRouter(httpapi.Dependencies{
		Assets:    webassets.FileSystem(),
		Readiness: db,
		Auth:      authHandler,
		WebSocket: authHandler.ProtectSession(consoleGateway),
	})
	return handler, worker, nil
}

func parseAllowedCIDRs(values []string, purpose string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("parse allowed %s CIDR: %w", purpose, err)
		}
		result = append(result, network)
	}
	return result, nil
}
