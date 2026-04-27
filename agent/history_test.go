package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gdey/outcrop/store"
)

func TestRegistrableDomain(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://example.com/article", "example.com"},
		{"https://news.example.com/x", "example.com"},
		{"https://blog.example.co.uk/x", "example.co.uk"},
		{"http://localhost:8080/", ""},
		{"https://127.0.0.1/", ""},
		{"file:///etc/passwd", ""},
		{"not a url", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := RegistrableDomain(tt.in); got != tt.want {
			t.Errorf("RegistrableDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRankVaults(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	c := store.Vault{Key: "kC", DisplayName: "Charlie"}
	all := []store.Vault{c, a, b} // intentionally out of order

	t.Run("no history → alphabetical", func(t *testing.T) {
		got := rankVaults(all, nil)
		want := []store.Vault{a, b, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("history orders the head", func(t *testing.T) {
		got := rankVaults(all, []string{"kC", "kA"})
		want := []store.Vault{c, a, b}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("history with unknown keys is skipped", func(t *testing.T) {
		got := rankVaults(all, []string{"k-deleted", "kB"})
		want := []store.Vault{b, a, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("history with duplicates dedups", func(t *testing.T) {
		got := rankVaults(all, []string{"kA", "kA", "kB"})
		want := []store.Vault{a, b, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		input := []store.Vault{c, a, b}
		_ = rankVaults(input, []string{"kC"})
		if input[0].Key != "kC" || input[1].Key != "kA" || input[2].Key != "kB" {
			t.Errorf("input mutated: %v", names(input))
		}
	})
}

// fakeHistory satisfies VaultHistory with a fixed map of (domain → keys).
type fakeHistory struct {
	keys map[string][]string
	err  error
}

func (f fakeHistory) VaultKeysForDomain(_ context.Context, domain string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.keys[domain], nil
}

func TestHistoryScorer_Score(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	c := store.Vault{Key: "kC", DisplayName: "Charlie"}
	all := []store.Vault{c, a, b}

	t.Run("empty URL → alphabetical", func(t *testing.T) {
		s := HistoryScorer{History: fakeHistory{}}
		got := s.Score(context.Background(), Input{}, all)
		want := []store.Vault{a, b, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("URL with history → history first", func(t *testing.T) {
		s := HistoryScorer{History: fakeHistory{
			keys: map[string][]string{"example.com": {"kC", "kA"}},
		}}
		got := s.Score(context.Background(), Input{URL: "https://example.com/article"}, all)
		want := []store.Vault{c, a, b}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("history error → alphabetical, no panic", func(t *testing.T) {
		s := HistoryScorer{History: fakeHistory{err: errors.New("DB on fire")}}
		got := s.Score(context.Background(), Input{URL: "https://example.com/x"}, all)
		want := []store.Vault{a, b, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})

	t.Run("URL without registrable domain → alphabetical", func(t *testing.T) {
		s := HistoryScorer{History: fakeHistory{}}
		got := s.Score(context.Background(), Input{URL: "http://localhost:8080/"}, all)
		want := []store.Vault{a, b, c}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", names(got), names(want))
		}
	})
}

func names(vs []store.Vault) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.DisplayName
	}
	return out
}
