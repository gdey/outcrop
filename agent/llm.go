package agent

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/gdey/outcrop/store"
)

// LLMScorer wraps an inner Scorer with an LLM Suggester. The inner Scorer
// produces the baseline ranking; if the Suggester names a vault in that
// baseline, it's promoted to the head. On any failure (timeout, empty /
// UNSURE response, name not in the list) the baseline is returned unchanged.
//
// The LLM call runs with a context deadline of Timeout; if the deadline
// fires, the Suggester's response (if any) is discarded.
type LLMScorer struct {
	Inner     Scorer
	Suggester Suggester
	Timeout   time.Duration
	Log       *slog.Logger
}

// Score implements Scorer.
func (l LLMScorer) Score(ctx context.Context, in Input, vaults []store.Vault) []store.Vault {
	base := l.Inner.Score(ctx, in, vaults)

	// No URL or title → nothing to ask the LLM about. Skip the call.
	if in.URL == "" && in.Title == "" {
		return base
	}
	// No Suggester → just the inner ranking.
	if l.Suggester == nil {
		return base
	}

	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 1 * time.Second
	}
	sCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := l.Suggester.Suggest(sCtx, in, base)
	if name == "" {
		return base
	}

	return promote(base, name)
}

// promote moves the vault with the matching display name (case-insensitive)
// to the head of the slice. Returns a new slice; the input is not mutated.
// Unknown name → input is returned (a copy, but order-equivalent).
func promote(vaults []store.Vault, name string) []store.Vault {
	target := strings.ToLower(strings.TrimSpace(name))
	for i, v := range vaults {
		if strings.ToLower(v.DisplayName) == target {
			out := make([]store.Vault, 0, len(vaults))
			out = append(out, v)
			out = append(out, vaults[:i]...)
			out = append(out, vaults[i+1:]...)
			return out
		}
	}
	return vaults
}
