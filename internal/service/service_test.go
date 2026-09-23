package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/arigatory/go-musthave-diploma/internal/auth"
	"github.com/arigatory/go-musthave-diploma/internal/model"
)

var errDB = errors.New("db down")

func newService(t *testing.T) (*Service, *MockStorage) {
	ctrl := gomock.NewController(t)
	store := NewMockStorage(ctrl)
	return New(store), store
}

func TestRegister(t *testing.T) {
	svc, store := newService(t)
	ctx := t.Context()

	store.EXPECT().CreateUser(ctx, "alice", gomock.Any()).DoAndReturn(
		func(_ any, _ string, hash string) (int64, error) {
			assert.True(t, auth.CheckPassword(hash, "pw"))
			return 1, nil
		})
	id, err := svc.Register(ctx, "alice", "pw")
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	store.EXPECT().CreateUser(ctx, "alice", gomock.Any()).Return(int64(0), model.ErrLoginTaken)
	_, err = svc.Register(ctx, "alice", "pw")
	assert.ErrorIs(t, err, model.ErrLoginTaken)

	for _, c := range [][2]string{{"", "pw"}, {"bob", ""}, {"bob", strings.Repeat("x", 73)}} {
		_, err = svc.Register(ctx, c[0], c[1])
		assert.ErrorIs(t, err, model.ErrInvalidInput)
	}
}

func TestLogin(t *testing.T) {
	svc, store := newService(t)
	ctx := t.Context()
	hash, err := auth.HashPassword("pw")
	require.NoError(t, err)

	store.EXPECT().GetUserByLogin(ctx, "alice").Return(model.User{ID: 5, Login: "alice", PasswordHash: hash}, nil).Times(2)
	id, err := svc.Login(ctx, "alice", "pw")
	require.NoError(t, err)
	assert.Equal(t, int64(5), id)

	_, err = svc.Login(ctx, "alice", "wrong")
	assert.ErrorIs(t, err, model.ErrInvalidCredentials)

	store.EXPECT().GetUserByLogin(ctx, "ghost").Return(model.User{}, model.ErrUserNotFound)
	_, err = svc.Login(ctx, "ghost", "pw")
	assert.ErrorIs(t, err, model.ErrInvalidCredentials)

	store.EXPECT().GetUserByLogin(ctx, "alice").Return(model.User{}, errDB)
	_, err = svc.Login(ctx, "alice", "pw")
	assert.ErrorIs(t, err, errDB)

	_, err = svc.Login(ctx, "", "pw")
	assert.ErrorIs(t, err, model.ErrInvalidInput)
}

func TestUploadOrder(t *testing.T) {
	svc, store := newService(t)
	ctx := t.Context()

	store.EXPECT().AddOrder(ctx, int64(1), "12345678903").Return(nil)
	assert.NoError(t, svc.UploadOrder(ctx, 1, "12345678903"))

	store.EXPECT().AddOrder(ctx, int64(1), "12345678903").Return(model.ErrOrderOwnedByAnother)
	assert.ErrorIs(t, svc.UploadOrder(ctx, 1, "12345678903"), model.ErrOrderOwnedByAnother)

	assert.ErrorIs(t, svc.UploadOrder(ctx, 1, "12345678901"), model.ErrInvalidOrderNumber)
	assert.ErrorIs(t, svc.UploadOrder(ctx, 1, "abc"), model.ErrInvalidOrderNumber)
}

func TestQueries(t *testing.T) {
	svc, store := newService(t)
	ctx := t.Context()
	orders := []model.Order{{Number: "1", Status: model.StatusNew, UploadedAt: time.Now()}}
	withdrawals := []model.Withdrawal{{Order: "2", Sum: 100}}
	balance := model.Balance{Current: 10, Withdrawn: 5}

	store.EXPECT().ListOrders(ctx, int64(1)).Return(orders, nil)
	store.EXPECT().ListWithdrawals(ctx, int64(1)).Return(withdrawals, nil)
	store.EXPECT().GetBalance(ctx, int64(1)).Return(balance, nil)

	gotOrders, err := svc.Orders(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, orders, gotOrders)

	gotW, err := svc.Withdrawals(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, withdrawals, gotW)

	gotB, err := svc.Balance(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, balance, gotB)
}

func TestWithdraw(t *testing.T) {
	svc, store := newService(t)
	ctx := t.Context()

	store.EXPECT().Withdraw(ctx, int64(1), "2377225624", model.Amount(75100)).Return(nil)
	assert.NoError(t, svc.Withdraw(ctx, 1, "2377225624", 75100))

	store.EXPECT().Withdraw(ctx, int64(1), "2377225624", model.Amount(1)).Return(model.ErrInsufficientFunds)
	assert.ErrorIs(t, svc.Withdraw(ctx, 1, "2377225624", 1), model.ErrInsufficientFunds)

	store.EXPECT().Withdraw(ctx, int64(1), "2377225624", model.Amount(1)).Return(model.ErrWithdrawalExists)
	err := svc.Withdraw(ctx, 1, "2377225624", 1)
	assert.ErrorIs(t, err, model.ErrInvalidOrderNumber)
	assert.ErrorIs(t, err, model.ErrWithdrawalExists)

	assert.ErrorIs(t, svc.Withdraw(ctx, 1, "123", 1), model.ErrInvalidOrderNumber)
	assert.ErrorIs(t, svc.Withdraw(ctx, 1, "2377225624", 0), model.ErrInvalidInput)
}
