package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Target struct {
	ID          string `mapstructure:"id"`
	URL         string `mapstructure:"url"`
	Secret      string `mapstructure:"secret"`
	MaxAttempts int    `mapstructure:"max_attempts"`
	TimeoutMS   int    `mapstructure:"timeout_ms"`
}

type Config struct {
	Port    int      `mapstructure:"port"`
	DBPath  string   `mapstructure:"db_path"`
	Targets []Target `mapstructure:"targets"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetDefault("port", 8080)
	v.SetDefault("db_path", "hookrelay.db")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	for i := range cfg.Targets {
		if cfg.Targets[i].MaxAttempts == 0 {
			cfg.Targets[i].MaxAttempts = 10
		}
		if cfg.Targets[i].TimeoutMS == 0 {
			cfg.Targets[i].TimeoutMS = 5000
		}
	}

	return &cfg, nil
}

// TargetMap returns targets indexed by id for O(1) lookup.
func (c *Config) TargetMap() map[string]*Target {
	m := make(map[string]*Target, len(c.Targets))
	for i := range c.Targets {
		m[c.Targets[i].ID] = &c.Targets[i]
	}
	return m
}
