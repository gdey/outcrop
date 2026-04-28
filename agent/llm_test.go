package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdey/outcrop/store"
)

// fakeSuggester returns a canned response. tracks invocation count.
type fakeSuggester struct {
	response string
	called   int
}

func (f *fakeSuggester) Suggest(_ context.Context, _ Input, _ []store.Vault) string {
	f.called++
	return f.response
}

// slowSuggester sleeps until ctx is done OR delay elapses, then returns response.
type slowSuggester struct {
	delay    time.Duration
	response string
}

func (s slowSuggester) Suggest(ctx context.Context, _ Input, _ []store.Vault) string {
	select {
	case <-time.After(s.delay):
		return s.response
	case <-ctx.Done():
		return ""
	}
}

// staticHistory is a lightweight inner Scorer for these tests — keeps the
// vault order stable so we can assert on promotion exactly.
type staticHistory struct{}

func (staticHistory) Score(_ context.Context, _ Input, vaults []store.Vault) []store.Vault {
	out := append([]store.Vault(nil), vaults...)
	return out
}

func TestLLMScorer_PromoteOnHit(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	c := store.Vault{Key: "kC", DisplayName: "Charlie"}
	vaults := []store.Vault{a, b, c}

	sug := &fakeSuggester{response: "Charlie"}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	want := []store.Vault{c, a, b}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", names(got), names(want))
	}
	if sug.called != 1 {
		t.Errorf("suggester called %d times, want 1", sug.called)
	}
}

func TestLLMScorer_CaseInsensitivePromotion(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	vaults := []store.Vault{a, b}

	sug := &fakeSuggester{response: "BETA"}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	want := []store.Vault{b, a}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", names(got), names(want))
	}
}

func TestLLMScorer_EmptyResponseFallsThrough(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	vaults := []store.Vault{a, b}

	sug := &fakeSuggester{response: ""}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	if !reflect.DeepEqual(got, vaults) {
		t.Errorf("got %v, want unchanged %v", names(got), names(vaults))
	}
}

func TestLLMScorer_UnknownNameFallsThrough(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	vaults := []store.Vault{a, b}

	sug := &fakeSuggester{response: "Gamma"} // not in the list
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	if !reflect.DeepEqual(got, vaults) {
		t.Errorf("got %v, want unchanged %v", names(got), names(vaults))
	}
}

func TestLLMScorer_NoURLOrTitleSkipsLLM(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	vaults := []store.Vault{a, b}

	sug := &fakeSuggester{response: "Beta"}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{}, vaults) // empty URL + title
	if !reflect.DeepEqual(got, vaults) {
		t.Errorf("got %v, want unchanged %v", names(got), names(vaults))
	}
	if sug.called != 0 {
		t.Errorf("suggester called %d times, want 0", sug.called)
	}
}

func TestLLMScorer_TimeoutFallsThrough(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	vaults := []store.Vault{a, b}

	sug := slowSuggester{delay: 200 * time.Millisecond, response: "Beta"}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: 20 * time.Millisecond}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	if !reflect.DeepEqual(got, vaults) {
		t.Errorf("got %v, want unchanged %v on timeout", names(got), names(vaults))
	}
}

func TestLLMScorer_PromotePreservesRelativeOrder(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Alpha"}
	b := store.Vault{Key: "kB", DisplayName: "Beta"}
	c := store.Vault{Key: "kC", DisplayName: "Charlie"}
	d := store.Vault{Key: "kD", DisplayName: "Delta"}
	vaults := []store.Vault{a, b, c, d}

	sug := &fakeSuggester{response: "Charlie"}
	s := LLMScorer{Inner: staticHistory{}, Suggester: sug, Timeout: time.Second}
	got := s.Score(context.Background(), Input{URL: "https://x"}, vaults)
	want := []store.Vault{c, a, b, d}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", names(got), names(want))
	}
}

