package handler

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/arigatory/go-musthave-diploma/internal/auth"
	"github.com/arigatory/go-musthave-diploma/internal/model"
)

const userID int64 = 42

var errDB = errors.New("db down")

type env struct {
	svc    *MockService
	tokens *auth.TokenManager
	router http.Handler
	token  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	svc := NewMockService(gomock.NewController(t))
	tokens := auth.NewTokenManager("test", time.Hour)
	token, err := tokens.Issue(userID)
	require.NoError(t, err)
	return &env{
		svc:    svc,
		tokens: tokens,
		router: New(svc, tokens, zap.NewNop()).Router(),
		token:  token,
	}
}

// do sends a request; when authed is true the valid token is attached.
func (e *env) do(t *testing.T, method, path, body string, authed bool) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if authed {
		r.Header.Set("Authorization", "Bearer "+e.token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	return w.Result()
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestRegister(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		setup  func(s *MockService)
		status int
	}{
		{"ok", `{"login":"a","password":"p"}`, func(s *MockService) {
			s.EXPECT().Register(gomock.Any(), "a", "p").Return(userID, nil)
		}, http.StatusOK},
		{"bad json", `{`, nil, http.StatusBadRequest},
		{"invalid input", `{"login":"","password":"p"}`, func(s *MockService) {
			s.EXPECT().Register(gomock.Any(), "", "p").Return(int64(0), model.ErrInvalidInput)
		}, http.StatusBadRequest},
		{"taken", `{"login":"a","password":"p"}`, func(s *MockService) {
			s.EXPECT().Register(gomock.Any(), "a", "p").Return(int64(0), model.ErrLoginTaken)
		}, http.StatusConflict},
		{"internal", `{"login":"a","password":"p"}`, func(s *MockService) {
			s.EXPECT().Register(gomock.Any(), "a", "p").Return(int64(0), errDB)
		}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.setup != nil {
				tt.setup(e.svc)
			}
			resp := e.do(t, http.MethodPost, "/api/user/register", tt.body, false)
			defer resp.Body.Close()
			assert.Equal(t, tt.status, resp.StatusCode)
			if tt.status == http.StatusOK {
				assertAuthenticated(t, e, resp)
			}
		})
	}
}

func assertAuthenticated(t *testing.T, e *env, resp *http.Response) {
	t.Helper()
	token, ok := strings.CutPrefix(resp.Header.Get("Authorization"), "Bearer ")
	require.True(t, ok)
	id, err := e.tokens.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, userID, id)
	require.NotEmpty(t, resp.Cookies())
	assert.Equal(t, token, resp.Cookies()[0].Value)
}

func TestLogin(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		setup  func(s *MockService)
		status int
	}{
		{"ok", `{"login":"a","password":"p"}`, func(s *MockService) {
			s.EXPECT().Login(gomock.Any(), "a", "p").Return(userID, nil)
		}, http.StatusOK},
		{"bad json", `not json`, nil, http.StatusBadRequest},
		{"invalid input", `{}`, func(s *MockService) {
			s.EXPECT().Login(gomock.Any(), "", "").Return(int64(0), model.ErrInvalidInput)
		}, http.StatusBadRequest},
		{"wrong pair", `{"login":"a","password":"x"}`, func(s *MockService) {
			s.EXPECT().Login(gomock.Any(), "a", "x").Return(int64(0), model.ErrInvalidCredentials)
		}, http.StatusUnauthorized},
		{"internal", `{"login":"a","password":"p"}`, func(s *MockService) {
			s.EXPECT().Login(gomock.Any(), "a", "p").Return(int64(0), errDB)
		}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.setup != nil {
				tt.setup(e.svc)
			}
			resp := e.do(t, http.MethodPost, "/api/user/login", tt.body, false)
			defer resp.Body.Close()
			assert.Equal(t, tt.status, resp.StatusCode)
			if tt.status == http.StatusOK {
				assertAuthenticated(t, e, resp)
			}
		})
	}
}

func TestUploadOrder(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		err    error
		call   bool
		status int
	}{
		{"accepted", "12345678903", nil, true, http.StatusAccepted},
		{"accepted trims newline", "12345678903\n", nil, true, http.StatusAccepted},
		{"already uploaded", "12345678903", model.ErrOrderAlreadyUploaded, true, http.StatusOK},
		{"other user", "12345678903", model.ErrOrderOwnedByAnother, true, http.StatusConflict},
		{"invalid number", "12345678903", model.ErrInvalidOrderNumber, true, http.StatusUnprocessableEntity},
		{"internal", "12345678903", errDB, true, http.StatusInternalServerError},
		{"empty body", "", nil, false, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.call {
				e.svc.EXPECT().UploadOrder(gomock.Any(), userID, "12345678903").Return(tt.err)
			}
			resp := e.do(t, http.MethodPost, "/api/user/orders", tt.body, true)
			defer resp.Body.Close()
			assert.Equal(t, tt.status, resp.StatusCode)
		})
	}
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/user/orders"},
		{http.MethodGet, "/api/user/orders"},
		{http.MethodGet, "/api/user/balance"},
		{http.MethodPost, "/api/user/balance/withdraw"},
		{http.MethodGet, "/api/user/withdrawals"},
	} {
		resp := e.do(t, route.method, route.path, "", false)
		resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, route.path)
	}
}

