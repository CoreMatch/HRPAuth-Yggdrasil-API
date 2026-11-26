package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrMigrationNotFound = errors.New("migration package not found")
	ErrMigrationLocked   = errors.New("migration is already being processed")
	ErrStagedConflict    = errors.New("staged migration conflicts with existing package")
)

type StagedMigrationAsset struct {
	PNG         []byte `json:"png"`
	PixelSHA256 string `json:"pixel_sha256"`
	PNGSHA256   string `json:"png_sha256"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type StagedMigrationPackage struct {
	Version  int                     `json:"version"`
	Digest   string                  `json:"digest"`
	Metadata StagedMigrationMetadata `json:"metadata"`
	Skin     *StagedMigrationAsset   `json:"skin,omitempty"`
	Cape     *StagedMigrationAsset   `json:"cape,omitempty"`
}

type MigrationStore interface {
	Acquire(ctx context.Context, migrationID string) (func(), error)
	Stage(ctx context.Context, migrationID string, migration StagedMigrationPackage) error
	Load(ctx context.Context, migrationID string) (StagedMigrationPackage, error)
	Delete(ctx context.Context, migrationID string) error
}

func StageMigrationPackage(request MigrationCommitRequest) (StagedMigrationPackage, error) {
	metadata := request.Metadata
	metadata.LegacyUserProperties = cloneMigrationProperties(metadata.LegacyUserProperties)
	packageData := StagedMigrationPackage{
		Version:  1,
		Digest:   request.Digest,
		Metadata: metadata,
	}
	if request.Skin != nil {
		packageData.Skin = &StagedMigrationAsset{
			PNG:         bytes.Clone(request.Skin.PNG),
			PixelSHA256: request.Skin.PixelSHA256,
			PNGSHA256:   request.Skin.PNGSHA256,
			Width:       request.Skin.Width,
			Height:      request.Skin.Height,
		}
	}
	if request.Cape != nil {
		packageData.Cape = &StagedMigrationAsset{
			PNG:         bytes.Clone(request.Cape.PNG),
			PixelSHA256: request.Cape.PixelSHA256,
			PNGSHA256:   request.Cape.PNGSHA256,
			Width:       request.Cape.Width,
			Height:      request.Cape.Height,
		}
	}
	if err := ValidateStagedMigrationPackage(packageData); err != nil {
		return StagedMigrationPackage{}, err
	}
	return packageData, nil
}

func ValidateStagedMigrationPackage(packageData StagedMigrationPackage) error {
	if packageData.Version != 1 || packageData.Metadata.MigrationID == "" {
		return migrationError("invalid_request", "staged_in_redis", errors.New("invalid staged package"))
	}
	metadata, err := normalizeMigrationMetadata(MigrationMetadata{
		MigrationID:          packageData.Metadata.MigrationID,
		Identifier:           packageData.Metadata.Identifier,
		Password:             "validation-only",
		Username:             packageData.Metadata.Username,
		Email:                packageData.Metadata.Email,
		LegacyUserID:         packageData.Metadata.LegacyUserID,
		LegacyUserProperties: packageData.Metadata.LegacyUserProperties,
		ProfileUUID:          packageData.Metadata.ProfileUUID,
		ProfileName:          packageData.Metadata.ProfileName,
		ProfileModel:         packageData.Metadata.ProfileModel,
		SkinSHA256:           packageData.Metadata.SkinSHA256,
		CapeSHA256:           packageData.Metadata.CapeSHA256,
		SkinSync:             packageData.Metadata.SkinSync,
	})
	if err != nil {
		return err
	}
	request := MigrationCommitRequest{Metadata: metadata}
	if packageData.Skin != nil {
		request.Skin, err = validateStagedMigrationAsset("skin", packageData.Skin)
		if err != nil {
			return err
		}
		if metadata.SkinSHA256 == "" || !strings.EqualFold(metadata.SkinSHA256, request.Skin.PixelSHA256) {
			return migrationError("migration_asset_conflict", "staged_in_redis", errors.New("staged skin digest mismatch"))
		}
	} else if metadata.SkinSHA256 != "" {
		return migrationError("invalid_request", "staged_in_redis", errors.New("staged package is missing skin"))
	}
	if packageData.Cape != nil {
		request.Cape, err = validateStagedMigrationAsset("cape", packageData.Cape)
		if err != nil {
			return err
		}
		if metadata.CapeSHA256 == "" || !strings.EqualFold(metadata.CapeSHA256, request.Cape.PixelSHA256) {
			return migrationError("migration_asset_conflict", "staged_in_redis", errors.New("staged cape digest mismatch"))
		}
	} else if metadata.CapeSHA256 != "" {
		return migrationError("invalid_request", "staged_in_redis", errors.New("staged package is missing cape"))
	}
	digest, err := migrationCommitDigest(request)
	if err != nil || digest != packageData.Digest {
		return migrationError("invalid_request", "staged_in_redis", errors.New("staged package digest mismatch"))
	}
	return nil
}

func validateStagedMigrationAsset(kind string, asset *StagedMigrationAsset) (*MigrationAsset, error) {
	if asset == nil {
		return nil, migrationError("invalid_request", "staged_in_redis", errors.New("missing staged asset"))
	}
	normalized, err := normalizeMigrationAsset(kind, asset.PNG)
	if err != nil {
		return nil, err
	}
	if normalized.Width != asset.Width || normalized.Height != asset.Height || normalized.PixelSHA256 != asset.PixelSHA256 || normalized.PNGSHA256 != asset.PNGSHA256 {
		return nil, migrationError("invalid_request", "staged_in_redis", fmt.Errorf("%s staged asset mismatch", kind))
	}
	if kind == "cape" && normalized.Width == 22 && normalized.Height == 17 {
		return nil, migrationError("invalid_texture_file", "staged_in_redis", errors.New("legacy cape dimensions are not normalized"))
	}
	return normalized, nil
}

func EncodeStagedMigration(packageData StagedMigrationPackage) ([]byte, error) {
	if err := ValidateStagedMigrationPackage(packageData); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(packageData)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(encoded, []byte(`"password"`)) {
		return nil, errors.New("staged package contains password field")
	}
	return encoded, nil
}

func DecodeStagedMigration(reader io.Reader) (StagedMigrationPackage, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var packageData StagedMigrationPackage
	if err := decoder.Decode(&packageData); err != nil {
		return StagedMigrationPackage{}, migrationError("invalid_request", "staged_in_redis", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return StagedMigrationPackage{}, migrationError("invalid_request", "staged_in_redis", errors.New("multiple staged package values"))
	}
	if err := ValidateStagedMigrationPackage(packageData); err != nil {
		return StagedMigrationPackage{}, err
	}
	return packageData, nil
}

func cloneMigrationProperties(properties map[string]string) map[string]string {
	if properties == nil {
		return nil
	}
	cloned := make(map[string]string, len(properties))
	for name, value := range properties {
		cloned[name] = value
	}
	return cloned
}

func ValidateMigrationID(migrationID string) bool {
	return migrationUUIDPattern.MatchString(migrationID)
}
