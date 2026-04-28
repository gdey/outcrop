package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"

	"github.com/gdey/outcrop/store"
)

// KronkSuggester implements Suggester using the kronk SDK with an embedded
// llama.cpp via yzma. The model is loaded lazily on the first Suggest call
// and held for the process lifetime; Close releases it.
//
// As with HTTPSuggester, all failure modes return "" — caller (LLMScorer)
// treats that as "fall through to the inner ranker."
type KronkSuggester struct {
	// ModelPath is the absolute path to a GGUF file. Required.
	ModelPath string

	mu     sync.Mutex
	loaded *kronk.Kronk
	err    error // sticky load error
}

// Suggest implements Suggester.
func (s *KronkSuggester) Suggest(ctx context.Context, in Input, vaults []store.Vault) string {
	parsed, _, _ := s.SuggestVerbose(ctx, in, vaults)
	return parsed
}

// SuggestVerbose mirrors HTTPSuggester.SuggestVerbose for symmetry — returns
// the raw model reply alongside the parse so `outcrop agent test` can show
// what the model actually said.
func (s *KronkSuggester) SuggestVerbose(ctx context.Context, in Input, vaults []store.Vault) (parsed, raw string, err error) {
	if s.ModelPath == "" {
		return "", "", errors.New("model path is empty")
	}

	krn, err := s.ensureLoaded(ctx)
	if err != nil {
		return "", "", err
	}

	system, user := BuildSuggestPrompt(in, vaults)
	d := model.D{
		"messages": []model.D{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
		"stream":      false,
	}

	resp, err := krn.Chat(ctx, d)
	if err != nil {
		return "", "", err
	}
	raw = chatResponseContent(resp)
	return ParseSuggestResponse(raw, vaults), raw, nil
}

// Close releases the underlying model. Safe to call multiple times.
func (s *KronkSuggester) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded == nil {
		return nil
	}
	err := s.loaded.Unload(ctx)
	s.loaded = nil
	return err
}

func (s *KronkSuggester) ensureLoaded(ctx context.Context) (*kronk.Kronk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	if s.loaded != nil {
		return s.loaded, nil
	}
	// kronk's backend (yzma + libllama) needs an explicit one-time Init that
	// dlopens the shared library. Subsequent calls are no-ops; safe to invoke
	// here on every load attempt. If the library isn't installed at the
	// expected location, Init returns a clear error pointing at the path.
	if err := kronk.Init(); err != nil {
		s.err = err
		return nil, err
	}
	krn, err := kronk.NewWithContext(ctx, model.WithModelFiles([]string{s.ModelPath}))
	if err != nil {
		s.err = err
		return nil, err
	}
	s.loaded = krn
	return krn, nil
}

// chatResponseContent extracts the assistant message content from a kronk
// non-streaming chat response. Defensive against shape changes — falls back
// to "" if the expected fields aren't present.
func chatResponseContent(resp model.ChatResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}
