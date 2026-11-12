package config

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/spf13/viper"
)

type Target struct {
	ID          string `mapstructure:"id"`
	URL         string `mapstructure:"url"`
	Secret      string `mapstructure:"secret"`
	MaxAttempts int    `mapstructure:"max_attempts"`
	TimeoutMS   int    `mapstructure:"timeout_ms"`
}

// Admin configures access to the admin API (/events, /dlq) and /metrics.
type Admin struct {
	// Token is the bearer token required on admin routes. It may be left
	// empty only when the server listens on a loopback address.
	Token string `mapstructure:"token"`
}

type Config struct {
	// Host is the interface to listen on. Empty means all interfaces.
	Host    string   `mapstructure:"host"`
	Port    int      `mapstructure:"port"`
	DBPath  string   `mapstructure:"db_path"`
	Admin   Admin    `mapstructure:"admin"`
	Targets []Target `mapstructure:"targets"`
}

// Environment variables that override the matching config file keys.
const (
	EnvHost       = "HOOKRELAY_HOST"
	EnvDBPath     = "HOOKRELAY_DB_PATH"
	EnvAdminToken = "HOOKRELAY_ADMIN_TOKEN"
)

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetDefault("host", "")
	v.SetDefault("port", 8080)
	v.SetDefault("db_path", "hookrelay.db")
	v.SetDefault("admin.token", "")

	for key, env := range map[string]string{
		"host":        EnvHost,
		"db_path":     EnvDBPath,
		"admin.token": EnvAdminToken,
	} {
		if err := v.BindEnv(key, env); err != nil {
			return nil, fmt.Errorf("bind env %s: %w", env, err)
		}
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.Admin.Token = strings.TrimSpace(cfg.Admin.Token)

	for i := range cfg.Targets {
		if cfg.Targets[i].MaxAttempts == 0 {
			cfg.Targets[i].MaxAttempts = 10
		}
		if cfg.Targets[i].TimeoutMS == 0 {
			cfg.Targets[i].TimeoutMS = 5000
		}
	}

	if err := CheckAdminExposure(cfg.Host, cfg.Admin.Token); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ListenAddr returns the host:port address the HTTP server binds to.
func (c *Config) ListenAddr() string {
	return net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
}

// ErrAdminTokenRequired is returned when the admin API would be reachable
// from the network without a token.
var ErrAdminTokenRequired = errors.New(
	"admin.token (or " + EnvAdminToken + ") is required unless host is a loopback address " +
		"(127.0.0.1, ::1 or localhost): /events, /dlq and /metrics would otherwise be unauthenticated")

// CheckAdminExposure refuses an empty admin token unless the server listens
// only on loopback.
func CheckAdminExposure(host, token string) error {
	if strings.TrimSpace(token) != "" || IsLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%w (host=%q)", ErrAdminTokenRequired, host)
}

// IsLoopbackHost reports whether host names only a loopback interface.
// An empty host means all interfaces and is not loopback.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TargetMap returns targets indexed by id for O(1) lookup.
func (c *Config) TargetMap() map[string]*Target {
	m := make(map[string]*Target, len(c.Targets))
	for i := range c.Targets {
		m[c.Targets[i].ID] = &c.Targets[i]
	}
	return m
}
