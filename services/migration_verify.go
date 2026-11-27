package services

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

type MigrationVerifier interface {
	ApplyAsset(profileID, textureType, model string, png []byte) (string, error)
	HasStoredAsset(hash string) (bool, error)
	StoredAsset(hash string) ([]byte, error)
	ProfileProperties(profileID, profileName string) ([]models.ProfileProperty, error)
	CallbackURL() string
}

type textureMigrationVerifier struct {
	textures *TextureService
}

func NewTextureMigrationVerifier(textures *TextureService) MigrationVerifier {
	return &textureMigrationVerifier{textures: textures}
}

func (v *textureMigrationVerifier) ApplyAsset(profileID, textureType, model string, png []byte) (string, error) {
	hash, _, err := v.textures.applyTexture(profileID, textureType, model, png)
	return hash, err
}

func (v *textureMigrationVerifier) HasStoredAsset(hash string) (bool, error) {
	path, err := v.textures.GetTexturePath(hash)
	if err != nil {
		return false, nil
	}
	return path != "", nil
}

func (v *textureMigrationVerifier) StoredAsset(hash string) ([]byte, error) {
	data, _, err := v.textures.GetTextureByHash(hash)
	return data, err
}

func (v *textureMigrationVerifier) ProfileProperties(profileID, profileName string) ([]models.ProfileProperty, error) {
	return v.textures.GetProfileProperties(profileID, profileName, true)
}

func (v *textureMigrationVerifier) CallbackURL() string {
	return v.textures.GetEffectiveCallbackURL()
}

type verifiedMigrationAssets struct {
	Results    MigrationAssetResults
	Properties []MigrationUserProperty
}

func verifyMigrationAssets(request MigrationCommitRequest, verifier MigrationVerifier, apply bool) (verifiedMigrationAssets, error) {
	verified := verifiedMigrationAssets{
		Results: MigrationAssetResults{
			Skin: MigrationAssetResult{Status: "absent"},
			Cape: MigrationAssetResult{Status: "absent"},
		},
	}
	hashes := make(map[string]string, 2)
	for _, asset := range []struct {
		kind string
		data *MigrationAsset
	}{
		{kind: "skin", data: request.Skin},
		{kind: "cape", data: request.Cape},
	} {
		if asset.data == nil {
			continue
		}
		existed, err := verifier.HasStoredAsset(asset.data.PNGSHA256)
		if err != nil {
			return verifiedMigrationAssets{}, migrationError("migration_commit_failed", "asset_apply", err)
		}
		hash := asset.data.PNGSHA256
		if apply {
			hash, err = verifier.ApplyAsset(request.Metadata.ProfileUUID, asset.kind, request.Metadata.ProfileModel, asset.data.PNG)
			if err != nil {
				return verifiedMigrationAssets{}, migrationError("migration_commit_failed", "asset_apply", err)
			}
			if hash != asset.data.PNGSHA256 {
				return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "asset_apply", errors.New("texture pipeline changed normalized PNG"))
			}
		}
		stored, err := verifier.StoredAsset(hash)
		if err != nil {
			return verifiedMigrationAssets{}, migrationError("migration_commit_failed", "verify", err)
		}
		normalized, err := normalizeMigrationAsset(asset.kind, stored)
		if err != nil {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", err)
		}
		if normalized.PixelSHA256 != asset.data.PixelSHA256 || normalized.PNGSHA256 != asset.data.PNGSHA256 {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("stored texture digest mismatch"))
		}
		status := "uploaded"
		if existed {
			status = "unchanged"
		}
		result := MigrationAssetResult{Status: status, SHA256: asset.data.PixelSHA256}
		if asset.kind == "skin" {
			verified.Results.Skin = result
		} else {
			verified.Results.Cape = result
		}
		hashes[strings.ToUpper(asset.kind)] = hash
	}

	properties, err := verifier.ProfileProperties(request.Metadata.ProfileUUID, request.Metadata.ProfileName)
	if err != nil {
		return verifiedMigrationAssets{}, migrationError("migration_commit_failed", "verify", err)
	}
	textureProperties := 0
	propertyValues := make(map[string]string, len(properties))
	for _, property := range properties {
		if _, exists := propertyValues[property.Name]; exists {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("duplicate authoritative property"))
		}
		propertyValues[property.Name] = property.Value
		if property.Name != "textures" {
			continue
		}
		textureProperties++
		payload, err := decodeMigrationTextures(property.Value)
		if err != nil {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", err)
		}
		if payload.ProfileID != request.Metadata.ProfileUUID || payload.ProfileName != request.Metadata.ProfileName {
			return verifiedMigrationAssets{}, migrationError("migration_profile_conflict", "verify", errors.New("textures property identifies another profile"))
		}
		if len(payload.Textures) != len(hashes) {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("textures property asset set mismatch"))
		}
		for kind, hash := range hashes {
			info, exists := payload.Textures[kind]
			expectedURL := verifier.CallbackURL() + "/textures/" + hash
			if !exists || info.URL != expectedURL {
				return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", fmt.Errorf("%s texture URL mismatch", kind))
			}
			if kind == "SKIN" && info.Metadata["model"] != request.Metadata.ProfileModel {
				return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("skin model mismatch"))
			}
		}
	}
	if len(hashes) == 0 {
		if textureProperties != 0 {
			return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("unexpected textures property"))
		}
	} else if textureProperties != 1 {
		return verifiedMigrationAssets{}, migrationError("migration_asset_conflict", "verify", errors.New("missing authoritative textures property"))
	}

	expectedProperties := cloneMigrationProperties(request.Metadata.LegacyUserProperties)
	if expectedProperties == nil {
		expectedProperties = make(map[string]string)
	}
	if _, exists := expectedProperties["uploadableTextures"]; !exists {
		expectedProperties["uploadableTextures"] = "skin,cape"
	}
	delete(propertyValues, "textures")
	if len(propertyValues) != len(expectedProperties) {
		return verifiedMigrationAssets{}, migrationError("migration_profile_conflict", "verify", errors.New("authoritative property set mismatch"))
	}
	propertyNames := make([]string, 0, len(expectedProperties))
	for name := range expectedProperties {
		propertyNames = append(propertyNames, name)
	}
	slices.Sort(propertyNames)
	for _, name := range propertyNames {
		value, exists := propertyValues[name]
		if !exists || value != expectedProperties[name] {
			return verifiedMigrationAssets{}, migrationError("migration_profile_conflict", "verify", fmt.Errorf("authoritative property %q mismatch", name))
		}
		verified.Properties = append(verified.Properties, MigrationUserProperty{Name: name, Value: value})
	}
	return verified, nil
}

func decodeMigrationTextures(value string) (models.TexturePayload, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return models.TexturePayload{}, err
	}
	var payload models.TexturePayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return models.TexturePayload{}, err
	}
	return payload, nil
}
