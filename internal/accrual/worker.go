package accrual

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/arigatory/go-musthave-diploma/internal/model"
	"github.com/arigatory/go-musthave-diploma/internal/ratelimit"
)

//go:generate go tool mockgen -destination=mock_test.go -package=accrual . Store,OrderSource

// batchPerWorker is how many pending orders are loaded per worker in one
// polling round. It keeps every worker busy for a few requests per round
// without loading the whole backlog into memory; orders not taken now are
// picked up in the next rounds (least recently checked first).
const batchPerWorker = 10

// Store is the persistence used by Worker.
type Store interface {
	// PendingOrders returns up to limit orders with a non-final status.
	PendingOrders(ctx context.Context, limit int) ([]model.Order, error)
	// UpdateOrderAccrual stores the accrual result and credits the balance.
	UpdateOrderAccrual(ctx context.Context, number string, status model.OrderStatus, accrual *model.Amount) error
}

// OrderSource fetches the accrual state of an order.
type OrderSource interface {
	// Order returns the accrual state of the order.
	Order(ctx context.Context, number string) (Result, error)
}

// Worker periodically polls the accrual system for orders that are not in
// a final status and updates them in the store. It respects the rate limit
// of the accrual system by pausing all requests for the Retry-After period.
type Worker struct {
	store    Store
	client   OrderSource
	log      *zap.Logger
	workers  int
	interval time.Duration
	batch    int
	limiter  ratelimit.Limiter
}

// NewWorker creates a Worker running the given number of concurrent
// requests every interval.
func NewWorker(store Store, client OrderSource, log *zap.Logger, workers int, interval time.Duration) *Worker {
	if workers < 1 {
		workers = 1
	}
	return &Worker{
		store:    store,
		client:   client,
		log:      log,
		workers:  workers,
		interval: interval,
		batch:    workers * batchPerWorker,
	}
}

// Run polls the accrual system until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		w.limiter.Wait(ctx)
		w.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// poll processes one batch of pending orders with at most w.workers
// concurrent requests.
func (w *Worker) poll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	orders, err := w.store.PendingOrders(ctx, w.batch)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("load pending orders", zap.Error(err))
		}
		return
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(w.workers)
	for _, o := range orders {
		if gctx.Err() != nil || w.limiter.Paused() {
			break
		}
		g.Go(func() error {
			w.process(gctx, o)
			return nil
		})
	}
	_ = g.Wait() // process never returns errors, it logs them
}

// process checks one order in the accrual system and stores the result.
func (w *Worker) process(ctx context.Context, o model.Order) {
	if w.limiter.Paused() {
		return
	}
	res, err := w.client.Order(ctx, o.Number)
	var rl *RateLimitError
	switch {
	case errors.As(err, &rl):
		w.limiter.Hit(rl.RetryAfter)
		w.log.Warn("accrual rate limit", zap.Duration("retry_after", rl.RetryAfter))
		return
	case errors.Is(err, ErrNotRegistered):
		// Keep the status but mark the order as checked so others get a turn.
		w.update(ctx, o.Number, o.Status, nil)
		return
	case err != nil:
		if ctx.Err() == nil {
			w.log.Error("get accrual", zap.String("order", o.Number), zap.Error(err))
		}
		return
	}

	status, ok := mapStatus(res.Status)
	if !ok {
		w.log.Warn("unknown accrual status", zap.String("order", o.Number), zap.String("status", res.Status))
		return
	}
	var accrual *model.Amount
	if status == model.StatusProcessed {
		accrual = res.Accrual
	}
	w.update(ctx, o.Number, status, accrual)
}

func (w *Worker) update(ctx context.Context, number string, status model.OrderStatus, accrual *model.Amount) {
	if err := w.store.UpdateOrderAccrual(ctx, number, status, accrual); err != nil && ctx.Err() == nil {
		w.log.Error("update order accrual", zap.String("order", number), zap.Error(err))
	}
}

// mapStatus converts an accrual system status to an order status.
func mapStatus(s string) (model.OrderStatus, bool) {
	switch s {
	case StatusRegistered, StatusProcessing:
		return model.StatusProcessing, true
	case StatusInvalid:
		return model.StatusInvalid, true
	case StatusProcessed:
		return model.StatusProcessed, true
	default:
		return "", false
	}
}