func TestListOrders(t *testing.T) {
	ts := time.Date(2020, 12, 10, 15, 15, 45, 0, time.FixedZone("", 3*3600))
	acc := model.Amount(50000)

	t.Run("ok", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Orders(gomock.Any(), userID).Return([]model.Order{
			{Number: "9278923470", Status: model.StatusProcessed, Accrual: &acc, UploadedAt: ts},
			{Number: "346436439", Status: model.StatusInvalid, UploadedAt: ts},
		}, nil)
		resp := e.do(t, http.MethodGet, "/api/user/orders", "", true)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		assert.JSONEq(t, `[
			{"number":"9278923470","status":"PROCESSED","accrual":500,"uploaded_at":"2020-12-10T15:15:45+03:00"},
			{"number":"346436439","status":"INVALID","uploaded_at":"2020-12-10T15:15:45+03:00"}
		]`, readBody(t, resp))
	})
	t.Run("empty", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Orders(gomock.Any(), userID).Return(nil, nil)
		resp := e.do(t, http.MethodGet, "/api/user/orders", "", true)
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
	t.Run("internal", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Orders(gomock.Any(), userID).Return(nil, errDB)
		resp := e.do(t, http.MethodGet, "/api/user/orders", "", true)
		resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestBalance(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Balance(gomock.Any(), userID).Return(model.Balance{Current: 50050, Withdrawn: 4200}, nil)
		resp := e.do(t, http.MethodGet, "/api/user/balance", "", true)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `{"current":500.5,"withdrawn":42}`, readBody(t, resp))
	})
	t.Run("internal", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Balance(gomock.Any(), userID).Return(model.Balance{}, errDB)
		resp := e.do(t, http.MethodGet, "/api/user/balance", "", true)
		resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestWithdraw(t *testing.T) {
	body := `{"order":"2377225624","sum":751}`
	tests := []struct {
		name   string
		body   string
		err    error
		call   bool
		status int
	}{
		{"ok", body, nil, true, http.StatusOK},
		{"insufficient", body, model.ErrInsufficientFunds, true, http.StatusPaymentRequired},
		{"invalid order", body, model.ErrInvalidOrderNumber, true, http.StatusUnprocessableEntity},
		{"bad sum", body, model.ErrInvalidInput, true, http.StatusBadRequest},
		{"internal", body, errDB, true, http.StatusInternalServerError},
		{"bad json", `{"order":1}`, nil, false, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			if tt.call {
				e.svc.EXPECT().Withdraw(gomock.Any(), userID, "2377225624", model.Amount(75100)).Return(tt.err)
			}
			resp := e.do(t, http.MethodPost, "/api/user/balance/withdraw", tt.body, true)
			resp.Body.Close()
			assert.Equal(t, tt.status, resp.StatusCode)
		})
	}
}

func TestListWithdrawals(t *testing.T) {
	ts := time.Date(2020, 12, 9, 16, 9, 57, 0, time.FixedZone("", 3*3600))
	t.Run("ok", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Withdrawals(gomock.Any(), userID).Return([]model.Withdrawal{
			{Order: "2377225624", Sum: 50000, ProcessedAt: ts},
		}, nil)
		resp := e.do(t, http.MethodGet, "/api/user/withdrawals", "", true)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `[{"order":"2377225624","sum":500,"processed_at":"2020-12-09T16:09:57+03:00"}]`, readBody(t, resp))
	})
	t.Run("empty", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Withdrawals(gomock.Any(), userID).Return([]model.Withdrawal{}, nil)
		resp := e.do(t, http.MethodGet, "/api/user/withdrawals", "", true)
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
	t.Run("internal", func(t *testing.T) {
		e := newEnv(t)
		e.svc.EXPECT().Withdrawals(gomock.Any(), userID).Return(nil, errDB)
		resp := e.do(t, http.MethodGet, "/api/user/withdrawals", "", true)
		resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestGzip(t *testing.T) {
	e := newEnv(t)
	e.svc.EXPECT().Login(gomock.Any(), "a", "p").Return(userID, nil)
	e.svc.EXPECT().Balance(gomock.Any(), userID).Return(model.Balance{Current: 100}, nil)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(`{"login":"a","password":"p"}`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	r := httptest.NewRequest(http.MethodPost, "/api/user/login", &buf)
	r.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "gzipped request body is decompressed")

	r = httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
	r.Header.Set("Authorization", "Bearer "+e.token)
	r.Header.Set("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	zr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"current":1,"withdrawn":0}`, string(plain))

	r = httptest.NewRequest(http.MethodPost, "/api/user/login", strings.NewReader("not gzip"))
	r.Header.Set("Content-Encoding", "gzip")
	w = httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "malformed gzip body")
}

func TestRouting(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.MethodDelete, "/api/user/orders", "", true)
	resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	resp = e.do(t, http.MethodGet, "/api/unknown", "", true)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRecoverer(t *testing.T) {
	h := Recoverer(zap.NewNop())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCompressSkipsEmptyAndBinaryResponses(t *testing.T) {
	e := newEnv(t)
	e.svc.EXPECT().Orders(gomock.Any(), userID).Return(nil, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
	r.Header.Set("Authorization", "Bearer "+e.token)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"), "204 has no body to compress")
	assert.Zero(t, w.Body.Len())

	h := Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Empty(t, w.Header().Get("Content-Encoding"), "only JSON and plain text are compressed")
	assert.Equal(t, "png", w.Body.String())
}
