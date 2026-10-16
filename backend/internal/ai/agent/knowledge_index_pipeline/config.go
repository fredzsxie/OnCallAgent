package knowledge_index_pipeline

import (
	"os"
	"path/filepath"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type Config struct {
	Root      string
	Knowledge config.Knowledge
}

func (c Config) validate() (Config, error) {
	if err := c.Knowledge.Validate(); err != nil {
		return c, err
	}
	if c.Root == "" {
		c.Root = "."
	}
	root, err := filepath.Abs(c.Root)
	if err != nil {
		return c, err
	}
	c.Root = root
	info, err := os.Stat(c.Root)
	if err != nil {
		return c, err
	}
	if !info.IsDir() {
		return c, &os.PathError{Op: "root", Path: c.Root, Err: os.ErrInvalid}
	}
	return c, nil
}
