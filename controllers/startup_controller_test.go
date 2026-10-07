package controllers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"gopkg.in/yaml.v3"
)

func TestConfigFileNameConstant(t *testing.T) {
	if ConfigFileName != "config.yaml" {
		t.Fatalf("expected ConfigFileName to be config.yaml, got %q", ConfigFileName)
	}
}

func TestCheckAndMigrateConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	currentConfig := `
version: "1"
site:
  name: "Test"
server:
  port: ":2778"
yggdrasil:
  server:
    signature_public_key_path: "public_key.pem"
    signature_private_key_path: "private_key.pem"
  security:
    enable_captcha: true
    captcha_ttl: 60
`
	if err := os.WriteFile(path, []byte(currentConfig), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	sc := NewStartupController()
	if err := sc.checkAndMigrateConfig(path); err != nil {
		t.Fatalf("checkAndMigrateConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read migrated config: %v", err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse migrated config: %v", err)
	}

	if cfg["version"] != config.ConfigVersion {
		t.Fatalf("expected version %s after check, got %v", config.ConfigVersion, cfg["version"])
	}
	if _, err := os.Stat(path + ".bak.1"); !os.IsNotExist(err) {
		t.Errorf("expected no backup for up-to-date config, got err=%v", err)
	}
}

func TestBuildDefaultConfigIncludesDecoupledDefaults(t *testing.T) {
	sc := NewStartupController()
	cfg := sc.buildDefaultConfig("/tmp/public.pem", "/tmp/private.pem")

	if cfg["version"] != config.ConfigVersion {
		t.Fatalf("expected default config version %s, got %v", config.ConfigVersion, cfg["version"])
	}

	coreAPI, ok := cfg["core_api"].(map[string]interface{})
	if !ok {
		t.Fatal("core_api section missing from default config")
	}
	if key, _ := coreAPI["internal_key"].(string); len(key) != 64 {
		t.Fatalf("expected generated core_api internal_key, got %q", key)
	}

	yggdrasil, ok := cfg["yggdrasil"].(map[string]interface{})
	if !ok {
		t.Fatal("yggdrasil section missing from default config")
	}
	server, ok := yggdrasil["server"].(map[string]interface{})
	if !ok {
		t.Fatal("yggdrasil.server section missing from default config")
	}
	if server["signature_public_key_path"] != "/tmp/public.pem" {
		t.Fatalf("expected public key path to be preserved, got %v", server["signature_public_key_path"])
	}
	if server["signature_private_key_path"] != "/tmp/private.pem" {
		t.Fatalf("expected private key path to be preserved, got %v", server["signature_private_key_path"])
	}

	security, ok := yggdrasil["security"].(map[string]interface{})
	if !ok {
		t.Fatal("yggdrasil.security section missing from default config")
	}
	if security["max_tokens_per_user"] != 10 {
		t.Fatalf("expected default max_tokens_per_user 10, got %v", security["max_tokens_per_user"])
	}
}
