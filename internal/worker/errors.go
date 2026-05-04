package worker

import "strings"

// IsPermanentError проверяет, является ли ошибка постоянной (не retry)
func IsPermanentError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	permanentErrors := []string{
		"unknown format",
		"unsupported media type",
		"image: unknown format",
		"invalid JPEG",
		"invalid PNG",
		"corrupt",
		"format detection failed",
		"unsupported format:",
		"no handler for task type",
	}
	for _, pe := range permanentErrors {
		if strings.Contains(errStr, pe) {
			return true
		}
	}
	return false
}
