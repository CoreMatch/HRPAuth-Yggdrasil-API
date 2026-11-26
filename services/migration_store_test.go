package services

import (
	"bytes"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func stagedMigrationForTest(t *testing.T) MigrationCommitRequest {
	t.Helper()
	metadata := migrationTestMetadata()
	skin := migrationTestPNG(t, 64, 64, true)
	asset, err := normalizeMigrationAsset("skin", skin)
	if err != nil {
		t.Fatal(err)
	}
	metadata.SkinSHA256 = asset.PixelSHA256
	metadata.LegacyUserProperties = map[string]string{"legacy_rank": "member"}
	request, err := NormalizeMigrationCommit(metadata, skin, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestStagedMigrationPackageRoundTrip(t *testing.T) {
	request := stagedMigrationForTest(t)
	packageData, err := StageMigrationPackage(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Metadata.LegacyUserProperties["legacy_rank"] = "changed"
	request.Skin.PNG[0] = 0
	if packageData.Metadata.LegacyUserProperties["legacy_rank"] != "member" || packageData.Skin.PNG[0] == 0 {
		t.Fatal("staged package aliases request memory")
	}
	encoded, err := EncodeStagedMigration(packageData)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-password")) || bytes.Contains(encoded, []byte(`"password"`)) {
		t.Fatal("staged package contains plaintext password")
	}
	decoded, err := DecodeStagedMigration(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Digest != packageData.Digest || decoded.Metadata.ProfileUUID != packageData.Metadata.ProfileUUID || !bytes.Equal(decoded.Skin.PNG, packageData.Skin.PNG) {
		t.Fatalf("staged package changed during round trip: %+v", decoded)
	}
}

func TestDecodeStagedMigrationRejectsTampering(t *testing.T) {
	packageData, err := StageMigrationPackage(stagedMigrationForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeStagedMigration(packageData)
	if err != nil {
		t.Fatal(err)
	}
	packageData.Skin.PNG = append([]byte(nil), packageData.Skin.PNG...)
	packageData.Skin.PNG[len(packageData.Skin.PNG)-1] ^= 0xff
	tampered, err := EncodeStagedMigration(packageData)
	if err == nil || tampered != nil {
		t.Fatal("encoder accepted tampered staged asset")
	}
	for _, input := range []string{
		string(encoded) + ` {}`,
		strings.Replace(string(encoded), `"version":1`, `"version":1,"password":"secret-password"`, 1),
		`null`,
	} {
		if _, err := DecodeStagedMigration(strings.NewReader(input)); err == nil {
			t.Fatalf("expected staged decode failure for %q", input)
		}
	}
}

func TestStagedMigrationRejectsMetadataAssetMismatch(t *testing.T) {
	packageData, err := StageMigrationPackage(stagedMigrationForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	packageData.Metadata.SkinSHA256 = strings.Repeat("0", 64)
	digest, err := migrationCommitDigest(MigrationCommitRequest{
		Metadata: packageData.Metadata,
		Skin: &MigrationAsset{
			PNG: packageData.Skin.PNG, PixelSHA256: packageData.Skin.PixelSHA256,
			PNGSHA256: packageData.Skin.PNGSHA256, Width: packageData.Skin.Width, Height: packageData.Skin.Height,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	packageData.Digest = digest
	if err := ValidateStagedMigrationPackage(packageData); err == nil {
		t.Fatal("expected metadata and staged asset digest mismatch")
	}
}

func TestNewRedisMigrationStoreOptions(t *testing.T) {
	if _, err := NewRedisMigrationStore(nil, MigrationStoreOptions{}); err == nil {
		t.Fatal("nil Redis client accepted")
	}
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	store, err := NewRedisMigrationStore(client, MigrationStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if store.ttl != 7*24*time.Hour || store.lockTTL != 10*time.Minute || store.maxPackageBytes != 16<<20 {
		t.Fatalf("unexpected defaults: %+v", store)
	}
	for _, options := range []MigrationStoreOptions{
		{TTL: -1},
		{LockTTL: -1},
		{MaxPackageBytes: -1},
		{TTL: time.Microsecond},
		{LockTTL: time.Microsecond},
	} {
		if _, err := NewRedisMigrationStore(client, options); err == nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
}

func TestMigrationIDValidation(t *testing.T) {
	if !ValidateMigrationID("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("valid migration ID rejected")
	}
	for _, value := range []string{"", "../escape", "123e4567e89b12d3a456426614174000", "123E4567-E89B-12D3-A456-426614174000"} {
		if ValidateMigrationID(value) {
			t.Fatalf("invalid migration ID accepted: %q", value)
		}
	}
}
