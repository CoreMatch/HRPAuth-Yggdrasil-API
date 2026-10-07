package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseServerConfigLoadsKeysAndDefaults(t *testing.T) {
	dir := t.TempDir()
	publicKeyPath := filepath.Join(dir, "public.pem")
	privateKeyPath := filepath.Join(dir, "private.pem")

	if err := os.WriteFile(publicKeyPath, []byte("PUBLIC"), 0644); err != nil {
		t.Fatalf("failed to write public key fixture: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, []byte("PRIVATE"), 0644); err != nil {
		t.Fatalf("failed to write private key fixture: %v", err)
	}

	parsed := parseServerConfig(map[string]interface{}{
		"server": map[string]interface{}{
			"name":                       "HRPAuth",
			"implementation":             "Ygg API",
			"version":                    "1.0.0",
			"signature_public_key_path":  publicKeyPath,
			"signature_private_key_path": privateKeyPath,
			"links": map[string]interface{}{
				"homepage": "https://example.com",
				"register": "https://example.com/register",
			},
			"skin_domains": []interface{}{"skins.example.com"},
		},
	})

	if parsed.TexturesStorage != "./" {
		t.Fatalf("expected default textures storage ./, got %q", parsed.TexturesStorage)
	}
	if parsed.SignaturePublicKey != "PUBLIC" {
		t.Fatalf("expected public key file to be loaded, got %q", parsed.SignaturePublicKey)
	}
	if parsed.SignaturePrivateKey != "PRIVATE" {
		t.Fatalf("expected private key file to be loaded, got %q", parsed.SignaturePrivateKey)
	}
	if len(parsed.SkinDomains) != 1 || parsed.SkinDomains[0] != "skins.example.com" {
		t.Fatalf("expected skin domains to be parsed, got %v", parsed.SkinDomains)
	}
	if parsed.Links.Homepage != "https://example.com" || parsed.Links.Register != "https://example.com/register" {
		t.Fatalf("expected links to be parsed, got %+v", parsed.Links)
	}
}

func TestParseYggdrasilConfigReadsSecurityAndFeatureFlags(t *testing.T) {
	parsed := parseYggdrasilConfig(map[string]interface{}{
		"yggdrasil": map[string]interface{}{
			"security": map[string]interface{}{
				"token_expiry_days":      15,
				"session_expiry_seconds": 28800,
				"max_texture_width":      1024,
				"max_texture_height":     512,
				"max_texture_file_size":  524288,
				"max_tokens_per_user":    7,
			},
			"feature_flags": map[string]interface{}{
				"non_email_login":             true,
				"legacy_skin_api":             true,
				"no_mojang_namespace":         false,
				"enable_mojang_anti_features": true,
				"enable_profile_key":          true,
				"username_check":              true,
				"enable_ip_check":             false,
			},
		},
	})

	if parsed.Security.MaxTokensPerUser != 7 {
		t.Fatalf("expected max_tokens_per_user 7, got %d", parsed.Security.MaxTokensPerUser)
	}
	if parsed.Security.MaxTextureFileSize != 524288 {
		t.Fatalf("expected max_texture_file_size 524288, got %d", parsed.Security.MaxTextureFileSize)
	}
	if !parsed.FeatureFlags.EnableProfileKey || !parsed.FeatureFlags.NonEmailLogin || !parsed.FeatureFlags.EnableMojangAntiFeatures {
		t.Fatalf("expected feature flags to be parsed, got %+v", parsed.FeatureFlags)
	}
}
