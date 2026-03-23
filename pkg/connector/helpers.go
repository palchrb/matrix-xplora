package connector

import (
	"errors"
	"os"
)

// removeFileImpl removes a file by path.
func removeFileImpl(path string) error {
	return os.Remove(path)
}

// isNotExist reports whether the error is "file not found".
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// suppress unused warning — isNotExist available for future use
var _ = isNotExist
