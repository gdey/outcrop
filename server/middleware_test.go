package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAuthMiddleware(t *testing.T) {
	const token = "s3cret"
	wrapped := authMiddleware(token)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"non-bearer", "Basic Zm9vOmJhcg==", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"correct token", "Bearer s3cret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/anything", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			wrapped.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestCORSMiddleware(t *testing.T) {
	called := false
	wrapped := corsMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("non-extension origin gets no headers", func(t *testing.T) {
		called = false
		r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		r.Header.Set("Origin", "https://example.com")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
			t.Errorf("unexpected ACAO: %q", h)
		}
		if !called {
			t.Errorf("inner handler should run for non-OPTIONS")
		}
	})

	t.Run("moz-extension origin echoes back", func(t *testing.T) {
		called = false
		r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		origin := "moz-extension://abcd-1234"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("ACAO = %q, want %q", got, origin)
		}
		if !called {
			t.Errorf("inner handler should run for non-OPTIONS")
		}
	})

	t.Run("preflight short-circuits with 204", func(t *testing.T) {
		called = false
		r := httptest.NewRequest(http.MethodOptions, "/clip", nil)
		r.Header.Set("Origin", "moz-extension://x")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", w.Code)
		}
		if called {
			t.Errorf("inner handler must not run on preflight")
		}
		if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") {
			t.Errorf("missing POST in allow-methods: %q", w.Header().Get("Access-Control-Allow-Methods"))
		}
	})

	t.Run("preflight without extension origin is forbidden", func(t *testing.T) {
		called = false
		r := httptest.NewRequest(http.MethodOptions, "/clip", nil)
		r.Header.Set("Origin", "https://evil.example/")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", w.Code)
		}
		if called {
			t.Errorf("inner handler must not run")
		}
	})
}

func TestRecoverMiddleware(t *testing.T) {
	wrapped := recoverMiddleware(discardLogger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"error":"internal"`) {
		t.Errorf("body missing error code: %s", w.Body.String())
	}
}

func TestValidateLoopback(t *testing.T) {
	tests := []struct {
		addr  string
		valid bool
	}{
		{"127.0.0.1:7878", true},
		{"[::1]:7878", true},
		{"localhost:7878", true},
		{"0.0.0.0:7878", false},
		{"192.168.1.5:7878", false},
		{"example.com:7878", false},
		{":7878", false},
		{"127.0.0.1", false}, // missing port
	}
	for _, tt := range tests {
		err := validateLoopback(tt.addr)
		if tt.valid && err != nil {
			t.Errorf("addr %q: expected ok, got %v", tt.addr, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("addr %q: expected error, got nil", tt.addr)
		}
	}
}
