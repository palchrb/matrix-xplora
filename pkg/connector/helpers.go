package connector

import (
	"os"
)

// removeFileImpl removes a file by path.
func removeFileImpl(path string) error {
	return os.Remove(path)
}
