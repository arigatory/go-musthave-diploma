package accrual

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/arigatory/go-musthave-diploma/internal/model"
)

func TestClientGetOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number := strings.TrimPrefix(r.URL.Path, "/api/orders/")
		switch number {
		case "1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"order":"1","status":"PROCESSED","accrual":729.98}`))
		case "2":
			_, _ = w.Write([]byte(`{"order":"2","status":"INVALID"}`))
		case "3":
			w.WriteHeader(http.StatusNoContent)
		case "4":
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		case "5":
			w.WriteHeader(http.StatusTooManyRequests)
		case "6":
			_, _ = w.Write([]byte(`{bad json`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", nil)
	ctx := t.Context()

	res, err := c.GetOrder(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, StatusProcessed, res.Status)
	require.NotNil(t, res.Accrual)
	assert.Equal(t, model.Amount(72998), *res.Accrual)

	res, err = c.GetOrder(ctx, "2")
	require.NoError(t, err)
	assert.Equal(t, StatusInvalid, res.Status)
	assert.Nil(t, res.Accrual)

	_, err = c.GetOrder(ctx, "3")
	assert.ErrorIs(t, err, ErrNotRegistered)

	_, err = c.GetOrder(ctx, "4")
	var rl *RateLimitError
	require.ErrorAs(t, err, &rl)
	assert.Equal(t, time.Minute, rl.RetryAfter)
	assert.Contains(t, rl.Error(), "1m0s")

	_, err = c.GetOrder(ctx, "5")
	require.ErrorAs(t, err, &rl)
	assert.Equal(t, DefaultRetryAfter, rl.RetryAfter)

	_, err = c.GetOrder(ctx, "6")
	assert.ErrorContains(t, err, "decode")

	_, err = c.GetOrder(ctx, "7")
	assert.ErrorContains(t, err, "unexpected accrual status 500")
}

func TestClientAddsScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(strings.TrimPrefix(srv.URL, "http://"), srv.Client())
	_, err := c.GetOrder(t.Context(), "1")
	assert.ErrorIs(t, err, ErrNotRegistered)
}

func TestClientTransportError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", nil)
	_, err := c.GetOrder(t.Context(), "1")
	assert.ErrorContains(t, err, "request accrual")
}

func TestParseRetryAfter(t *testing.T) {
	assert.Equal(t, 5*time.Second, parseRetryAfter("5"))
	assert.Equal(t, DefaultRetryAfter, parseRetryAfter(""))
	assert.Equal(t, DefaultRetryAfter, parseRetryAfter("-3"))
	assert.Equal(t, DefaultRetryAfter, parseRetryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)))
	d := parseRetryAfter(time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	assert.InDelta(t, time.Hour.Seconds(), d.Seconds(), 2)
}
