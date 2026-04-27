-- +goose Up
-- +goose StatementBegin
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE vaults (
  key             TEXT PRIMARY KEY,
  display_name    TEXT NOT NULL,
  path            TEXT NOT NULL,
  clipping_path   TEXT NOT NULL DEFAULT 'Clippings',
  attachment_path TEXT NOT NULL DEFAULT 'Clippings/attachments',
  created_at      INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX vaults_display_name ON vaults(display_name);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE history (
  domain     TEXT NOT NULL,
  vault_key  TEXT NOT NULL,
  count      INTEGER NOT NULL DEFAULT 0,
  last_used  INTEGER NOT NULL,
  PRIMARY KEY (domain, vault_key),
  FOREIGN KEY (vault_key) REFERENCES vaults(key) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX history_domain_lastused ON history(domain, last_used DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS history_domain_lastused;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS history;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS vaults_display_name;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS vaults;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS meta;
-- +goose StatementEnd
