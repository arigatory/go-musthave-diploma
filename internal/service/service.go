// Package service implements the business logic of the Gophermart loyalty
// system: user registration and authentication, order uploads, balance
// queries and withdrawals.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/arigatory/go-musthave-diploma/internal/auth"
	"github.com/arigatory/go-musthave-diploma/internal/luhn"
	"github.com/arigatory/go-musthave-diploma/internal/model"
)

//go:generate go tool mockgen -destination=mock_storage_test.go -package=service . Storage

// maxPasswordLen is the maximum password length supported by bcrypt.
const maxPasswordLen = 72

// Storage is the persistence layer used by Service.
type Storage interface {
	// CreateUser inserts a user and returns its ID or model.ErrLoginTaken.
	CreateUser(ctx context.Context, login, passwordHash string) (int64, error)
	// GetUserByLogin returns the user or model.ErrUserNotFound.
	GetUserByLogin(ctx context.Context, login string) (model.User, error)
	// AddOrder registers an order for the user.
	AddOrder(ctx context.Context, userID int64, number string) error
	// ListOrders returns the user's orders, newest first.
	ListOrders(ctx context.Context, userID int64) ([]model.Order, error)
	// GetBalance returns the user's balance.
	GetBalance(ctx context.Context, userID int64) (model.Balance, error)
	// Withdraw deducts sum from the user's balance for the given order.
	Withdraw(ctx context.Context, userID int64, order string, sum model.Amount) error
	// ListWithdrawals returns the user's withdrawals, newest first.
	ListWithdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error)
}

// Service implements the Gophermart use cases.
type Service struct {
	store Storage
}

// New creates a Service backed by store.
func New(store Storage) *Service {
	return &Service{store: store}
}

// Register creates a new user and returns its ID.
// It returns model.ErrInvalidInput for an empty login or an empty or too
// long password, and model.ErrLoginTaken if the login already exists.
func (s *Service) Register(ctx context.Context, login, password string) (int64, error) {
	if login == "" || password == "" || len(password) > maxPasswordLen {
		return 0, model.ErrInvalidInput
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}
	return s.store.CreateUser(ctx, login, hash)
}

// Login checks the credentials and returns the user ID.
// It returns model.ErrInvalidCredentials if the pair is wrong.
func (s *Service) Login(ctx context.Context, login, password string) (int64, error) {
	if login == "" || password == "" {
		return 0, model.ErrInvalidInput
	}
	u, err := s.store.GetUserByLogin(ctx, login)
	if errors.Is(err, model.ErrUserNotFound) {
		return 0, model.ErrInvalidCredentials
	}
	if err != nil {
		return 0, err
	}
	if !auth.CheckPassword(u.PasswordHash, password) {
		return 0, model.ErrInvalidCredentials
	}
	return u.ID, nil
}

// UploadOrder registers an order number for the user.
// It returns model.ErrInvalidOrderNumber if the number fails the Luhn check,
// model.ErrOrderAlreadyUploaded or model.ErrOrderOwnedByAnother for duplicates.
func (s *Service) UploadOrder(ctx context.Context, userID int64, number string) error {
	if !luhn.Valid(number) {
		return model.ErrInvalidOrderNumber
	}
	return s.store.AddOrder(ctx, userID, number)
}

// Orders returns the user's orders, newest first.
func (s *Service) Orders(ctx context.Context, userID int64) ([]model.Order, error) {
	return s.store.ListOrders(ctx, userID)
}

// Balance returns the user's current balance and total withdrawn amount.
func (s *Service) Balance(ctx context.Context, userID int64) (model.Balance, error) {
	return s.store.GetBalance(ctx, userID)
}

// Withdraw spends sum points to pay for the order.
// It returns model.ErrInvalidOrderNumber for an invalid or already used order
// number, model.ErrInvalidInput for a non-positive sum and
// model.ErrInsufficientFunds if the balance is too low.
func (s *Service) Withdraw(ctx context.Context, userID int64, order string, sum model.Amount) error {
	if !luhn.Valid(order) {
		return model.ErrInvalidOrderNumber
	}
	if sum <= 0 {
		return model.ErrInvalidInput
	}
	err := s.store.Withdraw(ctx, userID, order, sum)
	if errors.Is(err, model.ErrWithdrawalExists) {
		return fmt.Errorf("%w: %w", model.ErrInvalidOrderNumber, err)
	}
	return err
}

// Withdrawals returns the user's withdrawals, newest first.
func (s *Service) Withdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error) {
	return s.store.ListWithdrawals(ctx, userID)
}
