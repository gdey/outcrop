-- +goose Up
-- +goose StatementBegin
ALTER TABLE vaults ADD COLUMN description TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE vaults DROP COLUMN description;
-- +goose StatementEnd
