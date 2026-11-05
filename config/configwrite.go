package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RenderedConfigTemplateEnv / RenderedConfigPathEnv are the HA container
// convention (see docker-entrypoint.sh): when both are set, the
// entrypoint re-renders the config from a template plus environment
// variables on every container start.
const (
	RenderedConfigTemplateEnv = "CONFIG_TEMPLATE"
	RenderedConfigPathEnv     = "RENDERED_CONFIG"
)

// IsRenderedDeployment reports whether this process runs with an
// entrypoint-rendered config file. Such a file is one-shot: it is
// regenerated from template + environment on every container start, so
// writing credentials back into it would be lost on restart and must not
// be reported as a success. In that deployment form the credentials must
// be changed on the template / environment side instead.
func IsRenderedDeployment() bool {
	return os.Getenv(RenderedConfigTemplateEnv) != "" && os.Getenv(RenderedConfigPathEnv) != ""
}

// RewriteClientCredentials atomically replaces every
// (client_id, client_secret) pair equal to (oldID, oldSecret) inside the
// given YAML sections (each a key path from the document root, e.g.
// ["core_api"]) with (newID, newSecret). It returns how many sections
// were rewritten.
//
// Safety properties:
//
//   - The pre-change file is preserved as path + ".bak".
//   - The new content is written to a temporary file in the same
//     directory, validated by re-parsing it, tightened to 0600 (never
//     world-readable), then renamed over the original — the rename is
//     atomic on POSIX, so readers never observe a half-written config.
//   - Comments and key order are preserved (yaml.Node surgery, not a
//     full re-marshal of parsed structs).
//   - Sections whose pair differs from (oldID, oldSecret) are untouched.
//
// A returned error means the config file was NOT modified; callers
// should log and continue running with their in-memory credentials.
func RewriteClientCredentials(path, oldID, oldSecret, newID, newSecret string, sections ...[]string) (int, error) {
	if oldID == "" || oldSecret == "" || newID == "" || newSecret == "" {
		return 0, errors.New("refusing to rewrite credentials with empty values")
	}

	orig, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read current config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(orig, &root); err != nil {
		return 0, fmt.Errorf("parse current config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return 0, errors.New("config root is not a YAML mapping")
	}
	top := root.Content[0]

	replaced := 0
	for _, keyPath := range sections {
		sec := top
		for _, key := range keyPath {
			if sec == nil || sec.Kind != yaml.MappingNode {
				sec = nil
				break
			}
			sec = mappingValue(sec, key)
		}
		if sec != nil && sec.Kind == yaml.MappingNode &&
			replaceCredentialPair(sec, oldID, oldSecret, newID, newSecret) {
			replaced++
		}
	}
	if replaced == 0 {
		// Nothing carried the old pair: leave the file alone.
		return 0, nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(&root); err != nil {
		return 0, fmt.Errorf("encode rewritten config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("encode rewritten config: %w", err)
	}
	out := buf.Bytes()

	// Validate before touching any file: the rewritten bytes must parse
	// back into a config map, otherwise a broken file could keep the
	// service from starting next time.
	var check map[string]interface{}
	if err := yaml.Unmarshal(out, &check); err != nil {
		return 0, fmt.Errorf("rewritten config failed validation: %w", err)
	}

	// Backup the current file (kept at 0600 as well — it contains the
	// old super secret).
	if err := os.WriteFile(path+".bak", orig, 0600); err != nil {
		return 0, fmt.Errorf("write backup %s.bak: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("tighten temp file permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return 0, fmt.Errorf("replace config file: %w", err)
	}
	return replaced, nil
}

// mappingValue returns the value node of key in a mapping node, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// replaceCredentialPair swaps client_id/client_secret inside a section
// mapping when — and only when — the current pair equals (oldID,
// oldSecret). Returns whether the section was changed.
func replaceCredentialPair(section *yaml.Node, oldID, oldSecret, newID, newSecret string) bool {
	idNode := mappingValue(section, "client_id")
	secretNode := mappingValue(section, "client_secret")
	if idNode == nil || secretNode == nil {
		return false
	}
	if idNode.Value != oldID || secretNode.Value != oldSecret {
		return false
	}
	idNode.Value = newID
	secretNode.Value = newSecret
	return true
}
