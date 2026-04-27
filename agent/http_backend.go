package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gdey/outcrop/store"
)

// HTTPSuggester implements Suggester over an OpenAI-compatible
// chat-completions endpoint (ollama, llama-server, kronk-server, vLLM, LM
// Studio, etc.). No streaming, single request type.
//
// All Suggester failure modes return "" — caller (LLMScorer) treats that as
// "fall through to the inner ranker." We never bubble an error up.
type HTTPSuggester struct {
	// Endpoint is the OpenAI-compatible base URL, e.g.
	// "http://127.0.0.1:11434/v1". Trailing slash is tolerated.
	Endpoint string

	// Model is the model identifier the backend recognises, e.g.
	// "llama3.2:3b" for ollama. Required.
	Model string

	// APIKey is sent as `Authorization: Bearer <key>` when non-empty. Local
	// backends ignore it; some hosted-style backends require it.
	APIKey string

	// Client is the underlying HTTP client. Defaults to http.DefaultClient
	// when nil. Tests override this.
	Client *http.Client
}

// chatCompletionsRequest is the subset of the OpenAI chat-completions payload
// we send. Stream is false; temperature is 0 for deterministic ranking.
type chatCompletionsRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionsResponse is the subset of the OpenAI chat-completions
// response we read. We only consume the first choice's message content.
type chatCompletionsResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Suggest implements Suggester. All failure modes (network, non-200, missing
// config, unparseable response) collapse to "" so callers (LLMScorer) treat
// them uniformly as "fall through."
func (s HTTPSuggester) Suggest(ctx context.Context, in Input, vaults []store.Vault) string {
	parsed, _, _ := s.SuggestVerbose(ctx, in, vaults)
	return parsed
}

// SuggestVerbose is Suggest with the raw model reply returned alongside the
// parsed result. Used by `outcrop agent test` to surface what the model
// actually said when the parser rejects it. Production scoring uses Suggest.
//
// Returns (parsed, raw, err) where:
//   - parsed is the matched display name (or "" for UNSURE / unknown / multi-match);
//   - raw is the model's response text (may be non-empty even when parsed is "");
//   - err is non-nil for transport / config failures (network, 5xx, missing
//     endpoint or model). A non-empty raw with parsed="" is *not* an error —
//     it just means the model said something the parser couldn't classify.
func (s HTTPSuggester) SuggestVerbose(ctx context.Context, in Input, vaults []store.Vault) (parsed, raw string, err error) {
	if s.Endpoint == "" || s.Model == "" {
		return "", "", errors.New("endpoint or model is empty")
	}
	system, user := BuildSuggestPrompt(in, vaults)
	raw, err = chatComplete(ctx, s.Endpoint, s.Model, s.APIKey, s.Client, system, user)
	if err != nil {
		return "", "", err
	}
	return ParseSuggestResponse(raw, vaults), raw, nil
}

// chatComplete sends one chat-completions request and returns the content of
// the first choice's message. Returns an error on any transport or response
// failure; the returned content may be empty if the model's response was
// empty.
func chatComplete(ctx context.Context, endpoint, model, apiKey string, client *http.Client, system, user string) (string, error) {
	body := chatCompletionsRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0,
		Stream:      false,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read up to 512 bytes of the error body for the message — backends
		// typically return JSON errors that help diagnose the cause.
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(excerpt)))
	}

	var out chatCompletionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("no choices in response")
	}
	return out.Choices[0].Message.Content, nil
}

// CheckEndpoint returns nil if the given endpoint base URL responds 2xx to a
// GET on /models (the OpenAI-compatible "list models" endpoint, supported by
// ollama and most OpenAI-compatible backends). Used by `outcrop agent
// status`. Returns an error describing the failure otherwise.
//
// Not part of the Suggester interface — a one-shot reachability probe with
// its own timeout, distinct from the Suggester's per-request budget.
func CheckEndpoint(ctx context.Context, endpoint, apiKey string, client *http.Client) error {
	if endpoint == "" {
		return errors.New("endpoint is empty")
	}
	url := strings.TrimRight(endpoint, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("endpoint returned HTTP %d", resp.StatusCode)
	}
	return nil
}
