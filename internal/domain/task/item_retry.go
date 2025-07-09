package task

import "bricklink/parser/internal/domain"

type ItemRetryTask struct {
	ItemURL      string              `json:"item_url"`      // URL of the failed item
	ItemType     domain.CategoryType `json:"item_type"`     // S, P, M, G, B
	ItemID       string              `json:"item_id"`       // Item ID extracted from URL
	RetryCount   int                 `json:"retry_count"`   // Number of times this item has been retried
	Error        string              `json:"error"`         // Error message from the original failure
	FailureStage string              `json:"failure_stage"` // "fetch" or "save" - which stage failed
}

func (t *ItemRetryTask) TaskType() string {
	return "ItemRetryTask"
}

func (t *ItemRetryTask) TaskValue() ([]byte, error) {
	return DefaultTaskValue(t)
}
