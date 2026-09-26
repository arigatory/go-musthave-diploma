// Package logger builds the application zap logger.
package logger

import "go.uber.org/zap"

// New creates a production zap logger with the given level
// (debug, info, warn, error).
func New(level string) (*zap.Logger, error) {
	lvl, err := zap.ParseAtomicLevel(level)
	if err != nil {
		return nil, err
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = lvl
	return cfg.Build()
}
