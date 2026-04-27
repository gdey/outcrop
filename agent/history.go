package agent

import (
	"context"
	"log/slog"
	"net/url"
	"sort"

	"golang.org/x/net/publicsuffix"

	"github.com/gdey/outcrop/store"
)

// HistoryScorer is the v1 heuristic from RFD 0003: vaults ranked
// most-recently-used per registrable domain, with the rest alphabetical by
// display name. Used standalone when the agent is disabled, and as the inner
// fallback for LLM-augmented scorers (RFD 0005).
//
// Log is optional. When non-nil, it gets a single WARN line when the history
// lookup itself fails — the resulting ranking is still returned (alphabetical
// fallback), the failure is non-fatal.
type HistoryScorer struct {
	History VaultHistory
	Log     *slog.Logger
}

// Score ranks the vault list for in.URL by looking up most-recently-used vault
// keys for the registrable domain. Empty URL or unrecognisable domain falls
// through to alphabetical by display name.
func (h HistoryScorer) Score(ctx context.Context, in Input, vaults []store.Vault) []store.Vault {
	var historyKeys []string
	if in.URL != "" {
		if domain := RegistrableDomain(in.URL); domain != "" {
			keys, err := h.History.VaultKeysForDomain(ctx, domain)
			if err != nil {
				if h.Log != nil {
					h.Log.Warn("history lookup", "err", err, "domain", domain)
				}
			} else {
				historyKeys = keys
			}
		}
	}
	return rankVaults(vaults, historyKeys)
}

// rankVaults merges the alphabetical vault list with a most-recently-used key
// list from history. Keys present in historyKeys (in order) lead the result;
// remaining vaults follow alphabetically by display name. Unknown keys in
// historyKeys (e.g., for vaults that were deleted) are silently skipped.
//
// Pure function: no IO, easy to test.
func rankVaults(vaults []store.Vault, historyKeys []string) []store.Vault {
	out := append([]store.Vault(nil), vaults...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].DisplayName < out[j].DisplayName
	})

	if len(historyKeys) == 0 {
		return out
	}

	byKey := make(map[string]store.Vault, len(out))
	for _, v := range out {
		byKey[v.Key] = v
	}

	ranked := make([]store.Vault, 0, len(out))
	seen := make(map[string]bool, len(out))
	for _, k := range historyKeys {
		v, ok := byKey[k]
		if !ok || seen[k] {
			continue
		}
		ranked = append(ranked, v)
		seen[k] = true
	}
	for _, v := range out {
		if !seen[v.Key] {
			ranked = append(ranked, v)
		}
	}
	return ranked
}

// RegistrableDomain returns the eTLD+1 for the given URL, or "" if the URL is
// not parsable or has no registrable domain (e.g., raw IPs, file URLs).
func RegistrableDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return ""
	}
	return d
}
