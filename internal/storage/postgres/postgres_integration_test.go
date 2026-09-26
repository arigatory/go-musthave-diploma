//go:build integration

package postgres

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/arigatory/go-musthave-diploma/internal/model"
	"github.com/arigatory/go-musthave-diploma/internal/testutil"
)

func newStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := New(t.Context(), testutil.StartPostgres(t))
	require.NoError(t, err)
	t.Cleanup(s.Close)
	require.NoError(t, s.Ping(t.Context()))
	// Migrations are idempotent.
	require.NoError(t, Migrate(s.pool))
	return s
}

func TestStorage(t *testing.T) {
	s := newStorage(t)
	ctx := t.Context()

	alice, err := s.CreateUser(ctx, "alice", "hash")
	require.NoError(t, err)
	bob, err := s.CreateUser(ctx, "bob", "hash")
	require.NoError(t, err)

	t.Run("users", func(t *testing.T) {
		_, err := s.CreateUser(ctx, "alice", "other")
		assert.ErrorIs(t, err, model.ErrLoginTaken)

		u, err := s.UserByLogin(ctx, "alice")
		require.NoError(t, err)
		assert.Equal(t, alice, u.ID)
		assert.Equal(t, "hash", u.PasswordHash)

		_, err = s.UserByLogin(ctx, "nobody")
		assert.ErrorIs(t, err, model.ErrUserNotFound)

		_, err = s.Balance(ctx, 999999)
		assert.ErrorIs(t, err, model.ErrUserNotFound)
	})

	t.Run("orders", func(t *testing.T) {
		require.NoError(t, s.AddOrder(ctx, alice, "12345678903"))
		require.NoError(t, s.AddOrder(ctx, alice, "9278923470"))
		assert.ErrorIs(t, s.AddOrder(ctx, alice, "12345678903"), model.ErrOrderAlreadyUploaded)
		assert.ErrorIs(t, s.AddOrder(ctx, bob, "12345678903"), model.ErrOrderOwnedByAnother)

		orders, err := s.ListOrders(ctx, alice)
		require.NoError(t, err)
		require.Len(t, orders, 2)
		assert.Equal(t, "9278923470", orders[0].Number, "newest first")
		assert.Equal(t, model.StatusNew, orders[0].Status)
		assert.Nil(t, orders[0].Accrual)

		empty, err := s.ListOrders(ctx, bob)
		require.NoError(t, err)
		assert.Empty(t, empty)
	})

	t.Run("accrual", func(t *testing.T) {
		pending, err := s.PendingOrders(ctx, 10)
		require.NoError(t, err)
		assert.Len(t, pending, 2)

		require.NoError(t, s.UpdateOrderAccrual(ctx, "9278923470", model.StatusProcessing, nil))
		acc := model.Amount(72998)
		require.NoError(t, s.UpdateOrderAccrual(ctx, "12345678903", model.StatusProcessed, &acc))
		// A repeated final update must not credit the points twice.
		require.NoError(t, s.UpdateOrderAccrual(ctx, "12345678903", model.StatusProcessed, &acc))

		b, err := s.Balance(ctx, alice)
		require.NoError(t, err)
		assert.Equal(t, model.Balance{Current: 72998}, b)

		pending, err = s.PendingOrders(ctx, 10)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		assert.Equal(t, model.StatusProcessing, pending[0].Status)
		assert.Equal(t, alice, pending[0].UserID)

		require.NoError(t, s.UpdateOrderAccrual(ctx, "9278923470", model.StatusInvalid, nil))
		orders, err := s.ListOrders(ctx, alice)
		require.NoError(t, err)
		assert.Equal(t, model.StatusInvalid, orders[0].Status)
		assert.Equal(t, model.StatusProcessed, orders[1].Status)
		require.NotNil(t, orders[1].Accrual)
		assert.Equal(t, acc, *orders[1].Accrual)
	})

	t.Run("withdrawals", func(t *testing.T) {
		require.NoError(t, s.Withdraw(ctx, alice, "2377225624", 10000))
		require.NoError(t, s.Withdraw(ctx, alice, "79927398713", 2998))
		assert.ErrorIs(t, s.Withdraw(ctx, alice, "2377225624", 1), model.ErrWithdrawalExists)
		assert.ErrorIs(t, s.Withdraw(ctx, alice, "12345678903", 1_000_000), model.ErrInsufficientFunds)
		assert.ErrorIs(t, s.Withdraw(ctx, 999999, "12345678903", 1), model.ErrUserNotFound)

		b, err := s.Balance(ctx, alice)
		require.NoError(t, err)
		assert.Equal(t, model.Balance{Current: 60000, Withdrawn: 12998}, b)

		list, err := s.ListWithdrawals(ctx, alice)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "79927398713", list[0].Order, "newest first")
		assert.Equal(t, model.Amount(2998), list[0].Sum)
	})
}

func TestConcurrentWithdrawalsNeverOverdraw(t *testing.T) {
	s := newStorage(t)
	ctx := t.Context()
	id, err := s.CreateUser(ctx, "carol", "hash")
	require.NoError(t, err)
	require.NoError(t, s.AddOrder(ctx, id, "12345678903"))
	acc := model.Amount(1000)
	require.NoError(t, s.UpdateOrderAccrual(ctx, "12345678903", model.StatusProcessed, &acc))

	const attempts = 20
	var ok, insufficient atomic.Int32
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			err := s.Withdraw(ctx, id, "order-"+string(rune('a'+i)), 100)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, model.ErrInsufficientFunds):
				insufficient.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int32(10), ok.Load())
	assert.Equal(t, int32(10), insufficient.Load())
	b, err := s.Balance(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.Balance{Current: 0, Withdrawn: 1000}, b)
}

func TestConcurrentAccrualCreditsOnce(t *testing.T) {
	s := newStorage(t)
	ctx := t.Context()
	id, err := s.CreateUser(ctx, "dave", "hash")
	require.NoError(t, err)
	require.NoError(t, s.AddOrder(ctx, id, "12345678903"))

	acc := model.Amount(500)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			assert.NoError(t, s.UpdateOrderAccrual(ctx, "12345678903", model.StatusProcessed, &acc))
		})
	}
	wg.Wait()

	b, err := s.Balance(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, model.Amount(500), b.Current)
}

func TestConcurrentAddOrderSameNumber(t *testing.T) {
	s := newStorage(t)
	ctx := t.Context()
	id, err := s.CreateUser(ctx, "erin", "hash")
	require.NoError(t, err)

	const attempts = 10
	var added, duplicate atomic.Int32
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			err := s.AddOrder(ctx, id, "12345678903")
			switch {
			case err == nil:
				added.Add(1)
			case errors.Is(err, model.ErrOrderAlreadyUploaded):
				duplicate.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int32(1), added.Load())
	assert.Equal(t, int32(attempts-1), duplicate.Load())
}
