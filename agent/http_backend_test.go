package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gdey/outcrop/store"
)

func newServer(handler http.HandlerFunc) (*httptest.Server, *HTTPSuggester) {
	ts := httptest.NewServer(handler)
	return ts, &HTTPSuggester{
		Endpoint: ts.URL + "/v1",
		Model:    "test-model",
		Client:   ts.Client(),
	}
}

func okResponse(content string) string {
	return `{"choices":[{"message":{"content":` + jsonString(content) + `}}]}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestHTTPSuggester_HappyPath(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	b := store.Vault{Key: "kB", DisplayName: "Work Notes"}

	ts, sug := newServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}

		body, _ := io.ReadAll(r.Body)
		var req chatCompletionsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("body: %v\n%s", err, body)
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q", req.Model)
		}
		if req.Stream {
			t.Errorf("stream should be false")
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("messages = %+v", req.Messages)
		}
		if !strings.Contains(req.Messages[1].Content, "Personal") {
			t.Errorf("user message missing vault name: %q", req.Messages[1].Content)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okResponse("Personal"))
	})
	defer ts.Close()

	got := sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a, b})
	if got != "Personal" {
		t.Errorf("got %q, want Personal", got)
	}
}

func TestHTTPSuggester_UNSURE(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	ts, sug := newServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okResponse("UNSURE"))
	})
	defer ts.Close()

	got := sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
	if got != "" {
		t.Errorf("got %q, want empty for UNSURE", got)
	}
}

func TestHTTPSuggester_UnknownNameDropped(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	ts, sug := newServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okResponse("Recipes"))
	})
	defer ts.Close()

	got := sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
	if got != "" {
		t.Errorf("got %q, want empty for unknown name", got)
	}
}

func TestHTTPSuggester_Non200(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	ts, sug := newServer(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	defer ts.Close()

	got := sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
	if got != "" {
		t.Errorf("got %q, want empty for 5xx", got)
	}
}

func TestHTTPSuggester_NetworkError(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	sug := HTTPSuggester{
		Endpoint: "http://127.0.0.1:1/v1", // very likely closed
		Model:    "test-model",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	got := sug.Suggest(ctx, Input{URL: "https://x"}, []store.Vault{a})
	if got != "" {
		t.Errorf("got %q, want empty for network error", got)
	}
}

func TestHTTPSuggester_ContextCancelled(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	ts, sug := newServer(func(w http.ResponseWriter, _ *http.Request) {
		// Sleep long enough that the client's short timeout fires first; the
		// response that lands here would never be read.
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(w, okResponse("Personal"))
	})
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got := sug.Suggest(ctx, Input{URL: "https://x"}, []store.Vault{a})
	if got != "" {
		t.Errorf("got %q, want empty for cancelled ctx", got)
	}
}

func TestHTTPSuggester_AuthorizationHeader(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}

	t.Run("with key", func(t *testing.T) {
		ts, sug := newServer(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer s3cret" {
				t.Errorf("Authorization = %q, want Bearer s3cret", got)
			}
			_, _ = io.WriteString(w, okResponse("Personal"))
		})
		defer ts.Close()
		sug.APIKey = "s3cret"
		_ = sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
	})

	t.Run("without key", func(t *testing.T) {
		ts, sug := newServer(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("Authorization = %q, want empty", got)
			}
			_, _ = io.WriteString(w, okResponse("Personal"))
		})
		defer ts.Close()
		_ = sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
	})
}

func TestHTTPSuggester_MissingConfig(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}

	// Empty endpoint or model → "" without making a request.
	for _, sug := range []HTTPSuggester{
		{Endpoint: "", Model: "x"},
		{Endpoint: "http://x", Model: ""},
	} {
		got := sug.Suggest(context.Background(), Input{URL: "https://x"}, []store.Vault{a})
		if got != "" {
			t.Errorf("expected empty for missing config, got %q", got)
		}
	}
}

func TestCheckEndpoint(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				t.Errorf("path = %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()
		if err := CheckEndpoint(context.Background(), ts.URL+"/v1", "", ts.Client()); err != nil {
			t.Errorf("CheckEndpoint: %v", err)
		}
	})

	t.Run("non-2xx", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer ts.Close()
		if err := CheckEndpoint(context.Background(), ts.URL+"/v1", "", ts.Client()); err == nil {
			t.Errorf("expected error on 500")
		}
	})

	t.Run("empty endpoint", func(t *testing.T) {
		if err := CheckEndpoint(context.Background(), "", "", nil); err == nil {
			t.Errorf("expected error on empty endpoint")
		}
	})
}