func TestBuildSuggestPrompt(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal", Description: "journaling, life admin"}
	b := store.Vault{Key: "kB", DisplayName: "Work Notes"} // no description
	c := store.Vault{Key: "kC", DisplayName: "Recipes", Description: "cooking, food"}

	t.Run("default rendered as inline fallback instruction", func(t *testing.T) {
		_, user := BuildSuggestPrompt(
			Input{URL: "https://example.com/sourdough", Title: "How to make sourdough", DefaultKey: "kA"},
			[]store.Vault{a, b, c},
		)
		for _, want := range []string{
			"URL: https://example.com/sourdough",
			"Title: How to make sourdough",
			"- Personal — journaling, life admin",
			"- Work Notes\n", // bare-name path: no em-dash
			"- Recipes — cooking, food",
			"reply with Personal",
			"Reply UNSURE only if",
			"Best match:",
		} {
			if !strings.Contains(user, want) {
				t.Errorf("user prompt missing %q\n--- prompt ---\n%s", want, user)
			}
		}
		if strings.Contains(user, "Default notebook") {
			t.Errorf("user prompt should not include the meta-label 'Default notebook':\n%s", user)
		}
	})

	t.Run("no default key → bare UNSURE instruction", func(t *testing.T) {
		_, user := BuildSuggestPrompt(
			Input{URL: "https://example.com/sourdough"},
			[]store.Vault{a, b, c},
		)
		if !strings.Contains(user, "reply UNSURE") {
			t.Errorf("expected bare UNSURE fallback when no default:\n%s", user)
		}
		if strings.Contains(user, "reply with") {
			t.Errorf("expected no concrete fallback name when no default:\n%s", user)
		}
	})

	t.Run("unknown default key → bare UNSURE instruction", func(t *testing.T) {
		_, user := BuildSuggestPrompt(
			Input{URL: "https://example.com/sourdough", DefaultKey: "k-deleted"},
			[]store.Vault{a, b, c},
		)
		if !strings.Contains(user, "reply UNSURE") {
			t.Errorf("expected bare UNSURE fallback for unknown default key:\n%s", user)
		}
		if strings.Contains(user, "reply with") {
			t.Errorf("expected no concrete fallback name for unknown default key:\n%s", user)
		}
	})
}

func TestParseSuggestResponse(t *testing.T) {
	a := store.Vault{Key: "kA", DisplayName: "Personal"}
	b := store.Vault{Key: "kB", DisplayName: "Work Notes"}
	c := store.Vault{Key: "kC", DisplayName: "Reading List"}
	vaults := []store.Vault{a, b, c}

	tests := []struct {
		reply string
		want  string
	}{
		// Strict / canonical.
		{"Personal", "Personal"},
		{" personal ", "Personal"},
		{"PERSONAL", "Personal"},
		{`"Personal"`, "Personal"},
		{"Personal.", "Personal"},
		{"Work Notes", "Work Notes"},

		// UNSURE / empty / unknown.
		{"UNSURE", ""},
		{" unsure", ""},
		{"", ""},
		{"   ", ""},
		{"Recipes", ""}, // not in vaults

		// Model echoes the description back: "name — description".
		{"Reading List — ML papers", "Reading List"},
		{"Reading List - ML papers, programming notes", "Reading List"},
		{"Reading List: ML papers", "Reading List"},

		// Model adds prose after the name.
		{"Reading List, because the article is about ML.", "Reading List"},
		{"Personal\n(life admin)", "Personal"},

		// Word-boundary safety: don't false-match a longer concatenation.
		{"PersonalLog", ""},
		{"Read", ""},

		// Longest prefix wins on ambiguous prose.
		{"Personal, Work Notes", "Personal"},
		{"Reading List Personal", "Reading List"},
	}
	for _, tt := range tests {
		t.Run(tt.reply, func(t *testing.T) {
			got := ParseSuggestResponse(tt.reply, vaults)
			if got != tt.want {
				t.Errorf("ParseSuggestResponse(%q) = %q, want %q", tt.reply, got, tt.want)
			}
		})
	}
}
