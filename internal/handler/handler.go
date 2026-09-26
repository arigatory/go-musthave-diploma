// Package handler implements the HTTP API of the Gophermart loyalty system.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/auth"
	"github.com/arigatory/go-musthave-diploma/internal/model"
)

//go:generate go tool mockgen -destination=mock_service_test.go -package=handler . Service

// maxBodySize limits the size of request bodies.
const maxBodySize = 1 << 20

// Service is the business logic used by the HTTP handlers.
type Service interface {
	// Register creates a user and returns its ID.
	Register(ctx context.Context, login, password string) (int64, error)
	// Login checks credentials and returns the user ID.
	Login(ctx context.Context, login, password string) (int64, error)
	// UploadOrder registers an order number for the user.
	UploadOrder(ctx context.Context, userID int64, number string) error
	// Orders returns the user's orders, newest first.
	Orders(ctx context.Context, userID int64) ([]model.Order, error)
	// Balance returns the user's balance.
	Balance(ctx context.Context, userID int64) (model.Balance, error)
	// Withdraw spends points to pay for an order.
	Withdraw(ctx context.Context, userID int64, order string, sum model.Amount) error
	// Withdrawals returns the user's withdrawals, newest first.
	Withdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error)
}

// Handler serves the Gophermart HTTP API.
type Handler struct {
	svc    Service
	tokens *auth.TokenManager
	log    *zap.Logger
}

// New creates a Handler.
func New(svc Service, tokens *auth.TokenManager, log *zap.Logger) *Handler {
	return &Handler{svc: svc, tokens: tokens, log: log}
}

// Router returns the HTTP router with all API routes and middlewares.
func (h *Handler) Router() http.Handler {
	authed := func(f http.HandlerFunc) http.Handler { return h.tokens.Middleware(f) }

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/user/register", h.Register)
	mux.HandleFunc("POST /api/user/login", h.Login)
	mux.Handle("POST /api/user/orders", authed(h.UploadOrder))
	mux.Handle("GET /api/user/orders", authed(h.ListOrders))
	mux.Handle("GET /api/user/balance", authed(h.Balance))
	mux.Handle("POST /api/user/balance/withdraw", authed(h.Withdraw))
	mux.Handle("GET /api/user/withdrawals", authed(h.ListWithdrawals))

	return Logging(h.log)(Recoverer(h.log)(Decompress(Compress(mux))))
}

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type withdrawRequest struct {
	Order string       `json:"order"`
	Sum   model.Amount `json:"sum"`
}

// Register handles POST /api/user/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	userID, err := h.svc.Register(r.Context(), c.Login, c.Password)
	switch {
	case errors.Is(err, model.ErrInvalidInput):
		http.Error(w, "login and password are required", http.StatusBadRequest)
		return
	case errors.Is(err, model.ErrLoginTaken):
		http.Error(w, "login already taken", http.StatusConflict)
		return
	case err != nil:
		h.internalError(w, "register", err)
		return
	}
	h.authenticate(w, userID)
}

// Login handles POST /api/user/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := decodeJSON(r, &c); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	userID, err := h.svc.Login(r.Context(), c.Login, c.Password)
	switch {
	case errors.Is(err, model.ErrInvalidInput):
		http.Error(w, "login and password are required", http.StatusBadRequest)
		return
	case errors.Is(err, model.ErrInvalidCredentials):
		http.Error(w, "invalid login or password", http.StatusUnauthorized)
		return
	case err != nil:
		h.internalError(w, "login", err)
		return
	}
	h.authenticate(w, userID)
}

// UploadOrder handles POST /api/user/orders.
func (h *Handler) UploadOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	number := strings.TrimSpace(string(body))
	if number == "" {
		http.Error(w, "order number is required", http.StatusBadRequest)
		return
	}
	err = h.svc.UploadOrder(r.Context(), userIDFrom(r), number)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, model.ErrOrderAlreadyUploaded):
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, model.ErrOrderOwnedByAnother):
		http.Error(w, "order uploaded by another user", http.StatusConflict)
	case errors.Is(err, model.ErrInvalidOrderNumber):
		http.Error(w, "invalid order number", http.StatusUnprocessableEntity)
	default:
		h.internalError(w, "upload order", err)
	}
}

// ListOrders handles GET /api/user/orders.
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := h.svc.Orders(r.Context(), userIDFrom(r))
	if err != nil {
		h.internalError(w, "list orders", err)
		return
	}
	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.writeJSON(w, orders)
}

// Balance handles GET /api/user/balance.
func (h *Handler) Balance(w http.ResponseWriter, r *http.Request) {
	balance, err := h.svc.Balance(r.Context(), userIDFrom(r))
	if err != nil {
		h.internalError(w, "get balance", err)
		return
	}
	h.writeJSON(w, balance)
}

// Withdraw handles POST /api/user/balance/withdraw.
func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	var req withdrawRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	err := h.svc.Withdraw(r.Context(), userIDFrom(r), req.Order, req.Sum)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, model.ErrInvalidOrderNumber):
		http.Error(w, "invalid order number", http.StatusUnprocessableEntity)
	case errors.Is(err, model.ErrInvalidInput):
		http.Error(w, "sum must be positive", http.StatusBadRequest)
	case errors.Is(err, model.ErrInsufficientFunds):
		http.Error(w, "insufficient funds", http.StatusPaymentRequired)
	default:
		h.internalError(w, "withdraw", err)
	}
}

// ListWithdrawals handles GET /api/user/withdrawals.
func (h *Handler) ListWithdrawals(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Withdrawals(r.Context(), userIDFrom(r))
	if err != nil {
		h.internalError(w, "list withdrawals", err)
		return
	}
	if len(list) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.writeJSON(w, list)
}

func (h *Handler) authenticate(w http.ResponseWriter, userID int64) {
	token, err := h.tokens.Issue(userID)
	if err != nil {
		h.internalError(w, "issue token", err)
		return
	}
	h.tokens.SetToken(w, token)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.log.Warn("write response", zap.Error(err))
	}
}

func (h *Handler) internalError(w http.ResponseWriter, op string, err error) {
	h.log.Error(op, zap.Error(err))
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodySize))
	return dec.Decode(v)
}

// userIDFrom returns the authenticated user ID. Routes using it are always
// wrapped with the auth middleware, so the ID is guaranteed to be present.
func userIDFrom(r *http.Request) int64 {
	id, _ := auth.UserIDFromContext(r.Context())
	return id
}
