package accrual

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/model"
)

func amount(a model.Amount) *model.Amount { return &a }

func newWorker(t *testing.T, workers int) (*Worker, *MockStore, *MockOrderSource) {
	ctrl := gomock.NewController(t)
	store := NewMockStore(ctrl)
	client := NewMockOrderSource(ctrl)
	return NewWorker(store, client, zap.NewNop(), workers, 10*time.Millisecond), store, client
}

func TestProcessMapsStatuses(t *testing.T) {
	tests := []struct {
		name       string
		res        Result
		err        error
		wantStatus model.OrderStatus
		wantAcc    *model.Amount
		noUpdate   bool
	}{
		{"processed", Result{Status: StatusProcessed, Accrual: amount(500)}, nil, model.StatusProcessed, amount(500), false},
		{"processed without accrual", Result{Status: StatusProcessed}, nil, model.StatusProcessed, nil, false},
		{"registered", Result{Status: StatusRegistered}, nil, model.StatusProcessing, nil, false},
		{"processing", Result{Status: StatusProcessing}, nil, model.StatusProcessing, nil, false},
		{"invalid ignores accrual", Result{Status: StatusInvalid, Accrual: amount(1)}, nil, model.StatusInvalid, nil, false},
		{"not registered keeps status", Result{}, ErrNotRegistered, model.StatusNew, nil, false},
		{"unknown status", Result{Status: "WEIRD"}, nil, "", nil, true},
		{"transport error", Result{}, errors.New("boom"), "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, store, client := newWorker(t, 1)
			ctx := t.Context()
			client.EXPECT().Order(ctx, "1").Return(tt.res, tt.err)
			if !tt.noUpdate {
				store.EXPECT().UpdateOrderAccrual(ctx, "1", tt.wantStatus, tt.wantAcc).Return(nil)
			}
			w.process(ctx, model.Order{Number: "1", Status: model.StatusNew})
		})
	}
}

func TestProcessUpdateError(t *testing.T) {
	w, store, client := newWorker(t, 1)
	ctx := t.Context()
	client.EXPECT().Order(ctx, "1").Return(Result{Status: StatusInvalid}, nil)
	store.EXPECT().UpdateOrderAccrual(ctx, "1", model.StatusInvalid, nil).Return(errors.New("db"))
	w.process(ctx, model.Order{Number: "1"})
}

func TestRateLimitPausesWorker(t *testing.T) {
	w, store, client := newWorker(t, 1)
	ctx := t.Context()
	store.EXPECT().PendingOrders(ctx, 10).Return([]model.Order{{Number: "1"}, {Number: "2"}, {Number: "3"}}, nil)
	client.EXPECT().Order(gomock.Any(), "1").Return(Result{}, &RateLimitError{RetryAfter: time.Hour})

	w.poll(ctx)
	assert.True(t, w.limiter.Paused(), "remaining orders are skipped while paused")
}

func TestPollErrors(t *testing.T) {
	w, store, _ := newWorker(t, 2)
	ctx := t.Context()
	store.EXPECT().PendingOrders(ctx, 20).Return(nil, errors.New("db"))
	w.poll(ctx)

	store.EXPECT().PendingOrders(ctx, 20).Return(nil, nil)
	w.poll(ctx)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	w.poll(cctx) // no calls on a cancelled context
}

func TestRunProcessesOrdersConcurrently(t *testing.T) {
	w, store, client := newWorker(t, 3)
	ctx, cancel := context.WithCancel(t.Context())

	orders := []model.Order{{Number: "1"}, {Number: "2"}, {Number: "3"}, {Number: "4"}}
	var polled atomic.Bool
	store.EXPECT().PendingOrders(gomock.Any(), 30).DoAndReturn(
		func(context.Context, int) ([]model.Order, error) {
			if polled.Swap(true) {
				return nil, nil
			}
			return orders, nil
		}).MinTimes(1)
	var updated atomic.Int32
	client.EXPECT().Order(gomock.Any(), gomock.Any()).Return(Result{Status: StatusProcessed, Accrual: amount(100)}, nil).Times(4)
	store.EXPECT().UpdateOrderAccrual(gomock.Any(), gomock.Any(), model.StatusProcessed, amount(100)).
		DoAndReturn(func(context.Context, string, model.OrderStatus, *model.Amount) error {
			if updated.Add(1) == 4 {
				cancel()
			}
			return nil
		}).Times(4)

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
	assert.Equal(t, int32(4), updated.Load())
}

func TestNewWorkerMinWorkers(t *testing.T) {
	w := NewWorker(nil, nil, zap.NewNop(), 0, time.Second)
	assert.Equal(t, 1, w.workers)
}
