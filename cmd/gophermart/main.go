// Command gophermart runs the Gophermart cumulative loyalty system HTTP API.
//
// Configuration (environment variables take precedence over flags):
//
//	-a, RUN_ADDRESS             address and port to listen on
//	-d, DATABASE_URI            PostgreSQL connection string
//	-r, ACCRUAL_SYSTEM_ADDRESS  accrual calculation system address
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/app"
	"github.com/arigatory/go-musthave-diploma/internal/config"
	"github.com/arigatory/go-musthave-diploma/internal/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromOS()
	if err != nil {
		return err
	}
	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = log.Sync() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, log); err != nil {
		log.Error("service stopped with error", zap.Error(err))
		return err
	}
	return nil
}
