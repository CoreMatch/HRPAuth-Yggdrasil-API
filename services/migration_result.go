package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

var ErrMigrationResultNotFound = errors.New("migration result not found")

type MigrationCommitResult struct {
	MigrationID     string                `json:"migration_id"`
	Status          string                `json:"status"`
	CoreUID         string                `json:"core_uid"`
	AccountID       int64                 `json:"account_id"`
	ProfileID       string                `json:"profile_id"`
	CreatedCoreUser bool                  `json:"created_core_user"`
	ReusedCoreUser  bool                  `json:"reused_core_user"`
	Session         MigrationSession      `json:"session"`
	User            MigrationUser         `json:"user"`
	Assets          MigrationAssetResults `json:"assets"`
	CommittedAt     time.Time             `json:"committed_at"`
}

type MigrationSession struct {
	AccessToken       string             `json:"accessToken"`
	ClientToken       string             `json:"clientToken"`
	SelectedProfile   MigrationProfile   `json:"selectedProfile"`
	AvailableProfiles []MigrationProfile `json:"availableProfiles"`
}

type MigrationProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

type MigrationUser struct {
	ID         string                  `json:"id"`
	Properties []MigrationUserProperty `json:"properties,omitempty"`
}

type MigrationUserProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type MigrationAssetResults struct {
	Skin MigrationAssetResult `json:"skin"`
	Cape MigrationAssetResult `json:"cape"`
}

type MigrationAssetResult struct {
	Status string `json:"status"`
	SHA256 string `json:"sha256,omitempty"`
}

type MigrationResultStore interface {
	Save(ctx context.Context, result MigrationCommitResult) error
	Load(ctx context.Context, migrationID string) (MigrationCommitResult, error)
	Delete(ctx context.Context, migrationID string) error
}

var migrationCoreUIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

func EncodeMigrationResult(result MigrationCommitResult) ([]byte, error) {
	if err := validateMigrationResult(result); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func DecodeMigrationResult(reader io.Reader) (MigrationCommitResult, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var result *MigrationCommitResult
	if err := decoder.Decode(&result); err != nil || result == nil {
		if err == nil {
			err = errors.New("migration result must be an object")
		}
		return MigrationCommitResult{}, fmt.Errorf("%w: %v", ErrMigrationResultNotFound, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return MigrationCommitResult{}, fmt.Errorf("%w: multiple JSON values", ErrMigrationResultNotFound)
	}
	if err := validateMigrationResult(*result); err != nil {
		return MigrationCommitResult{}, err
	}
	return *result, nil
}

func validateMigrationResult(result MigrationCommitResult) error {
	if !ValidateMigrationID(result.MigrationID) || result.Status != "committed" ||
		!migrationCoreUIDPattern.MatchString(result.CoreUID) || result.AccountID <= 0 ||
		!profileIDPattern.MatchString(result.ProfileID) || result.CreatedCoreUser == result.ReusedCoreUser ||
		result.CommittedAt.IsZero() || result.Session.AccessToken == "" || result.Session.ClientToken == "" ||
		result.Session.SelectedProfile.ID != result.ProfileID || result.Session.SelectedProfile.Name == "" ||
		result.User.ID != result.ProfileID || len(result.Session.AvailableProfiles) != 1 ||
		result.Session.AvailableProfiles[0].ID != result.ProfileID {
		return fmt.Errorf("%w: invalid committed result", ErrMigrationResultNotFound)
	}
	for _, asset := range []MigrationAssetResult{result.Assets.Skin, result.Assets.Cape} {
		switch asset.Status {
		case "absent":
			if asset.SHA256 != "" {
				return fmt.Errorf("%w: absent asset has digest", ErrMigrationResultNotFound)
			}
		case "uploaded", "unchanged":
			if _, err := canonicalMigrationSHA256(asset.SHA256); err != nil {
				return fmt.Errorf("%w: invalid asset digest", ErrMigrationResultNotFound)
			}
		default:
			return fmt.Errorf("%w: invalid asset status", ErrMigrationResultNotFound)
		}
	}
	for _, property := range result.User.Properties {
		if property.Name == "" {
			return fmt.Errorf("%w: invalid user property", ErrMigrationResultNotFound)
		}
	}
	if _, err := strconv.ParseUint(result.CoreUID, 10, 64); err != nil {
		return fmt.Errorf("%w: invalid Core UID", ErrMigrationResultNotFound)
	}
	return nil
}
