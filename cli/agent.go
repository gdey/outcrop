package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/store"
)

const (
	defaultAgentBackend   = "http"
	defaultAgentEndpoint  = "http://127.0.0.1:11434/v1"
	defaultAgentTimeoutMs = 1000
	agentStatusProbeMs    = 2000
)

// CmdAgent implements `outcrop agent <subcommand>`.
func CmdAgent(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outcrop agent <enable|disable|status|test> ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "enable":
		return cmdAgentEnable(rest)
	case "disable":
		return cmdAgentDisable(rest)
	case "status":
		return cmdAgentStatus(rest)
	case "test":
		return cmdAgentTest(rest)
	default:
		return fmt.Errorf("unknown agent subcommand %q", sub)
	}
}

func cmdAgentEnable(args []string) error {
	fs := flag.NewFlagSet("agent enable", flag.ContinueOnError)
	backend := fs.String("backend", defaultAgentBackend, `backend: "http" (default) or "kronk"`)
	model := fs.String("model", "", "model name on the endpoint (http) or path to GGUF (kronk); required")
	endpoint := fs.String("endpoint", defaultAgentEndpoint, "OpenAI-compatible base URL (http only)")
	apiKey := fs.String("api-key", "", "bearer token sent to the endpoint (http only)")
	timeoutMs := fs.Int("timeout", defaultAgentTimeoutMs, "LLM call timeout in milliseconds")
	if err := fs.Parse(args); err != nil {
		return err
	}

	switch *backend {
	case "http":
		// ok
	case "kronk":
		return fmt.Errorf("kronk backend is not yet implemented (RFD 0005 build order step 6); use --backend http for now")
	default:
		return fmt.Errorf("--backend must be \"http\" or \"kronk\", got %q", *backend)
	}
	if *model == "" {
		return fmt.Errorf("--model is required (model name on the endpoint for http; GGUF path for kronk)")
	}
	if *timeoutMs <= 0 {
		return fmt.Errorf("--timeout must be > 0")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	for _, kv := range []struct{ k, v string }{
		{store.MetaAgentBackend, *backend},
		{store.MetaAgentEndpoint, *endpoint},
		{store.MetaAgentModel, *model},
		{store.MetaAgentAPIKey, *apiKey},
		{store.MetaAgentTimeoutMs, strconv.Itoa(*timeoutMs)},
		{store.MetaAgentEnabled, "true"},
	} {
		if err := st.SetMeta(ctx, kv.k, kv.v); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stdout, "Agent enabled.\n  backend:  %s\n  endpoint: %s\n  model:    %s\n  timeout:  %dms\n", *backend, *endpoint, *model, *timeoutMs)
	if !isLoopbackEndpoint(*endpoint) {
		fmt.Fprintf(os.Stdout, "\nWARNING: %s is not a loopback address. Enabling the agent against a non-local\nendpoint sends URL, title, and vault names to a remote service.\n", *endpoint)
	}
	fmt.Fprintln(os.Stdout, "\nRestart `outcrop serve` to pick up the new config.")
	return nil
}

func cmdAgentDisable(args []string) error {
	fs := flag.NewFlagSet("agent disable", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetMeta(context.Background(), store.MetaAgentEnabled, "false"); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Agent disabled. Restart `outcrop serve` to drop the LLM scorer.")
	return nil
}

func cmdAgentStatus(args []string) error {
	fs := flag.NewFlagSet("agent status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	cfg, err := readAgentConfig(ctx, st)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Enabled:  %v\n", cfg.enabled)
	fmt.Fprintf(os.Stdout, "Backend:  %s\n", cfg.backend)
	fmt.Fprintf(os.Stdout, "Endpoint: %s\n", or(cfg.endpoint, "(unset)"))
	fmt.Fprintf(os.Stdout, "Model:    %s\n", or(cfg.model, "(unset)"))
	fmt.Fprintf(os.Stdout, "Timeout:  %dms\n", cfg.timeoutMs)
	if cfg.apiKey != "" {
		fmt.Fprintln(os.Stdout, "APIKey:   (set)")
	} else {
		fmt.Fprintln(os.Stdout, "APIKey:   (unset)")
	}

	if !cfg.enabled {
		return nil
	}

	if cfg.backend == "http" {
		fmt.Fprintf(os.Stdout, "\nProbing %s/models …\n", cfg.endpoint)
		probeCtx, cancel := context.WithTimeout(ctx, agentStatusProbeMs*time.Millisecond)
		defer cancel()
		if err := agent.CheckEndpoint(probeCtx, cfg.endpoint, cfg.apiKey, http.DefaultClient); err != nil {
			fmt.Fprintf(os.Stdout, "  unreachable: %v\n", err)
		} else {
			fmt.Fprintln(os.Stdout, "  reachable")
		}
	}
	return nil
}

func cmdAgentTest(args []string) error {
	fs := flag.NewFlagSet("agent test", flag.ContinueOnError)
	timeoutMs := fs.Int("timeout", 60000, "override the agent_timeout_ms for this call (default 60s — generous on purpose so first-call model loads complete)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: outcrop agent test [--timeout MS] <url> [<title>]")
	}
	rawURL := fs.Arg(0)
	title := ""
	if fs.NArg() == 2 {
		title = fs.Arg(1)
	}
	if *timeoutMs <= 0 {
		return fmt.Errorf("--timeout must be > 0")
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	cfg, err := readAgentConfig(ctx, st)
	if err != nil {
		return err
	}
	if !cfg.enabled {
		return errors.New("agent is disabled; run `outcrop agent enable` first")
	}

	vaults, err := st.ListVaults(ctx)
	if err != nil {
		return err
	}
	if len(vaults) == 0 {
		return errors.New("no vaults configured; nothing to suggest")
	}

	defaultKey, _ := st.Meta(ctx, store.MetaDefaultVaultKey)

	sug, err := buildSuggester(cfg)
	if err != nil {
		return err
	}

	in := agent.Input{URL: rawURL, Title: title, DefaultKey: defaultKey}
	sys, user := agent.BuildSuggestPrompt(in, vaults)
	fmt.Fprintln(os.Stdout, "--- system prompt ---")
	fmt.Fprintln(os.Stdout, sys)
	fmt.Fprintln(os.Stdout, "\n--- user prompt ---")
	fmt.Fprintln(os.Stdout, user)
	fmt.Fprintf(os.Stdout, "\n--- calling %s (%s, timeout %dms) ---\n", cfg.backend, cfg.model, *timeoutMs)

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(*timeoutMs)*time.Millisecond)
	defer cancel()

	var (
		parsed, raw string
		callErr     error
	)
	start := time.Now()
	if hsug, ok := sug.(agent.HTTPSuggester); ok {
		parsed, raw, callErr = hsug.SuggestVerbose(callCtx, in, vaults)
	} else {
		parsed = sug.Suggest(callCtx, in, vaults)
	}
	elapsed := time.Since(start)

	fmt.Fprintln(os.Stdout, "\n--- result ---")
	fmt.Fprintf(os.Stdout, "elapsed: %s\n", elapsed.Round(time.Millisecond))
	if callErr != nil {
		fmt.Fprintf(os.Stdout, "error: %v\n", callErr)
	}
	if raw != "" {
		fmt.Fprintf(os.Stdout, "raw response: %q\n", raw)
	}
	if parsed == "" {
		fmt.Fprintln(os.Stdout, "(no match — UNSURE / unknown / parsing failed / timeout)")
		if callErr == nil && raw == "" {
			fmt.Fprintln(os.Stdout, "If this was a cold start, ollama may have been loading the model.")
			fmt.Fprintln(os.Stdout, "Re-run the same command — the second call should be much faster.")
		}
	} else {
		fmt.Fprintf(os.Stdout, "matched: %s\n", parsed)
	}
	return nil
}

// agentConfig is the in-memory shape of the agent's meta keys, used by both
// the CLI and the serve-time scorer construction.
type agentConfig struct {
	enabled   bool
	backend   string
	endpoint  string
	model     string
	apiKey    string
	timeoutMs int
	modelPath string
}

func readAgentConfig(ctx context.Context, st *store.Store) (agentConfig, error) {
	var cfg agentConfig

	enabled, err := st.Meta(ctx, store.MetaAgentEnabled)
	if err != nil {
		return cfg, err
	}
	cfg.enabled = (enabled == "true")

	cfg.backend, _ = st.Meta(ctx, store.MetaAgentBackend)
	if cfg.backend == "" {
		cfg.backend = defaultAgentBackend
	}
	cfg.endpoint, _ = st.Meta(ctx, store.MetaAgentEndpoint)
	cfg.model, _ = st.Meta(ctx, store.MetaAgentModel)
	cfg.apiKey, _ = st.Meta(ctx, store.MetaAgentAPIKey)
	cfg.modelPath, _ = st.Meta(ctx, store.MetaAgentModelPath)

	timeoutStr, _ := st.Meta(ctx, store.MetaAgentTimeoutMs)
	if v, err := strconv.Atoi(timeoutStr); err == nil && v > 0 {
		cfg.timeoutMs = v
	} else {
		cfg.timeoutMs = defaultAgentTimeoutMs
	}

	return cfg, nil
}

// buildSuggester constructs the configured Suggester, or an error if config
// is incomplete / the backend isn't yet implemented.
func buildSuggester(cfg agentConfig) (agent.Suggester, error) {
	switch cfg.backend {
	case "http":
		if cfg.endpoint == "" || cfg.model == "" {
			return nil, errors.New("http backend needs both --endpoint and --model")
		}
		return agent.HTTPSuggester{Endpoint: cfg.endpoint, Model: cfg.model, APIKey: cfg.apiKey}, nil
	case "kronk":
		return nil, errors.New("kronk backend not yet implemented (RFD 0005 build order step 6)")
	default:
		return nil, fmt.Errorf("unknown backend %q", cfg.backend)
	}
}

// buildScorer assembles the Scorer that `outcrop serve` passes to server.New.
// When the agent is disabled or misconfigured, falls back to history-only.
func buildScorer(ctx context.Context, st *store.Store, log *slog.Logger) agent.Scorer {
	history := agent.HistoryScorer{History: st, Log: log}

	cfg, err := readAgentConfig(ctx, st)
	if err != nil {
		log.Warn("read agent config; falling back to history-only", "err", err)
		return history
	}
	if !cfg.enabled {
		return history
	}

	sug, err := buildSuggester(cfg)
	if err != nil {
		log.Warn("build agent suggester; falling back to history-only", "err", err)
		return history
	}

	log.Info("agent enabled",
		"backend", cfg.backend,
		"model", cfg.model,
		"endpoint", cfg.endpoint,
		"timeout_ms", cfg.timeoutMs)
	return agent.LLMScorer{
		Inner:     history,
		Suggester: sug,
		Timeout:   time.Duration(cfg.timeoutMs) * time.Millisecond,
		Log:       log,
	}
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// isLoopbackEndpoint is a cheap host check used only for the user-facing
// warning in `outcrop agent enable`. The HTTP server's strict loopback bind
// validation lives elsewhere.
func isLoopbackEndpoint(endpoint string) bool {
	s := strings.ToLower(endpoint)
	return strings.Contains(s, "://127.") ||
		strings.Contains(s, "://localhost") ||
		strings.Contains(s, "://[::1]") ||
		strings.Contains(s, "://::1")
}
