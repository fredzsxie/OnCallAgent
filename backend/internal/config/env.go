package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// LoadEnv loads local values before config parsing without replacing process variables.
// Precedence: existing environment > .env > defaults.
func LoadEnv() error {
	// Load only the first file; backend/.env supports binaries launched from the repo root.
	for _, path := range []string{".env", filepath.Join("backend", ".env")} {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot access %s", path)
		}
		if info.IsDir() {
			return fmt.Errorf("%s must be a file", path)
		}
		if err := godotenv.Load(path); err != nil {
			// Parser errors can include a complete secret-bearing line; do not wrap them.
			return fmt.Errorf("cannot load %s: check KEY=value syntax and file permissions", path)
		}
		return nil
	}
	return nil
}
