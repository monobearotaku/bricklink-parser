package task

import "bricklink/parser/internal/domain"

type PageRetryTask struct {
	PageNumber   int                 `json:"page_number"`   // Failed page number
	CategoryType domain.CategoryType `json:"category_type"` // S, P, M, G, B
	RetryCount   int                 `json:"retry_count"`   // Number of times this page has been retried
	Error        string              `json:"error"`         // Error message from the original failure
}

func (t *PageRetryTask) TaskType() string {
	return "PageRetryTask"
}

func (t *PageRetryTask) TaskValue() ([]byte, error) {
	return DefaultTaskValue(t)
}
