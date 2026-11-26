package services

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func migrationResultFixture() MigrationCommitResult {
	profile := MigrationProfile{ID: "0123456789abcdef0123456789abcdef", Name: "Player", Model: "default"}
	return MigrationCommitResult{
		MigrationID:     "123e4567-e89b-12d3-a456-426614174000",
		Status:          "committed",
		CoreUID:         "7",
		AccountID:       11,
		ProfileID:       profile.ID,
		CreatedCoreUser: true,
		Session: MigrationSession{
			AccessToken:       "access-token",
			ClientToken:       "client-token",
			SelectedProfile:   profile,
			AvailableProfiles: []MigrationProfile{profile},
		},
		User: MigrationUser{
			ID: profile.ID,
			Properties: []MigrationUserProperty{
				{Name: "uploadableTextures", Value: "skin,cape"},
				{Name: "legacy_empty", Value: ""},
			},
		},
		Assets: MigrationAssetResults{
			Skin: MigrationAssetResult{Status: "uploaded", SHA256: strings.Repeat("a", 64)},
			Cape: MigrationAssetResult{Status: "absent"},
		},
		CommittedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestMigrationResultRoundTrip(t *testing.T) {
	encoded, err := EncodeMigrationResult(migrationResultFixture())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"password"`)) {
		t.Fatal("result unexpectedly contains password field")
	}
	decoded, err := DecodeMigrationResult(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.MigrationID != "123e4567-e89b-12d3-a456-426614174000" || decoded.User.ID != decoded.ProfileID || decoded.Session.AccessToken != "access-token" {
		t.Fatalf("unexpected result: %+v", decoded)
	}
}

func TestMigrationResultRejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MigrationCommitResult)
	}{
		{"wrong user ID", func(result *MigrationCommitResult) { result.User.ID = strings.Repeat("b", 32) }},
		{"wrong selected profile", func(result *MigrationCommitResult) { result.Session.SelectedProfile.ID = strings.Repeat("b", 32) }},
		{"both ownership flags", func(result *MigrationCommitResult) { result.ReusedCoreUser = true }},
		{"uncommitted", func(result *MigrationCommitResult) { result.Status = "applying" }},
		{"missing commit time", func(result *MigrationCommitResult) { result.CommittedAt = time.Time{} }},
		{"missing token", func(result *MigrationCommitResult) { result.Session.AccessToken = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := migrationResultFixture()
			test.mutate(&result)
			if _, err := EncodeMigrationResult(result); err == nil {
				t.Fatal("expected invalid result to be rejected")
			}
		})
	}
}

func TestDecodeMigrationResultRejectsUnknownAndTrailingJSON(t *testing.T) {
	encoded, err := EncodeMigrationResult(migrationResultFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{`"password":"secret"`, `{}`} {
		input := append(append([]byte{}, encoded[:len(encoded)-1]...), []byte(`,"unexpected":`+suffix+`}`)...)
		if _, err := DecodeMigrationResult(bytes.NewReader(input)); err == nil {
			t.Fatal("expected unknown field to be rejected")
		}
	}
	if _, err := DecodeMigrationResult(bytes.NewReader(append(encoded, []byte(` {}`)...))); err == nil {
		t.Fatal("expected trailing JSON value to be rejected")
	}
	if _, err := DecodeMigrationResult(bytes.NewReader([]byte(`null`))); err == nil {
		t.Fatal("expected null result to be rejected")
	}
}
