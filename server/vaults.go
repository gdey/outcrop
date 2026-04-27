package server

import (
	"net/http"
	"net/url"
	"sort"

	"golang.org/x/net/publicsuffix"

	"github.com/gdey/outcrop/store"
)

// vaultDTO is the JSON shape for a vault entry on /vaults.
type vaultDTO struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	IsDefault   bool   `json:"isDefault"`
}

func (s *Server) handleListVaults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vaults, err := s.store.ListVaults(ctx)
	if err != nil {
		s.log.Error("list vaults", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "list vaults failed")
		return
	}

	defaultKey, err := s.store.Meta(ctx, store.MetaDefaultVaultKey)
	if err != nil {
		s.log.Error("read default vault", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "read default vault failed")
		return
	}

	var historyKeys []string
	if rawURL := r.URL.Query().Get("url"); rawURL != "" {
		if domain := registrableDomain(rawURL); domain != "" {
			historyKeys, err = s.store.VaultKeysForDomain(ctx, domain)
			if err != nil {
				// History is best-effort for ranking; degrade to alphabetical.
				s.log.Warn("history lookup", "err", err, "domain", domain)
				historyKeys = nil
			}
		}
	}

	ranked := rankVaults(vaults, historyKeys)

	out := make([]vaultDTO, 0, len(ranked))
	for _, v := range ranked {
		out = append(out, vaultDTO{
			Key:         v.Key,
			DisplayName: v.DisplayName,
			IsDefault:   v.Key == defaultKey,
		})
	}
	writeJSON(w, out)
}

// rankVaults merges the alphabetical vault list with a most-recently-used key
// list from history. Keys present in historyKeys (in order) lead the result;
// remaining vaults follow alphabetically by display name. Unknown keys in
// historyKeys (e.g., for vaults that were deleted) are silently skipped.
//
// Pure function: no IO, no logger, easy to test.
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

// registrableDomain returns the eTLD+1 for the given URL, or "" if the URL is
// not parsable or has no registrable domain (e.g., raw IPs, file URLs).
func registrableDomain(rawURL string) string {
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
