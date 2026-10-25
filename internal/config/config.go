// Package config locates Chaebot's saved data and loads .env files. It is
// shared by the command-line bot and the desktop app.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DataDir is where Chaebot keeps its saved state, overridable with
// CHAEBOT_DATA_DIR.
func DataDir() (string, error) {
	if dir := os.Getenv("CHAEBOT_DATA_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Chaebot"), nil
}

// LoadDotEnv sets KEY=VALUE pairs from path as environment variables,
// without overriding ones already set. A missing file is not an error.
func LoadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return nil
}
