package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"controlpanel/internal/auth"
	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/httpapi"
	"controlpanel/internal/inventory"
	"controlpanel/internal/jobs"
	"controlpanel/internal/providers"
	providermock "controlpanel/internal/providers/mock"
	"controlpanel/internal/secrets"
	"controlpanel/internal/webassets"
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
	connectionRepository := connections.NewSQLRepository(db, dialect)
	jobRepository := jobs.NewSQLRepository(db, dialect)
	queue := jobs.NewQueue(jobRepository, jobs.QueueOptions{})
	connectionService := connections.NewService(connectionRepository, credentialCipher, registry, queue, connections.ServiceOptions{})
	featureHandler := connections.NewHTTPHandler(connectionService)
	inventoryRepository := inventory.NewSQLRepository(db, dialect)
	syncer := inventory.NewSyncer(connectionRepository, inventoryRepository, credentialCipher, registry, inventory.SyncerOptions{})
	worker := jobs.NewWorker(jobRepository, jobs.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		if job.Kind != jobs.KindSyncConnection {
			return errors.New("unsupported job kind")
		}
		var payload struct {
			ConnectionID string `json:"connection_id"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ConnectionID == "" {
			return errors.New("invalid sync job payload")
		}
		return syncer.SyncConnection(ctx, payload.ConnectionID)
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
	})
	return handler, worker, nil
}
