//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/config"
	"github.com/arigatory/go-musthave-diploma/internal/testutil"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().String()
}

// fakeAccrual answers 429 once, then REGISTERED, then PROCESSED with 500 points
// for order 12345678903, INVALID for 9278923470 and 204 for anything else.
func fakeAccrual(t *testing.T) *httptest.Server {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number := strings.TrimPrefix(r.URL.Path, "/api/orders/")
		switch number {
		case "12345678903":
			switch calls.Add(1) {
			case 1:
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
			case 2:
				fmt.Fprintf(w, `{"order":"%s","status":"REGISTERED"}`, number)
			default:
				fmt.Fprintf(w, `{"order":"%s","status":"PROCESSED","accrual":500}`, number)
			}
		case "9278923470":
			fmt.Fprintf(w, `{"order":"%s","status":"INVALID"}`, number)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path, body string) (int, string) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, strings.NewReader(body))
	require.NoError(c.t, err)
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	if h := resp.Header.Get("Authorization"); h != "" {
		c.token = h
	}
	return resp.StatusCode, string(b)
}

func TestEndToEnd(t *testing.T) {
	cfg := &config.Config{
		RunAddress:           freeAddr(t),
		DatabaseURI:          testutil.StartPostgres(t),
		AccrualSystemAddress: fakeAccrual(t).URL,
		JWTSecret:            "test",
		TokenTTL:             time.Hour,
		Workers:              2,
		PollInterval:         50 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, zap.NewNop()) }()

	c := &client{t: t, base: "http://" + cfg.RunAddress}
	require.Eventually(t, func() bool {
		resp, err := http.Get(c.base + "/api/user/balance")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusUnauthorized
	}, 10*time.Second, 50*time.Millisecond, "server did not start")

	code, _ := c.do(http.MethodPost, "/api/user/register", `{"login":"user","password":"pass"}`)
	require.Equal(t, http.StatusOK, code)
	code, _ = c.do(http.MethodPost, "/api/user/register", `{"login":"user","password":"pass"}`)
	assert.Equal(t, http.StatusConflict, code)

	code, _ = c.do(http.MethodGet, "/api/user/orders", "")
	assert.Equal(t, http.StatusNoContent, code)

	code, _ = c.do(http.MethodPost, "/api/user/orders", "12345678903")
	assert.Equal(t, http.StatusAccepted, code)
	code, _ = c.do(http.MethodPost, "/api/user/orders", "9278923470")
	assert.Equal(t, http.StatusAccepted, code)
	code, _ = c.do(http.MethodPost, "/api/user/orders", "12345678903")
	assert.Equal(t, http.StatusOK, code)
	code, _ = c.do(http.MethodPost, "/api/user/orders", "12345678901")
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	other := &client{t: t, base: c.base}
	code, _ = other.do(http.MethodPost, "/api/user/register", `{"login":"other","password":"pass"}`)
	require.Equal(t, http.StatusOK, code)
	code, _ = other.do(http.MethodPost, "/api/user/orders", "12345678903")
	assert.Equal(t, http.StatusConflict, code)

	var balance struct{ Current, Withdrawn float64 }
	require.Eventually(t, func() bool {
		code, body := c.do(http.MethodGet, "/api/user/balance", "")
		return code == http.StatusOK && json.Unmarshal([]byte(body), &balance) == nil && balance.Current == 500
	}, 10*time.Second, 100*time.Millisecond, "accrual was not credited")

	code, body := c.do(http.MethodGet, "/api/user/orders", "")
	require.Equal(t, http.StatusOK, code)
	var orders []struct {
		Number  string   `json:"number"`
		Status  string   `json:"status"`
		Accrual *float64 `json:"accrual"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &orders))
	require.Len(t, orders, 2)
	byNumber := map[string]string{}
	for _, o := range orders {
		byNumber[o.Number] = o.Status
	}
	assert.Equal(t, "PROCESSED", byNumber["12345678903"])
	assert.Equal(t, "INVALID", byNumber["9278923470"])

	code, _ = c.do(http.MethodPost, "/api/user/balance/withdraw", `{"order":"2377225624","sum":751}`)
	assert.Equal(t, http.StatusPaymentRequired, code)
	code, _ = c.do(http.MethodPost, "/api/user/balance/withdraw", `{"order":"2377225624","sum":100.5}`)
	assert.Equal(t, http.StatusOK, code)
	code, _ = c.do(http.MethodPost, "/api/user/balance/withdraw", `{"order":"123","sum":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	code, body = c.do(http.MethodGet, "/api/user/balance", "")
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"current":399.5,"withdrawn":100.5}`, body)

	code, body = c.do(http.MethodGet, "/api/user/withdrawals", "")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"order":"2377225624","sum":100.5`)

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("service did not shut down")
	}
}

func TestRunFailsOnBadDatabase(t *testing.T) {
	cfg := &config.Config{RunAddress: freeAddr(t), DatabaseURI: "postgres://nobody@127.0.0.1:1/none?connect_timeout=1"}
	err := Run(t.Context(), cfg, zap.NewNop())
	assert.ErrorContains(t, err, "init storage")
}
