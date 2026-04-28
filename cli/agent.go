package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gdey/outcrop/agent"
	"github.com/gdey/outcrop/store"
)

const (
	defaultAgentBackend   = "kronk" // RFD 0005 §"Recommendation": in-process by default
	defaultAgentEndpoint  = "http://127.0.0.1:11434/v1"
	defaultAgentTimeoutMs = 1000
	agentStatusProbeMs    = 2000
)

// CmdAgent implements `outcrop agent <subcommand>`.
func CmdAgent(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outcrop agent <enable|disable|status|test|download|install-libs> ...")
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
	case "download":
		return cmdAgentDownload(rest)
	case "install-libs":
		return cmdAgentInstallLibs(rest)
	default:
		return fmt.Errorf("unknown agent subcommand %q", sub)
	}
}

func cmdAgentInstallLibs(args []string) error {
	fs := flag.NewFlagSet("agent install-libs", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Installing kronk's llama.cpp libraries (one-time, ~50 MB).")
	if err := agent.EnsureKronkLibs(context.Background(), func(msg string) {
		fmt.Fprintf(os.Stdout, "  %s\n", msg)
	}); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Libraries installed and validated.")
	return nil
}

func cmdAgentEnable(args []string) error {
	fs := flag.NewFlagSet("agent enable", flag.ContinueOnError)
	backend := fs.String("backend", defaultAgentBackend, `backend: "kronk" (in-process; default) or "http"`)
	model := fs.String("model", "", "model name on the endpoint (http) or path to GGUF (kronk)")
	endpoint := fs.String("endpoint", defaultAgentEndpoint, "OpenAI-compatible base URL (http only)")
	apiKey := fs.String("api-key", "", "bearer token sent to the endpoint (http only)")
	timeoutMs := fs.Int("timeout", defaultAgentTimeoutMs, "LLM call timeout in milliseconds")
	download := fs.Bool("download", false, "kronk: download the recommended default model if --model is not provided (skips the interactive prompt)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeoutMs <= 0 {
		return fmt.Errorf("--timeout must be > 0")
	}

	switch *backend {
	case "http":
		if *model == "" {
			return fmt.Errorf("--model is required for the http backend (the model name on the endpoint, e.g. \"llama3.2:3b\")")
		}
	case "kronk":
		// Step 1/2: ensure the llama.cpp shared libraries are installed.
		// Idempotent; cheap when already present.
		fmt.Fprintln(os.Stdout, "Ensuring kronk's llama.cpp libraries are installed…")
		if err := agent.EnsureKronkLibs(context.Background(), func(msg string) {
			fmt.Fprintf(os.Stdout, "  %s\n", msg)
		}); err != nil {
			return err
		}

		// Step 2/2: resolve the model file (download if needed).
		resolved, err := resolveKronkModelPath(*model, *download)
		if err != nil {
			return err
		}
		*model = resolved
	default:
		return fmt.Errorf("--backend must be \"http\" or \"kronk\", got %q", *backend)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()

	// For the kronk backend we store the path under MetaAgentModelPath; for
	// http we store the model name under MetaAgentModel. Keep the unused key
	// cleared so `agent status` doesn't show stale info.
	var settings []struct{ k, v string }
	switch *backend {
	case "kronk":
		settings = []struct{ k, v string }{
			{store.MetaAgentBackend, *backend},
			{store.MetaAgentModelPath, *model},
			{store.MetaAgentModel, ""},
			{store.MetaAgentEndpoint, ""},
			{store.MetaAgentAPIKey, ""},
			{store.MetaAgentTimeoutMs, strconv.Itoa(*timeoutMs)},
			{store.MetaAgentEnabled, "true"},
		}
	case "http":
		settings = []struct{ k, v string }{
			{store.MetaAgentBackend, *backend},
			{store.MetaAgentModelPath, ""},
			{store.MetaAgentModel, *model},
			{store.MetaAgentEndpoint, *endpoint},
			{store.MetaAgentAPIKey, *apiKey},
			{store.MetaAgentTimeoutMs, strconv.Itoa(*timeoutMs)},
			{store.MetaAgentEnabled, "true"},
		}
	}
	for _, kv := range settings {
		if err := st.SetMeta(ctx, kv.k, kv.v); err != nil {
			return err
		}
	}

	switch *backend {
	case "kronk":
		fmt.Fprintf(os.Stdout, "Agent enabled.\n  backend:  kronk (in-process)\n  model:    %s\n  timeout:  %dms\n", *model, *timeoutMs)
	case "http":
		fmt.Fprintf(os.Stdout, "Agent enabled.\n  backend:  http\n  endpoint: %s\n  model:    %s\n  timeout:  %dms\n", *endpoint, *model, *timeoutMs)
		if !isLoopbackEndpoint(*endpoint) {
			fmt.Fprintf(os.Stdout, "\nWARNING: %s is not a loopback address. Enabling the agent against a non-local\nendpoint sends URL, title, and vault names to a remote service.\n", *endpoint)
		}
	}
	fmt.Fprintln(os.Stdout, "\nRestart `outcrop serve` to pick up the new config.")
	return nil
}

// resolveKronkModelPath picks the GGUF path for the kronk backend.
// Precedence:
//  1. Explicit --model PATH (the user knows what they want).
//  2. --download flag → fetch the default recommended model now, no prompt.
//  3. Interactive TTY → prompt to download the default.
//  4. Non-interactive with no model and no --download → error with hint.
//
// Always returns an absolute path that exists (or an error explaining why
// no path could be resolved).
func resolveKronkModelPath(modelArg string, downloadFlag bool) (string, error) {
	if modelArg != "" {
		// Treat as a path. Allow the file to not yet exist — kronk will fail
		// at load time with a clearer error than we can produce here.
		if abs, err := filepath.Abs(modelArg); err == nil {
			return abs, nil
		}
		return modelArg, nil
	}

	rec := agent.DefaultRecommended(false)
	if rec == nil {
		return "", errors.New("--model is required for the kronk backend, and no recommended-default model is configured")
	}

	// If the recommended model's destination file is already on disk, use it
	// without prompting — the user already paid the download cost on a prior
	// run, and asking again is just noise.
	if dir, err := ModelsDir(); err == nil {
		expected := filepath.Join(dir, rec.Filename)
		if info, statErr := os.Stat(expected); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			fmt.Fprintf(os.Stdout, "Using existing model file: %s\n", expected)
			return expected, nil
		}
	}

	if downloadFlag {
		fmt.Fprintf(os.Stdout, "Downloading recommended default model: %s\n", rec.DisplayName)
		path, err := downloadModel(context.Background(), rec, false)
		if err != nil {
			return "", err
		}
		return path, nil
	}

	if !stdinIsTTY() {
		return "", fmt.Errorf("no --model provided and stdin is not a TTY; pass --model PATH or --download, or run `outcrop agent download` first")
	}

	fmt.Fprintf(os.Stdout, "No model file configured. Download recommended default?\n  %s\n  size: ~%s\n  url:  %s\nProceed? [Y/n] ",
		rec.DisplayName, humanBytes(rec.SizeBytes), rec.URL)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
	case "n", "no":
		return "", errors.New("declined; pass --model PATH or run `outcrop agent download`")
	default:
		return "", fmt.Errorf("unrecognised response %q; expected y/n", strings.TrimSpace(line))
	}

	path, err := downloadModel(context.Background(), rec, false)
	if err != nil {
		return "", err
	}
	return path, nil
}

