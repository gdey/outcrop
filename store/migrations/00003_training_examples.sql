-- +goose Up
-- +goose StatementBegin
CREATE TABLE training_examples (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  ts                  INTEGER NOT NULL,                       -- unix seconds, UTC
  mode                TEXT    NOT NULL,                       -- "none" (agent off) or "preclip" (agent on)
  url                 TEXT    NOT NULL,
  title               TEXT    NOT NULL,
  selected_text       TEXT    NOT NULL DEFAULT '',
  notes               TEXT    NOT NULL DEFAULT '',
  candidate_vaults    TEXT    NOT NULL,                       -- JSON: [{"key","displayName","description"}, ...] at capture time
  suggested_vault_key TEXT,                                   -- reserved (Refiner pick / Auto-route); NULL in v1
  actual_vault_key    TEXT    NOT NULL,                       -- vault written to; no FK so deletion doesn't lose history
  auto_routed         INTEGER NOT NULL DEFAULT 0,             -- bool; reserved for Auto-route
  note_path           TEXT    NOT NULL,                       -- vault-relative
  image_path          TEXT    NOT NULL,                       -- vault-relative
  image_sha256        TEXT    NOT NULL DEFAULT ''             -- reserved; empty in v1
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX training_examples_ts ON training_examples(ts DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS training_examples_ts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS training_examples;
-- +goose StatementEnd
