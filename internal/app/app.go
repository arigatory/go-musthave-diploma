// Package app wires the Gophermart components together and runs the service.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/accrual"
	"github.com/arigatory/go-musthave-diploma/internal/auth"
	"github.com/arigatory/go-musthave-diploma/internal/config"
	"github.com/arigatory/go-musthave-diploma/internal/handler"
	"github.com/arigatory/go-musthave-diploma/internal/service"
	"github.com/arigatory/go-musthave-diploma/internal/storage/postgres"
)

// shutdownTimeout bounds the graceful shutdown of the HTTP server.
const shutdownTimeout = 10 * time.Second

// Run starts the HTTP server and the accrual worker and blocks until ctx is
// cancelled or the server fails. On cancellation it shuts everything down
// gracefully.
func Run(ctx context.Context, cfg *config.Config, log *zap.Logger) error {
	store, err := postgres.New(ctx, cfg.DatabaseURI)
	if err != nil {
		return fmt.Errorf("init storage: %w", err)
	}
	defer store.Close()

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.TokenTTL)
	h := handler.New(service.New(store), tokens, log)
	srv := &http.Server{
		Addr:              cfg.RunAddress,
		Handler:           h.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	var wg sync.WaitGroup
	if cfg.AccrualSystemAddress != "" {
		client := accrual.NewClient(cfg.AccrualSystemAddress, nil)
		worker := accrual.NewWorker(store, client, log, cfg.Workers, cfg.PollInterval)
		wg.Go(func() { worker.Run(workerCtx) })
	} else {
		log.Warn("accrual system address is not set, orders will not be processed")
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting server", zap.String("address", cfg.RunAddress))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case runErr = <-errCh:
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown", zap.Error(err))
	}
	stopWorker()
	wg.Wait()
	return runErr
}
