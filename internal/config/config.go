package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Defaults for a config file that does not exist yet.
const (
	DefaultResultLimitRows  = 10_000
	DefaultResultLimitBytes = 32 << 20
	DefaultMongoSampleSize  = 50
)

// Config is the user's config.toml. Unknown keys are rejected: a typo in a
// config file must fail loudly instead of being silently ignored.
type Config struct {
	ReadOnly         bool        `toml:"read_only"`
	ResultLimitRows  int         `toml:"result_limit_rows"`
	ResultLimitBytes int64       `toml:"result_limit_bytes"`
	MongoDB          MongoConfig `toml:"mongodb"`
	// Mouse turns on terminal mouse reporting, which the middle button needs.
	// It is off by default: reporting takes plain text selection away from the
	// terminal, and many users would rather keep it.
	Mouse bool `toml:"mouse"`
	// Keys overrides the default key bindings, action name to key.
	Keys map[string]string `toml:"keys"`
}

// MongoConfig is the MongoDB-specific part of config.toml.
type MongoConfig struct {
	// SampleSize is how many documents the card and completion sample for
	// fields. It is an observation, not a schema.
	SampleSize int `toml:"sample_size"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		ResultLimitRows:  DefaultResultLimitRows,
		ResultLimitBytes: DefaultResultLimitBytes,
		MongoDB:          MongoConfig{SampleSize: DefaultMongoSampleSize},
	}
}

// Load reads path, applying defaults for anything absent. A missing file is not
// an error: it means "all defaults".
func Load(path string) (Config, error) {
	cfg := Default()

	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate rejects values that would make the UI misbehave.
func (c Config) Validate() error {
	if c.ResultLimitRows <= 0 {
		return fmt.Errorf("result_limit_rows must be positive, got %d", c.ResultLimitRows)
	}
	if c.ResultLimitBytes <= 0 {
		return fmt.Errorf("result_limit_bytes must be positive, got %d", c.ResultLimitBytes)
	}
	if c.MongoDB.SampleSize <= 0 {
		return fmt.Errorf("mongodb.sample_size must be positive, got %d", c.MongoDB.SampleSize)
	}
	if c.MongoDB.SampleSize > 1000 {
		return fmt.Errorf("mongodb.sample_size is too large (%d), keep it under 1000", c.MongoDB.SampleSize)
	}
	if _, err := c.Keymap(); err != nil {
		return err
	}
	return nil
}