// stdinIsTTY reports whether stdin is connected to a terminal. Used to gate
// interactive prompts from the agent CLI.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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

	fmt.Fprintf(os.Stdout, "Enabled:    %v\n", cfg.enabled)
	fmt.Fprintf(os.Stdout, "Backend:    %s\n", cfg.backend)
	switch cfg.backend {
	case "http":
		fmt.Fprintf(os.Stdout, "Endpoint:   %s\n", or(cfg.endpoint, "(unset)"))
		fmt.Fprintf(os.Stdout, "Model:      %s\n", or(cfg.model, "(unset)"))
		if cfg.apiKey != "" {
			fmt.Fprintln(os.Stdout, "APIKey:     (set)")
		} else {
			fmt.Fprintln(os.Stdout, "APIKey:     (unset)")
		}
	case "kronk":
		fmt.Fprintf(os.Stdout, "Model path: %s\n", or(cfg.modelPath, "(unset)"))
	}
	fmt.Fprintf(os.Stdout, "Timeout:    %dms\n", cfg.timeoutMs)

	if !cfg.enabled {
		return nil
	}

	switch cfg.backend {
	case "http":
		fmt.Fprintf(os.Stdout, "\nProbing %s/models …\n", cfg.endpoint)
		probeCtx, cancel := context.WithTimeout(ctx, agentStatusProbeMs*time.Millisecond)
		defer cancel()
		if err := agent.CheckEndpoint(probeCtx, cfg.endpoint, cfg.apiKey, http.DefaultClient); err != nil {
			fmt.Fprintf(os.Stdout, "  unreachable: %v\n", err)
		} else {
			fmt.Fprintln(os.Stdout, "  reachable")
		}
	case "kronk":
		fmt.Fprintln(os.Stdout, "\nChecking model file…")
		if cfg.modelPath == "" {
			fmt.Fprintln(os.Stdout, "  no path configured (run `outcrop agent enable --backend kronk` to set up)")
		} else if info, err := os.Stat(cfg.modelPath); err != nil {
			fmt.Fprintf(os.Stdout, "  not found: %v\n", err)
		} else if !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stdout, "  not a regular file: %s\n", cfg.modelPath)
		} else {
			fmt.Fprintf(os.Stdout, "  present: %s (%s)\n", cfg.modelPath, humanBytes(info.Size()))
		}

		fmt.Fprintln(os.Stdout, "\nProbing kronk libraries…")
		if err := agent.ProbeKronk(); err != nil {
			fmt.Fprintf(os.Stdout, "  not loadable: %v\n", err)
			fmt.Fprintln(os.Stdout, "  run `outcrop agent install-libs` to install them")
		} else {
			fmt.Fprintln(os.Stdout, "  loaded successfully")
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
	fmt.Fprintf(os.Stdout, "\n--- calling %s (%s, timeout %dms) ---\n", cfg.backend, modelLabel(cfg), *timeoutMs)

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(*timeoutMs)*time.Millisecond)
	defer cancel()

	var (
		parsed, raw string
		callErr     error
	)
	start := time.Now()
	if vsug, ok := sug.(agent.VerboseSuggester); ok {
		parsed, raw, callErr = vsug.SuggestVerbose(callCtx, in, vaults)
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
		fmt.Fprintln(os.Stdout, "(no match — UNSURE / unknown / parsing failed / timeout / load error)")
		if callErr == nil && raw == "" {
			fmt.Fprintln(os.Stdout, "If this was a cold start, the backend may have been loading the model.")
			fmt.Fprintln(os.Stdout, "Re-run the same command — the second call should be much faster.")
		}
	} else {
		fmt.Fprintf(os.Stdout, "matched: %s\n", parsed)
	}
	return nil
}

