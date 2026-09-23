package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassword(t *testing.T) {
	hash, err := HashPassword("secret")
	require.NoError(t, err)
	assert.NotEqual(t, "secret", hash)
	assert.True(t, CheckPassword(hash, "secret"))
	assert.False(t, CheckPassword(hash, "wrong"))
	assert.False(t, CheckPassword("not-a-hash", "secret"))

	_, err = HashPassword(string(make([]byte, 100)))
	assert.Error(t, err, "bcrypt rejects passwords longer than 72 bytes")
}

func TestTokenRoundTrip(t *testing.T) {
	m := NewTokenManager("key", time.Hour)
	assert.Equal(t, time.Hour, m.TTL())
	token, err := m.Issue(42)
	require.NoError(t, err)
	id, err := m.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
}

func TestTokenInvalid(t *testing.T) {
	m := NewTokenManager("key", time.Hour)
	token, err := m.Issue(1)
	require.NoError(t, err)

	_, err = NewTokenManager("other", time.Hour).Parse(token)
	assert.ErrorIs(t, err, ErrInvalidToken, "wrong secret")

	_, err = m.Parse("garbage")
	assert.ErrorIs(t, err, ErrInvalidToken)

	expired := NewTokenManager("key", time.Hour)
	expired.now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	old, err := expired.Issue(1)
	require.NoError(t, err)
	_, err = m.Parse(old)
	assert.ErrorIs(t, err, ErrInvalidToken, "expired")

	bad, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{Subject: "abc"}).SignedString([]byte("key"))
	require.NoError(t, err)
	_, err = m.Parse(bad)
	assert.ErrorIs(t, err, ErrInvalidToken, "non-numeric subject")

	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{Subject: "1"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)
	_, err = m.Parse(none)
	assert.ErrorIs(t, err, ErrInvalidToken, "alg none")
}

func TestMiddleware(t *testing.T) {
	m := NewTokenManager("key", time.Hour)
	token, err := m.Issue(7)
	require.NoError(t, err)

	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := UserIDFromContext(r.Context())
		require.True(t, ok)
		assert.Equal(t, int64(7), id)
		w.WriteHeader(http.StatusTeapot)
	}))

	tests := []struct {
		name   string
		setup  func(r *http.Request)
		status int
	}{
		{"no token", func(*http.Request) {}, http.StatusUnauthorized},
		{"bad token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer bad") }, http.StatusUnauthorized},
		{"bearer header", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }, http.StatusTeapot},
		{"bare header", func(r *http.Request) { r.Header.Set("Authorization", token) }, http.StatusTeapot},
		{"cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: CookieName, Value: token}) }, http.StatusTeapot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			tt.setup(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			assert.Equal(t, tt.status, w.Code)
		})
	}
}

func TestSetToken(t *testing.T) {
	m := NewTokenManager("key", time.Hour)
	w := httptest.NewRecorder()
	m.SetToken(w, "abc")
	assert.Equal(t, "Bearer abc", w.Header().Get("Authorization"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, CookieName, cookies[0].Name)
	assert.Equal(t, "abc", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestUserIDFromContextMissing(t *testing.T) {
	_, ok := UserIDFromContext(t.Context())
	assert.False(t, ok)
}
