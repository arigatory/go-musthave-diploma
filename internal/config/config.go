// Package config loads the Gophermart service configuration from
// command line flags and environment variables.
package config

import (
	"errors"
	"flag"
	"os"
	"time"
)

// Default configuration values.
const (
	// DefaultRunAddress is the default address the HTTP server listens on.
	DefaultRunAddress = "localhost:8080"
	// DefaultJWTSecret is used to sign tokens when JWT_SECRET is not set.
	// It is intended for local development only.
	DefaultJWTSecret = "gophermart-dev-secret"
	// DefaultTokenTTL is the default lifetime of an authentication token.
	DefaultTokenTTL = 24 * time.Hour
	// DefaultWorkers is the default number of accrual polling workers.
	DefaultWorkers = 4
	// DefaultPollInterval is the default interval between accrual polling rounds.
	DefaultPollInterval = time.Second
)

// Config holds the service configuration.
type Config struct {
	// RunAddress is the address and port the HTTP server listens on.
	RunAddress string
	// DatabaseURI is the PostgreSQL connection string.
	DatabaseURI string
	// AccrualSystemAddress is the base URL of the accrual calculation system.
	AccrualSystemAddress string
	// JWTSecret is the key used to sign authentication tokens.
	JWTSecret string
	// TokenTTL is the lifetime of an authentication token.
	TokenTTL time.Duration
	// Workers is the number of concurrent accrual polling workers.
	Workers int
	// PollInterval is the interval between accrual polling rounds.
	PollInterval time.Duration
	// LogLevel is the logging level (debug, info, warn, error).
	LogLevel string
}

// ErrNoDatabaseURI is returned when the database URI is not configured.
var ErrNoDatabaseURI = errors.New("database URI is not set (use -d flag or DATABASE_URI)")

// Load parses flags from args (without the program name) and then applies
// environment variables obtained via getenv, which take precedence over flags.
func Load(args []string, getenv func(string) string) (*Config, error) {
	cfg := &Config{}
	fs := flag.NewFlagSet("gophermart", flag.ContinueOnError)
	fs.StringVar(&cfg.RunAddress, "a", DefaultRunAddress, "address and port to run the service")
	fs.StringVar(&cfg.DatabaseURI, "d", "", "database connection URI")
	fs.StringVar(&cfg.AccrualSystemAddress, "r", "", "accrual system address")
	fs.StringVar(&cfg.JWTSecret, "s", DefaultJWTSecret, "JWT signing secret")
	fs.DurationVar(&cfg.TokenTTL, "t", DefaultTokenTTL, "authentication token lifetime")
	fs.IntVar(&cfg.Workers, "w", DefaultWorkers, "number of accrual polling workers")
	fs.DurationVar(&cfg.PollInterval, "p", DefaultPollInterval, "accrual polling interval")
	fs.StringVar(&cfg.LogLevel, "l", "info", "log level")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	stringFromEnv(getenv, "RUN_ADDRESS", &cfg.RunAddress)
	stringFromEnv(getenv, "DATABASE_URI", &cfg.DatabaseURI)
	stringFromEnv(getenv, "ACCRUAL_SYSTEM_ADDRESS", &cfg.AccrualSystemAddress)
	stringFromEnv(getenv, "JWT_SECRET", &cfg.JWTSecret)
	stringFromEnv(getenv, "LOG_LEVEL", &cfg.LogLevel)

	if cfg.DatabaseURI == "" {
		return nil, ErrNoDatabaseURI
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	return cfg, nil
}

// FromOS loads the configuration from os.Args and the process environment.
func FromOS() (*Config, error) {
	return Load(os.Args[1:], os.Getenv)
}

func stringFromEnv(getenv func(string) string, key string, dst *string) {
	if v := getenv(key); v != "" {
		*dst = v
	}
}
