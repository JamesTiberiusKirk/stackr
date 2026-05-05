package reconcile

import "os"

// osMkdirAll is a thin wrapper kept in a test-only file so reconcile's
// production source has no os import.
func osMkdirAll(p string) error {
	return os.MkdirAll(p, 0o755)
}
