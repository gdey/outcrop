package agent

// RecommendedModel describes a GGUF model the user can download with
// `outcrop agent download`. The list is intentionally short and curated;
// users with a model not on this list pass it explicitly via
// `outcrop agent enable --model PATH`.
//
// SHA256 may be empty when we don't have an authoritative hash baked in;
// the download command warns and skips integrity verification in that case.
// Production releases should pin a non-empty hash before shipping.
type RecommendedModel struct {
	ID          string // stable identifier used by `agent download --model ID`
	DisplayName string // human-readable
	Filename    string // local filename under the models dir
	URL         string // direct HTTPS download URL
	SizeBytes   int64  // approximate, used in the user-facing prompt
	SHA256      string // hex-encoded; empty = skip verification
	Vision      bool   // model supports vision inputs (Auto-route, RFD 0005 §"Auto-route")
	Tagline     string // one-line summary for CLI display
}

// RecommendedModels is the curated list. Order matters: the first non-vision
// entry is the default for `agent download` with no flags; the first vision
// entry is the default with `--vision`.
var RecommendedModels = []RecommendedModel{
	{
		ID:          "qwen2.5-3b-instruct",
		DisplayName: "Qwen2.5-3B-Instruct (Q4_K_M)",
		Filename:    "qwen2.5-3b-instruct-q4_k_m.gguf",
		URL:         "https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf",
		SizeBytes:   2_000_000_000, // ~1.93 GB
		SHA256:      "",            // unverified — download command will warn
		Vision:      false,
		Tagline:     "Small, fast text model. Good default for vault routing. ~2 GB.",
	},
	{
		ID:          "llama-3.2-3b-instruct",
		DisplayName: "Llama-3.2-3B-Instruct (Q4_K_M)",
		Filename:    "llama-3.2-3b-instruct-q4_k_m.gguf",
		URL:         "https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		SizeBytes:   2_020_000_000, // ~1.88 GB
		SHA256:      "",
		Vision:      false,
		Tagline:     "Alternative text model. Comparable size and quality to Qwen2.5-3B.",
	},
}

// LookupRecommended returns the entry matching id, or nil if none.
func LookupRecommended(id string) *RecommendedModel {
	for i := range RecommendedModels {
		if RecommendedModels[i].ID == id {
			return &RecommendedModels[i]
		}
	}
	return nil
}

// DefaultRecommended returns the default recommended model. If vision is
// true, returns the first vision-capable entry; otherwise the first
// non-vision entry. Returns nil if no entry matches.
func DefaultRecommended(vision bool) *RecommendedModel {
	for i := range RecommendedModels {
		if RecommendedModels[i].Vision == vision {
			return &RecommendedModels[i]
		}
	}
	return nil
}
