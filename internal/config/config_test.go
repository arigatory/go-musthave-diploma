package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaultsAndFlags(t *testing.T) {
	cfg, err := Load([]string{"-d", "postgres://db", "-r", "http://accrual", "-w", "0"}, env(nil))
	require.NoError(t, err)
	assert.Equal(t, DefaultRunAddress, cfg.RunAddress)
	assert.Equal(t, "postgres://db", cfg.DatabaseURI)
	assert.Equal(t, "http://accrual", cfg.AccrualSystemAddress)
	assert.Equal(t, DefaultJWTSecret, cfg.JWTSecret)
	assert.Equal(t, DefaultTokenTTL, cfg.TokenTTL)
	assert.Equal(t, 1, cfg.Workers)
	assert.Equal(t, time.Second, cfg.PollInterval)
}

func TestLoadEnvOverridesFlags(t *testing.T) {
	cfg, err := Load([]string{"-a", ":1", "-d", "flag-db", "-r", "flag-acc"}, env(map[string]string{
		"RUN_ADDRESS":            ":2",
		"DATABASE_URI":           "env-db",
		"ACCRUAL_SYSTEM_ADDRESS": "env-acc",
		"JWT_SECRET":             "s3cret",
	}))
	require.NoError(t, err)
	assert.Equal(t, ":2", cfg.RunAddress)
	assert.Equal(t, "env-db", cfg.DatabaseURI)
	assert.Equal(t, "env-acc", cfg.AccrualSystemAddress)
	assert.Equal(t, "s3cret", cfg.JWTSecret)
}

func TestLoadErrors(t *testing.T) {
	_, err := Load(nil, env(nil))
	assert.ErrorIs(t, err, ErrNoDatabaseURI)

	_, err = Load([]string{"-unknown"}, env(nil))
	assert.Error(t, err)
}

func TestLoadEnvOverridesNumericFlags(t *testing.T) {
	cfg, err := Load([]string{"-d", "db", "-t", "1h", "-w", "2", "-p", "5s"}, env(map[string]string{
		"TOKEN_TTL":       "2h",
		"ACCRUAL_WORKERS": "8",
		"POLL_INTERVAL":   "250ms",
	}))
	require.NoError(t, err)
	assert.Equal(t, 2*time.Hour, cfg.TokenTTL)
	assert.Equal(t, 8, cfg.Workers)
	assert.Equal(t, 250*time.Millisecond, cfg.PollInterval)
}

func TestLoadInvalidEnv(t *testing.T) {
	_, err := Load([]string{"-d", "db"}, env(map[string]string{
		"POLL_INTERVAL":   "abc",
		"ACCRUAL_WORKERS": "x",
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, "POLL_INTERVAL: not a duration")
	assert.ErrorContains(t, err, "ACCRUAL_WORKERS: not a number")
}
