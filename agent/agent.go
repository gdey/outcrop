// Package agent ranks vaults for the popup picker. The default ranker is
// history-based (see RFD 0003); future implementations layer LLM signals on
// top per RFD 0005. The Scorer interface is the seam through which the
// HTTP handler obtains a ranked vault list — it doesn't care which scorer
// it has, only that it produced *some* ordering.
package agent

import (
	"context"

	"github.com/gdey/outcrop/store"
)

// Scorer ranks a vault list against the page being clipped. The input order
// is not meaningful; the output ranks first→best.
//
// Implementations don't return an error: the contract is that a Scorer always
// produces *some* ordering. Internal failures (history lookup, LLM timeout,
// etc.) degrade silently to whichever fallback the scorer composes with.
type Scorer interface {
	Score(ctx context.Context, in Input, vaults []store.Vault) []store.Vault
}

// Input is what the popup gives us when it opens. URL or Title may be empty
// (the popup omits them when no active tab is identifiable). DefaultKey is
// the key of the user's configured default vault; used by HistoryScorer to
// promote it to head when there's no history, and by BuildSuggestPrompt to
// give the LLM a concrete fallback option ("if uncertain, reply with the
// default notebook's name") instead of forcing UNSURE.
type Input struct {
	URL        string
	Title      string
	DefaultKey string
}

// VaultHistory is the read-side store dependency the HistoryScorer needs.
// store.Store satisfies it; tests fake it.
type VaultHistory interface {
	VaultKeysForDomain(ctx context.Context, domain string) ([]string, error)
}

// Suggester is the LLM-side seam for the pre-clip ranker. Implementations
// format the prompt (using vault display names + descriptions), call their
// configured model, and parse the response into a vault display name.
//
// Returns the chosen vault's display name on a confident match, or "" for
// "unsure" / failure / unknown. The "" path is the LLMScorer's signal to
// fall through to the inner Scorer's ranking unchanged.
type Suggester interface {
	Suggest(ctx context.Context, in Input, vaults []store.Vault) string
}

// VerboseSuggester is the diagnostic surface used by `outcrop agent test`.
// Implementations return the raw model reply alongside the parsed result so
// the caller can see what the model actually said when the parser rejects
// it, plus any transport / load error. Both HTTPSuggester and KronkSuggester
// satisfy it; the LLMScorer never calls it (production path uses Suggest).
type VerboseSuggester interface {
	Suggester
	SuggestVerbose(ctx context.Context, in Input, vaults []store.Vault) (parsed, raw string, err error)
}
