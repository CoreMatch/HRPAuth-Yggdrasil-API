package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// ConfigMigration describes one version step: it transforms a config map from
// FromVersion into ToVersion. Migrations run strictly in the order they are
// registered and must be idempotent for the version they target.
type ConfigMigration struct {
	FromVersion string
	ToVersion   string
	Migrate     func(cfg map[string]interface{}, tokenGen func() string) error
}

// configMigrations holds the version chain, ordered from oldest to newest.
//
// History:
//   - "1": initial decoupled Yggdrasil-API service config
func configMigrations() []ConfigMigration {
	return []ConfigMigration{
		// New service, no migrations yet
	}
}

// MigrateConfig upgrades cfg step by step until it reaches ConfigVersion.
// It returns the migrated config and whether at least one migration ran.
func MigrateConfig(cfg map[string]interface{}, tokenGen func() string) (map[string]interface{}, bool, error) {
	if tokenGen == nil {
		tokenGen = func() string { return "" }
	}

	current, _ := cfg["version"].(string)
	if current == "" {
		return nil, false, fmt.Errorf("config file is missing the version field; add version: %q or restore a backup", ConfigVersion)
	}

	if current == ConfigVersion {
		return cfg, false, nil
	}

	if VersionMajor(current) > VersionMajor(ConfigVersion) {
		log.Printf("Warning: config file version %q is newer than supported version %q, continuing without migration",
			current, ConfigVersion)
		return cfg, false, nil
	}

	changed := false
	steps := 0
	for current != ConfigVersion {
		step, found := findMigration(current)
		if !found {
			return nil, changed, fmt.Errorf(
				"no migration path from config version %q to %q; restore a backup or upgrade the program", current, ConfigVersion)
		}
		if err := step.Migrate(cfg, tokenGen); err != nil {
			return nil, changed, fmt.Errorf("migration %s->%s failed: %w", step.FromVersion, step.ToVersion, err)
		}
		next, _ := cfg["version"].(string)
		if next == "" || next == current {
			return nil, changed, fmt.Errorf(
				"migration %s->%s did not advance the version field (got %q)", step.FromVersion, step.ToVersion, next)
		}
		current = next
		changed = true
		steps++
		if steps > len(configMigrations())+1 {
			return nil, changed, fmt.Errorf("config migration did not converge")
		}
	}

	return cfg, changed, nil
}

// findMigration returns the registered migration starting from version.
func findMigration(from string) (*ConfigMigration, bool) {
	for i := range configMigrations() {
		if configMigrations()[i].FromVersion == from {
			m := configMigrations()[i]
			return &m, true
		}
	}
	return nil, false
}

// VersionMajor extracts the leading integer of a version string.
func VersionMajor(v string) int {
	v = strings.TrimSpace(v)
	i := 0
	for i < len(v) && v[i] >= '0' && v[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, err := strconv.Atoi(v[:i])
	if err != nil {
		return 0
	}
	return n
}

// BackupConfigFile copies path to path + ".bak." + version before migration.
func BackupConfigFile(path, version string) error {
	backupPath := fmt.Sprintf("%s.bak.%s", path, version)
	if _, err := os.Stat(backupPath); err == nil {
		log.Printf("Config backup %s already exists, keeping it", backupPath)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config backup %s: %w", backupPath, err)
	}
	log.Printf("Config backed up to %s", backupPath)
	return nil
}
