package services

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func migrationTestMetadata() MigrationMetadata {
	return MigrationMetadata{
		MigrationID:  "123e4567-e89b-12d3-a456-426614174000",
		Identifier:   "LegacyPlayer",
		Password:     "secret-password",
		Username:     "LegacyPlayer",
		ProfileUUID:  "0123456789abcdef0123456789abcdef",
		ProfileName:  "LegacyPlayer",
		ProfileModel: "default",
		SkinSync:     false,
	}
}

func migrationTestPNG(t *testing.T, width, height int, transparentRGB bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if transparentRGB {
		img.SetNRGBA(1, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 0})
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestNormalizeMigrationCommit(t *testing.T) {
	request, err := NormalizeMigrationCommit(migrationTestMetadata(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.Identifier != "LegacyPlayer" || request.Metadata.Email != "" || request.Metadata.SkinSync {
		t.Fatalf("unexpected metadata: %+v", request.Metadata)
	}
	if len(request.Digest) != 64 || request.Password != "secret-password" {
		t.Fatalf("unexpected request: %+v", request)
	}
	encoded, err := json.Marshal(request.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-password")) || bytes.Contains(encoded, []byte(`"password"`)) {
		t.Fatal("staged metadata contains password")
	}

	other := migrationTestMetadata()
	other.Password = "another-password"
	retry, err := NormalizeMigrationCommit(other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Digest != request.Digest {
		t.Fatal("password changed migration digest")
	}
	other.LegacyUserProperties = map[string]string{"z": "last", "a": "first"}
	withProperties, err := NormalizeMigrationCommit(other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if withProperties.Digest == request.Digest {
		t.Fatal("legacy properties did not change migration digest")
	}
	other.LegacyUserProperties["a"] = "changed"
	changed, err := NormalizeMigrationCommit(other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Digest == withProperties.Digest {
		t.Fatal("changed legacy property value did not change migration digest")
	}
}

func TestNormalizeMigrationEmailIdentifier(t *testing.T) {
	metadata := migrationTestMetadata()
	metadata.Identifier = "Player@Example.COM"
	metadata.Username = "Player"
	metadata.ProfileName = "Player"
	metadata.Email = "Player@Example.COM"
	request, err := NormalizeMigrationCommit(metadata, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.Identifier != "Player@example.com" || request.Metadata.Email != request.Metadata.Identifier {
		t.Fatalf("email was not normalized: %+v", request.Metadata)
	}
}

func TestNormalizeMigrationRejectsInvalidMetadata(t *testing.T) {
	tests := map[string]func(*MigrationMetadata){
		"missing password":      func(m *MigrationMetadata) { m.Password = "" },
		"short password":        func(m *MigrationMetadata) { m.Password = "12345" },
		"long password":         func(m *MigrationMetadata) { m.Password = strings.Repeat("x", 73) },
		"invalid migration ID":  func(m *MigrationMetadata) { m.MigrationID = "not-a-uuid" },
		"invalid profile ID":    func(m *MigrationMetadata) { m.ProfileUUID = "not-a-profile" },
		"username mismatch":     func(m *MigrationMetadata) { m.ProfileName = "AnotherPlayer" },
		"short name":            func(m *MigrationMetadata) { m.Username, m.ProfileName = "ab", "ab" },
		"invalid model":         func(m *MigrationMetadata) { m.ProfileModel = "classic" },
		"skin sync enabled":     func(m *MigrationMetadata) { m.SkinSync = true },
		"email for username":    func(m *MigrationMetadata) { m.Email = "player@example.com" },
		"reserved property":     func(m *MigrationMetadata) { m.LegacyUserProperties = map[string]string{"Textures": "forged"} },
		"invalid property name": func(m *MigrationMetadata) { m.LegacyUserProperties = map[string]string{" bad": "value"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			metadata := migrationTestMetadata()
			mutate(&metadata)
			if _, err := NormalizeMigrationCommit(metadata, nil, nil); err == nil {
				t.Fatal("expected invalid metadata to fail")
			}
		})
	}
}

func TestMigrationCanonicalPNG(t *testing.T) {
	metadata := migrationTestMetadata()
	first, err := normalizeMigrationAsset("skin", migrationTestPNG(t, 64, 64, true))
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeMigrationAsset("skin", migrationTestPNG(t, 64, 64, false))
	if err != nil {
		t.Fatal(err)
	}
	if first.PixelSHA256 != second.PixelSHA256 || first.PNGSHA256 != second.PNGSHA256 || !bytes.Equal(first.PNG, second.PNG) {
		t.Fatal("transparent RGB changed canonical PNG")
	}
	metadata.SkinSHA256 = strings.ToUpper(first.PixelSHA256)
	request, err := NormalizeMigrationCommit(metadata, migrationTestPNG(t, 64, 64, true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.Metadata.SkinSHA256 != first.PixelSHA256 {
		t.Fatal("asset digest was not canonicalized")
	}
	metadata.SkinSHA256 = strings.Repeat("0", 64)
	if _, err := NormalizeMigrationCommit(metadata, migrationTestPNG(t, 64, 64, true), nil); err == nil {
		t.Fatal("expected pixel digest mismatch")
	}
}

func TestMigrationRejectsLegacyCapeDimensions(t *testing.T) {
	metadata := migrationTestMetadata()
	cape := migrationTestPNG(t, 22, 17, false)
	asset, err := normalizeMigrationAsset("cape", cape)
	if err != nil {
		t.Fatal(err)
	}
	metadata.CapeSHA256 = asset.PixelSHA256
	if _, err := NormalizeMigrationCommit(metadata, nil, cape); err == nil {
		t.Fatal("expected silently resized legacy cape to fail")
	}
}

func TestDecodeMigrationMetadataStrict(t *testing.T) {
	valid := `{"migration_id":"123e4567-e89b-12d3-a456-426614174000","identifier":"Player","password":"secret-password","username":"Player","profile_uuid":"0123456789abcdef0123456789abcdef","profile_name":"Player","profile_model":"default","skin_sync":false}`
	if _, err := DecodeMigrationMetadata(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		strings.Replace(valid, `"password":"secret-password",`, "", 1) + `,"unknown":true`,
		valid + ` {}`,
		`null`,
	} {
		if _, err := DecodeMigrationMetadata(strings.NewReader(input)); err == nil {
			t.Fatalf("expected strict decode failure for %q", input)
		}
	}
}
