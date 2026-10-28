package config

import (
	"os"
	"path/filepath"
	"testing"
)

func tokenGen() string { return "test-manage-token" }

func TestVersionMajor(t *testing.T) {
	cases := map[string]int{
		"1.0": 1,
		"2":   2,
		"10":  10,
		"":    0,
		"abc": 0,
	}
	for in, want := range cases {
		if got := VersionMajor(in); got != want {
			t.Errorf("VersionMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMigrateConfigUpToDate(t *testing.T) {
	cfg := map[string]interface{}{"version": ConfigVersion}
	out, changed, err := MigrateConfig(cfg, tokenGen)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("expected no migration for up-to-date config")
	}
	if out["version"] != ConfigVersion {
		t.Fatalf("expected version %q, got %v", ConfigVersion, out["version"])
	}
}

func TestMigrateConfigFutureVersion(t *testing.T) {
	cfg := map[string]interface{}{
		"version": "8",
		"site":    map[string]interface{}{"name": "future"},
	}
	out, changed, err := MigrateConfig(cfg, tokenGen)
	if err != nil {
		t.Fatalf("future version should be left untouched, got error: %v", err)
	}
	if changed {
		t.Fatal("expected no migration for newer config version")
	}
	if out["version"] != "8" {
		t.Fatalf("expected version to remain 8, got %v", out["version"])
	}
}

func TestMigrateConfigMissingVersion(t *testing.T) {
	_, _, err := MigrateConfig(map[string]interface{}{}, tokenGen)
	if err == nil {
		t.Fatal("expected error for config missing version")
	}
}

func TestMigrateConfigUnsupportedOldVersion(t *testing.T) {
	_, _, err := MigrateConfig(map[string]interface{}{"version": "0"}, tokenGen)
	if err == nil {
		t.Fatal("expected error when no migration path exists for old version")
	}
}

func TestBackupConfigFileCreatesCopyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := []byte("version: \"1\"\nsite:\n  name: test\n")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatalf("failed to write original config: %v", err)
	}

	if err := BackupConfigFile(path, "1"); err != nil {
		t.Fatalf("BackupConfigFile failed: %v", err)
	}

	backupPath := path + ".bak.1"
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed to read backup: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("expected backup contents to match original, got %q", string(data))
	}

	updated := []byte("version: \"1\"\nsite:\n  name: updated\n")
	if err := os.WriteFile(path, updated, 0644); err != nil {
		t.Fatalf("failed to rewrite original config: %v", err)
	}
	if err := BackupConfigFile(path, "1"); err != nil {
		t.Fatalf("second BackupConfigFile failed: %v", err)
	}

	data, err = os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed to re-read backup: %v", err)
	}
	if string(data) != string(original) {
		t.Fatalf("expected existing backup to stay untouched, got %q", string(data))
	}
}
