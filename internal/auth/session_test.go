package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilhauk/msg/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetCookie_SecureFlag(t *testing.T) {
	tests := []struct {
		name           string
		secure         bool
		wantSecure     bool
		wantSameSite   string
	}{
		{name: "http (dev)", secure: false, wantSecure: false, wantSameSite: "Lax"},
		{name: "https (prod)", secure: true, wantSecure: true, wantSameSite: "Lax"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			auth.SetCookie(w, "tok.sig", tc.secure)

			resp := w.Result()
			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			c := cookies[0]
			assert.Equal(t, "session", c.Name)
			assert.Equal(t, tc.wantSecure, c.Secure)
			assert.True(t, c.HttpOnly)
			assert.Equal(t, http.SameSiteLaxMode, c.SameSite)
		})
	}
}

func TestClearCookie_SecureFlag(t *testing.T) {
	for _, secure := range []bool{false, true} {
		w := httptest.NewRecorder()
		auth.ClearCookie(w, secure)

		resp := w.Result()
		cookies := resp.Cookies()
		require.Len(t, cookies, 1)
		c := cookies[0]
		assert.Equal(t, "session", c.Name)
		assert.Equal(t, secure, c.Secure)
		assert.True(t, c.HttpOnly)
		assert.Equal(t, -1, c.MaxAge)
	}
}

func TestHandler_Secure(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{"http://localhost:8080", false},
		{"https://example.com", true},
		{"https://example.com/", true},
	}
	for _, tc := range tests {
		t.Run(tc.baseURL, func(t *testing.T) {
			got := strings.HasPrefix(tc.baseURL, "https://")
			assert.Equal(t, tc.want, got)
		})
	}
}
