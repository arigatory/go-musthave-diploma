// Package model contains domain entities and errors of the Gophermart
// loyalty system shared between the storage, service and transport layers.
package model

import (
	"errors"
	"time"
)

// OrderStatus is the processing status of an uploaded order.
type OrderStatus string

// Order statuses exposed by the Gophermart API.
const (
	// StatusNew means the order is uploaded but has not been processed yet.
	StatusNew OrderStatus = "NEW"
	// StatusProcessing means the accrual is being calculated.
	StatusProcessing OrderStatus = "PROCESSING"
	// StatusInvalid means the accrual system refused to calculate the reward.
	StatusInvalid OrderStatus = "INVALID"
	// StatusProcessed means the accrual has been calculated and credited.
	StatusProcessed OrderStatus = "PROCESSED"
)

// IsFinal reports whether the status can no longer change.
func (s OrderStatus) IsFinal() bool {
	return s == StatusInvalid || s == StatusProcessed
}

// User is a registered user of the loyalty system.
type User struct {
	// ID is the unique user identifier.
	ID int64
	// Login is the unique user login.
	Login string
	// PasswordHash is the bcrypt hash of the user's password.
	PasswordHash string
}

// Order is an order number uploaded by a user for accrual calculation.
type Order struct {
	// Number is the order number (a string of digits).
	Number string `json:"number"`
	// UserID is the owner of the order.
	UserID int64 `json:"-"`
	// Status is the current processing status.
	Status OrderStatus `json:"status"`
	// Accrual is the amount of points credited for the order, if any.
	Accrual *Amount `json:"accrual,omitempty"`
	// UploadedAt is the time the order was uploaded.
	UploadedAt time.Time `json:"uploaded_at"`
}

// Balance is the state of a user's loyalty account.
type Balance struct {
	// Current is the amount of points available for withdrawal.
	Current Amount `json:"current"`
	// Withdrawn is the total amount of points withdrawn so far.
	Withdrawn Amount `json:"withdrawn"`
}

// Withdrawal is a fact of spending points to pay for an order.
type Withdrawal struct {
	// Order is the number of the order paid with points.
	Order string `json:"order"`
	// Sum is the amount of withdrawn points.
	Sum Amount `json:"sum"`
	// ProcessedAt is the time the withdrawal was registered.
	ProcessedAt time.Time `json:"processed_at"`
}

// Domain errors returned by the storage and service layers.
var (
	// ErrInvalidInput is returned when request data fails validation.
	ErrInvalidInput = errors.New("invalid input")
	// ErrLoginTaken is returned when registering a login that already exists.
	ErrLoginTaken = errors.New("login already taken")
	// ErrInvalidCredentials is returned when the login/password pair is wrong.
	ErrInvalidCredentials = errors.New("invalid login or password")
	// ErrUserNotFound is returned when a user does not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidOrderNumber is returned for order numbers failing validation.
	ErrInvalidOrderNumber = errors.New("invalid order number")
	// ErrOrderAlreadyUploaded is returned when the same user uploads an order again.
	ErrOrderAlreadyUploaded = errors.New("order already uploaded by this user")
	// ErrOrderOwnedByAnother is returned when the order belongs to another user.
	ErrOrderOwnedByAnother = errors.New("order already uploaded by another user")
	// ErrInsufficientFunds is returned when the balance is lower than the requested sum.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrWithdrawalExists is returned when a withdrawal for the order already exists.
	ErrWithdrawalExists = errors.New("withdrawal for this order already exists")
)
