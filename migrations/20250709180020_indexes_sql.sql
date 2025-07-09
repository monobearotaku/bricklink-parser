-- +goose Up
-- +goose StatementBegin

-- Create GIN index on the main data JSONB column for general JSON operations
CREATE INDEX IF NOT EXISTS idx_part_details_data_gin ON part_details USING GIN (data);

-- Create GIN indexes for specific JSON paths that are commonly queried
-- Index for item_number field
CREATE INDEX IF NOT EXISTS idx_part_details_item_number_gin ON part_details USING GIN ((data->'item_number'));

-- Index for item_name field
CREATE INDEX IF NOT EXISTS idx_part_details_item_name_gin ON part_details USING GIN ((data->'item_name'));

-- Index for colors array for efficient color searches
-- This supports queries like: data->'colors' @> '[{"id": "11"}]' or data->'colors' @> '[{"name": "Black"}]'
CREATE INDEX IF NOT EXISTS idx_part_details_colors_gin ON part_details USING GIN ((data->'colors'));

-- Index for full_category field
CREATE INDEX IF NOT EXISTS idx_part_details_category_gin ON part_details USING GIN ((data->'full_category'));

-- Index for year_released field
CREATE INDEX IF NOT EXISTS idx_part_details_year_gin ON part_details USING GIN ((data->'year_released'));

-- Index for appears_in array
CREATE INDEX IF NOT EXISTS idx_part_details_appears_in_gin ON part_details USING GIN ((data->'appears_in'));

-- Index for consists_of array
CREATE INDEX IF NOT EXISTS idx_part_details_consists_of_gin ON part_details USING GIN ((data->'consists_of'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop all GIN indexes
DROP INDEX IF EXISTS idx_part_details_data_gin;
DROP INDEX IF EXISTS idx_part_details_item_number_gin;
DROP INDEX IF EXISTS idx_part_details_item_name_gin;
DROP INDEX IF EXISTS idx_part_details_colors_gin;
DROP INDEX IF EXISTS idx_part_details_category_gin;
DROP INDEX IF EXISTS idx_part_details_year_gin;
DROP INDEX IF EXISTS idx_part_details_appears_in_gin;
DROP INDEX IF EXISTS idx_part_details_consists_of_gin;

-- +goose StatementEnd 