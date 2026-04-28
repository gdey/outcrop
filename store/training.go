package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TrainingExample is one row of the training_examples table — the captured
// context for a single successful clip. Used by RFD 0011 fine-tuning
// pipelines.
//
// Denormalised on purpose: candidate_vaults is JSON of the user-visible
// vaults at capture time, so the example survives a later vault rename or
// delete. There is no FK to vaults — historical examples must outlive
// the vaults they reference.
type TrainingExample struct {
	ID                int64
	Time              time.Time
	Mode              string // "none" or "preclip"
	URL               string
	Title             string
	SelectedText      string
	Notes             string
	CandidateVaults   []CandidateVaultRef
	SuggestedVaultKey string // reserved; "" in v1
	ActualVaultKey    string
	AutoRouted        bool   // reserved; false in v1
	NotePath          string // vault-relative
	ImagePath         string // vault-relative
	ImageSHA256       string // reserved; "" in v1
}

// CandidateVaultRef captures the user-visible attributes of a vault at the
// moment of capture. Stored denormalised so renames / deletes don't break
// historical examples.
type CandidateVaultRef struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// RecordTrainingExample inserts a captured example. The caller decides
// whether to call this — typically gated on MetaTrainingDataEnabled.
func (s *Store) RecordTrainingExample(ctx context.Context, ex TrainingExample) error {
	if ex.Time.IsZero() {
		ex.Time = time.Now().UTC()
	}
	if ex.Mode == "" {
		return errors.New("training example mode is required")
	}
	if ex.ActualVaultKey == "" {
		return errors.New("training example actual_vault_key is required")
	}

	candidates, err := json.Marshal(ex.CandidateVaults)
	if err != nil {
		return fmt.Errorf("marshal candidate vaults: %w", err)
	}

	autoRouted := 0
	if ex.AutoRouted {
		autoRouted = 1
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO training_examples
		  (ts, mode, url, title, selected_text, notes,
		   candidate_vaults, suggested_vault_key, actual_vault_key,
		   auto_routed, note_path, image_path, image_sha256)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		ex.Time.Unix(), ex.Mode, ex.URL, ex.Title, ex.SelectedText, ex.Notes,
		string(candidates), ex.SuggestedVaultKey, ex.ActualVaultKey,
		autoRouted, ex.NotePath, ex.ImagePath, ex.ImageSHA256,
	)
	if err != nil {
		return fmt.Errorf("insert training example: %w", err)
	}
	return nil
}

// TrainingExampleCount returns the number of rows in training_examples.
func (s *Store) TrainingExampleCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM training_examples`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count training examples: %w", err)
	}
	return n, nil
}

// LastTrainingExampleTime returns the timestamp of the most recent example,
// or (zero, false, nil) if the table is empty.
func (s *Store) LastTrainingExampleTime(ctx context.Context) (time.Time, bool, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT ts FROM training_examples ORDER BY ts DESC LIMIT 1`).Scan(&ts)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false, nil
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read last training example time: %w", err)
	}
	return time.Unix(ts, 0).UTC(), true, nil
}

// TrainingSuggestionStats returns counts useful for `training-data status`:
//
//   - suggested: rows where the extension captured a system suggestion (the
//     vault that was at the top of the popup's pill when it loaded).
//   - overrides: rows where that suggestion differs from the vault the user
//     actually picked. The override signal is the highest-value labelled
//     pair for fine-tuning (RFD 0011).
//
// Treats both NULL and "" as "no suggestion captured" so the count is robust
// across mixed historical writes.
func (s *Store) TrainingSuggestionStats(ctx context.Context) (suggested, overrides int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM training_examples
			 WHERE suggested_vault_key IS NOT NULL AND suggested_vault_key != ''),
			(SELECT COUNT(*) FROM training_examples
			 WHERE suggested_vault_key IS NOT NULL AND suggested_vault_key != ''
			   AND suggested_vault_key != actual_vault_key)
	`).Scan(&suggested, &overrides)
	if err != nil {
		return 0, 0, fmt.Errorf("read training suggestion stats: %w", err)
	}
	return suggested, overrides, nil
}

// ClearTrainingExamples deletes every row from training_examples and resets
// the autoincrement counter so a fresh dataset starts at id=1. Returns the
// number of rows deleted.
//
// Destructive — callers should confirm with the user before invoking.
func (s *Store) ClearTrainingExamples(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM training_examples`)
	if err != nil {
		return 0, fmt.Errorf("clear training examples: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	// Best-effort: reset the autoincrement counter. Failure is benign — the
	// table is empty either way; ID values just start above the prior max.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sqlite_sequence WHERE name='training_examples'`)
	return int(n), nil
}
