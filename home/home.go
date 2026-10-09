// Package home resolves where Synapse keeps its local state. There is nothing to configure:
// the default is a per-user directory, and SYNAPSE_HOME overrides it for tests and containers.
package home

import (
	"os"
	"path/filepath"
)

// Dir returns the directory for keys, usage logs and caches. It does not create it.
func Dir() string {
	if dir := os.Getenv("SYNAPSE_HOME"); dir != "" {
		return dir
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "synapse")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".synapse")
	}
	return ".synapse"
}

// Ensure creates the directory (and any sub-directories) with private permissions and returns
// its path.
func Ensure(sub ...string) (string, error) {
	path := filepath.Join(append([]string{Dir()}, sub...)...)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return path, nil
}
