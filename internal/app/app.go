package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"controlpanel/internal/httpapi"
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
	handler := httpapi.NewRouter(httpapi.Dependencies{
		Assets:    webassets.FileSystem(),
		Readiness: db,
	})
	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
