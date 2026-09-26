// Package accrual contains a client of the external accrual calculation
// system and a background worker that polls it for uploaded orders and
// stores the results.
package accrual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/arigatory/go-musthave-diploma/internal/model"
)

// Statuses reported by the accrual system.
const (
	// StatusRegistered means the order is registered but not yet calculated.
	StatusRegistered = "REGISTERED"
	// StatusInvalid means the order will not be rewarded.
	StatusInvalid = "INVALID"
	// StatusProcessing means the calculation is in progress.
	StatusProcessing = "PROCESSING"
	// StatusProcessed means the calculation is complete.
	StatusProcessed = "PROCESSED"
)

// DefaultRetryAfter is used when a 429 response has no valid Retry-After header.
const DefaultRetryAfter = time.Minute

// ErrNotRegistered is returned when the order is unknown to the accrual system.
var ErrNotRegistered = errors.New("order is not registered in accrual system")

// RateLimitError is returned when the accrual system responds with
// 429 Too Many Requests.
type RateLimitError struct {
	// RetryAfter is how long to wait before sending more requests.
	RetryAfter time.Duration
}

// Error implements the error interface.
func (e *RateLimitError) Error() string {
	return fmt.Sprintf("accrual rate limit exceeded, retry after %s", e.RetryAfter)
}

// Result is the accrual calculation state of an order.
type Result struct {
	// Order is the order number.
	Order string `json:"order"`
	// Status is the calculation status (REGISTERED, INVALID, PROCESSING, PROCESSED).
	Status string `json:"status"`
	// Accrual is the calculated reward, absent if there is none.
	Accrual *model.Amount `json:"accrual,omitempty"`
}

// Client is an HTTP client of the accrual system.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a client for the accrual system at address.
// The "http://" scheme is added if address has none.
func NewClient(address string, httpClient *http.Client) *Client {
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(address, "/"), http: httpClient}
}

// Order requests the accrual calculation state of the order.
// It returns ErrNotRegistered for 204 No Content and *RateLimitError for
// 429 Too Many Requests.
func (c *Client) Order(ctx context.Context, number string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/orders/"+url.PathEscape(number), nil)
	if err != nil {
		return Result{}, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("request accrual: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var res Result
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			return Result{}, fmt.Errorf("decode accrual response: %w", err)
		}
		return res, nil
	case http.StatusNoContent:
		return Result{}, ErrNotRegistered
	case http.StatusTooManyRequests:
		return Result{}, &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	default:
		return Result{}, fmt.Errorf("unexpected accrual status %d", resp.StatusCode)
	}
}

func parseRetryAfter(v string) time.Duration {
	if sec, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return DefaultRetryAfter
}
