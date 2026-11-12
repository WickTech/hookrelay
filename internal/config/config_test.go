package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckAdminExposure(t *testing.T) {
	cases := []struct {
		host, token string
		wantErr     bool
	}{
		{"", "", true},             // all interfaces, no token
		{"0.0.0.0", "", true},      // explicit all interfaces
		{"::", "", true},           // IPv6 all interfaces
		{"10.0.0.5", "", true},     // private but not loopback
		{"example.com", "", true},  // hostname other than localhost
		{"   ", "", true},          // blank host
		{"", "   ", true},          // whitespace token counts as empty
		{"127.0.0.1", "", false},   // loopback v4
		{"127.0.0.2", "", false},   // whole 127/8 is loopback
		{"::1", "", false},         // loopback v6
		{"[::1]", "", false},       // bracketed v6
		{"localhost", "", false},   // name
		{"LocalHost", "", false},   // case-insensitive
		{"", "s3cret", false},      // token set: any host
		{"0.0.0.0", "s3cret", false},
	}
	for _, c := range cases {
		err := CheckAdminExposure(c.host, c.token)
		if (err != nil) != c.wantErr {
			t.Errorf("CheckAdminExposure(%q, %q) = %v, wantErr %v", c.host, c.token, err, c.wantErr)
		}
		if err != nil && !errors.Is(err, ErrAdminTokenRequired) {
			t.Errorf("CheckAdminExposure(%q, %q) error %v is not ErrAdminTokenRequired", c.host, c.token, err)
		}
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "targets.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_RefusesExposedAdminWithoutToken(t *testing.T) {
	t.Setenv(EnvAdminToken, "")
	t.Setenv(EnvHost, "")
	_, err := Load(writeConfig(t, "port: 8080\n"))
	if !errors.Is(err, ErrAdminTokenRequired) {
		t.Fatalf("expected ErrAdminTokenRequired, got %v", err)
	}
}

func TestLoad_LoopbackWithoutToken(t *testing.T) {
	t.Setenv(EnvAdminToken, "")
	t.Setenv(EnvHost, "")
	cfg, err := Load(writeConfig(t, "host: 127.0.0.1\nport: 9000\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.ListenAddr(); got != "127.0.0.1:9000" {
		t.Errorf("ListenAddr = %q, want 127.0.0.1:9000", got)
	}
}

func TestLoad_TokenFromFile(t *testing.T) {
	t.Setenv(EnvAdminToken, "")
	t.Setenv(EnvHost, "")
	cfg, err := Load(writeConfig(t, "admin:\n  token: file-token\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.Token != "file-token" {
		t.Errorf("Admin.Token = %q, want file-token", cfg.Admin.Token)
	}
	if got := cfg.ListenAddr(); got != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", got)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv(EnvAdminToken, "env-token")
	t.Setenv(EnvHost, "0.0.0.0")
	t.Setenv(EnvDBPath, "/data/x.db")
	cfg, err := Load(writeConfig(t, "host: 127.0.0.1\nadmin:\n  token: file-token\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Admin.Token != "env-token" {
		t.Errorf("Admin.Token = %q, want env-token", cfg.Admin.Token)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.DBPath != "/data/x.db" {
		t.Errorf("DBPath = %q, want /data/x.db", cfg.DBPath)
	}
}
