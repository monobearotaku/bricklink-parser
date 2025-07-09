-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS part_details (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,
    data JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_part_details_category ON part_details (category);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS part_details;
-- +goose StatementEnd
