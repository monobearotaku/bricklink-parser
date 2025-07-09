package task

import "bricklink/parser/internal/domain"

type ParseCatalogTreeTask struct {
	CategoryType domain.CategoryType `json:"category_type"`
	CategoryID   string              `json:"category_id"`
}

func (t *ParseCatalogTreeTask) TaskType() string {
	return "ParseCatalogTreeTask"
}

func (t *ParseCatalogTreeTask) TaskValue() ([]byte, error) {
	return DefaultTaskValue(t)
}
