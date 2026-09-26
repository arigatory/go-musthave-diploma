package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHitDoesNotShrinkPause(t *testing.T) {
	var l Limiter
	assert.False(t, l.Paused(), "zero value is not paused")

	l.Hit(time.Hour)
	assert.True(t, l.Paused())
	l.Hit(time.Minute)
	assert.Greater(t, l.remaining(), 59*time.Minute, "shorter pause does not shrink the longer one")
}

func TestWaitReturnsOnCancel(t *testing.T) {
	var l Limiter
	l.Hit(time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	l.Wait(ctx) // returns immediately on cancelled context
}

func TestWaitExpires(t *testing.T) {
	var l Limiter
	l.Hit(20 * time.Millisecond)
	start := time.Now()
	l.Wait(t.Context())
	assert.GreaterOrEqual(t, time.Since(start), 15*time.Millisecond)
	assert.False(t, l.Paused())
}
