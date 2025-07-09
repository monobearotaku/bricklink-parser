package task

import "bricklink/parser/internal/domain"

type CatalogPageTask struct {
	PageNumber   int                      `json:"page_number"`   // Current page number
	CategoryType domain.CategoryType      `json:"category_type"` // S, P, M, G, B
	Items        []domain.CatalogPageItem `json:"items"`         // Basic item info for queuing
}

func (t *CatalogPageTask) TaskType() string {
	return "CatalogPageTask"
}

func (t *CatalogPageTask) TaskValue() ([]byte, error) {
	return DefaultTaskValue(t)
}
