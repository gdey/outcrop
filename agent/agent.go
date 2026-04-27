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
// (the popup omits them when no active tab is identifiable).
type Input struct {
	URL   string
	Title string
}

// VaultHistory is the read-side store dependency the HistoryScorer needs.
// store.Store satisfies it; tests fake it.
type VaultHistory interface {
	VaultKeysForDomain(ctx context.Context, domain string) ([]string, error)
}
