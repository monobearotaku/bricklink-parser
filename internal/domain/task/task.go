package task

import "encoding/json"

type Task interface {
	TaskType() string
	TaskValue() ([]byte, error)
}

// DefaultTaskValue provides a common implementation for TaskValue
func DefaultTaskValue(task interface{}) ([]byte, error) {
	return json.Marshal(task)
}

func UnmarshalTask[T Task](task []byte) (T, error) {
	var t T
	err := json.Unmarshal(task, &t)
	return t, err
}