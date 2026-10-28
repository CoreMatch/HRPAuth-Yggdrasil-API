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
//   - "2": remove static internal_key and add relay_path_prefix
//   - "3": add profile_cards priority settings
//   - "4": add the Game Profile card priority
func configMigrations() []ConfigMigration {
	return []ConfigMigration{
		{
			FromVersion: "1",
			ToVersion:   "2",
			Migrate: func(cfg map[string]interface{}, tokenGen func() string) error {
				// 1. Remove internal_key from core_api
				if coreAPI, ok := cfg["core_api"].(map[string]interface{}); ok {
					delete(coreAPI, "internal_key")
				}

				// 2. Add relay_path_prefix to microservice
				if ms, ok := cfg["microservice"].(map[string]interface{}); ok {
					if ms["relay_path_prefix"] == nil {
						ms["relay_path_prefix"] = "/yggdrasil-api"
					}
				}

				cfg["version"] = "2"
				return nil
			},
		},
		{
			FromVersion: "2",
			ToVersion:   "3",
			Migrate: func(cfg map[string]interface{}, tokenGen func() string) error {
				// Add profile_cards to microservice
				if ms, ok := cfg["microservice"].(map[string]interface{}); ok {
					if ms["profile_cards"] == nil {
						ms["profile_cards"] = map[string]interface{}{
							"Minecraft Assets": 200,
							"Mojang Binding":   100,
						}
					}
				}
				cfg["version"] = "3"
				return nil
			},
		},
		{
			FromVersion: "3",
			ToVersion:   "4",
			Migrate: func(cfg map[string]interface{}, tokenGen func() string) error {
				if ms, ok := cfg["microservice"].(map[string]interface{}); ok {
					priorities, ok := ms["profile_cards"].(map[string]interface{})
					if !ok {
						priorities = make(map[string]interface{})
						ms["profile_cards"] = priorities
					}
					if priorities["Game Profile"] == nil {
						priorities["Game Profile"] = 150
					}
				}
				cfg["version"] = "4"
				return nil
			},
		},
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
