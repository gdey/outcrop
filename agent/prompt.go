package agent

import (
	"fmt"
	"strings"

	"github.com/gdey/outcrop/store"
)

// suggestSystemPrompt is the constant system message for the pre-clip
// Suggester. Backends prepend this to the user message that BuildSuggestPrompt
// produces.
//
// Concrete fallback instructions (the literal default-vault name, the UNSURE
// option) live in the *user* prompt rather than here, so the model sees the
// actual notebook name spelled out alongside the choices instead of having
// to reason about a meta-label like "the default notebook." Small models
// otherwise echo the meta-label back as their answer.
const suggestSystemPrompt = `You route a webpage clip to one of the user's notebooks based on the page's topic.

Match the page to the notebook whose description is the closest fit.

Each notebook below is shown as either just its name, or "name — description".
Reply with ONLY the notebook name (the part before the em-dash, if any).
Do NOT include the description, quotes, dashes, or any other text.`

// BuildSuggestPrompt formats the (system, user) message pair the pre-clip
// Suggester sends to the model. URL and title come from Input; the user-facing
// vault list comes from `vaults` (and includes descriptions when present).
//
// When in.DefaultKey names a vault in the list, the user prompt's tail
// instructs the model to fall back to that vault by *name* ("If no notebook
// clearly fits, reply with Personal") rather than via a meta-label the model
// might echo back as its answer.
func BuildSuggestPrompt(in Input, vaults []store.Vault) (system, user string) {
	system = suggestSystemPrompt

	var defaultName string
	if in.DefaultKey != "" {
		for _, v := range vaults {
			if v.Key == in.DefaultKey {
				defaultName = v.DisplayName
				break
			}
		}
	}

	var b strings.Builder
	if in.URL != "" {
		fmt.Fprintf(&b, "URL: %s\n", in.URL)
	}
	if in.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", in.Title)
	}
	b.WriteString("\nNotebooks:\n")
	for _, v := range vaults {
		if v.Description != "" {
			fmt.Fprintf(&b, "- %s — %s\n", v.DisplayName, v.Description)
		} else {
			fmt.Fprintf(&b, "- %s\n", v.DisplayName)
		}
	}
	if defaultName != "" {
		fmt.Fprintf(&b, "\nIf no notebook clearly fits this page, reply with %s.\nReply UNSURE only if the page is genuinely unrelated to every notebook above.\n", defaultName)
	} else {
		b.WriteString("\nIf no notebook clearly fits this page, reply UNSURE.\n")
	}
	b.WriteString("\nBest match:")
	user = b.String()
	return
}

// ParseSuggestResponse parses the LLM's reply against the vault list. Returns
// the matched display name, or "" for: empty / UNSURE / no recognised vault.
//
// Matching is "longest prefix where the character after the match is not a
// letter or digit." This deliberately tolerates models that echo extra
// content the system prompt told them not to include, e.g.:
//
//	"Reading List"                -> Reading List
//	"Reading List — ML papers"    -> Reading List
//	"Reading List, because…"      -> Reading List
//	"READING LIST."               -> Reading List
//
// And rejects prefix collisions:
//
//	"ReadingListMatches"          -> ""    (next char 'M' is alphanumeric)
//	"Read"                        -> ""    (no vault named "Read")
//
// On ambiguous prose like "Personal Reading List", the longest prefix wins —
// the model led with "Personal", that's its pick.
//
// "" is the contract for "fall back" — callers never branch on an error.
func ParseSuggestResponse(reply string, vaults []store.Vault) string {
	s := strings.TrimSpace(reply)
	// Strip leading/trailing decoration the model might wrap around its answer.
	s = strings.Trim(s, "*-•—\"'`[]() \t\n.,;:!?")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "UNSURE") {
		return ""
	}

	lower := strings.ToLower(s)

	var best string
	for _, v := range vaults {
		name := strings.ToLower(v.DisplayName)
		if len(name) == 0 || len(name) > len(lower) {
			continue
		}
		if lower[:len(name)] != name {
			continue
		}
		// Boundary: if the response is longer than the matched name, the next
		// character must not be a letter or digit, so we don't match "Read"
		// against "Reading List" or vice versa.
		if len(lower) > len(name) {
			next := lower[len(name)]
			if isLetterOrDigit(next) {
				continue
			}
		}
		if len(v.DisplayName) > len(best) {
			best = v.DisplayName
		}
	}
	return best
}

func isLetterOrDigit(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