// modelLabel renders the model identifier for a given backend in a way that
// reads correctly in CLI output. For the kronk backend the path can be very
// long (under ~/Library/Application Support/outcrop/models/…); we strip the
// configured models-dir prefix when present so the line stays readable.
func modelLabel(cfg agentConfig) string {
	switch cfg.backend {
	case "kronk":
		if cfg.modelPath == "" {
			return "(no model path)"
		}
		if dir, err := ModelsDir(); err == nil && dir != "" {
			if rel, err := filepath.Rel(dir, cfg.modelPath); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
		return filepath.Base(cfg.modelPath)
	case "http":
		return or(cfg.model, "(no model)")
	default:
		return "(unknown backend)"
	}
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
// is incomplete.
func buildSuggester(cfg agentConfig) (agent.Suggester, error) {
	switch cfg.backend {
	case "http":
		if cfg.endpoint == "" || cfg.model == "" {
			return nil, errors.New("http backend needs both --endpoint and --model")
		}
		return agent.HTTPSuggester{Endpoint: cfg.endpoint, Model: cfg.model, APIKey: cfg.apiKey}, nil
	case "kronk":
		if cfg.modelPath == "" {
			return nil, errors.New("kronk backend needs --model PATH (a GGUF file). Run `outcrop agent download` to fetch a recommended one.")
		}
		return &agent.KronkSuggester{ModelPath: cfg.modelPath}, nil
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
