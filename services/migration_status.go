package services

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

type MigrationResource struct {
	Kind       string `json:"kind"`
	ResourceID string `json:"resource_id"`
	Ownership  string `json:"ownership"`
	Present    bool   `json:"present"`
}

type MigrationStatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Stage   string `json:"stage"`
}

type MigrationStatus struct {
	MigrationID string                 `json:"migration_id"`
	Status      string                 `json:"status"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Resources   []MigrationResource    `json:"resources"`
	Result      *MigrationCommitResult `json:"result,omitempty"`
	LastError   *MigrationStatusError  `json:"last_error,omitempty"`
}

func (c *MigrationCoordinator) Reconcile(ctx context.Context, migrationID string) (MigrationStatus, error) {
	release, err := c.acquireOperation(ctx, migrationID, "reconcile")
	if err != nil {
		return MigrationStatus{}, err
	}
	defer release()
	return c.reconcileLocked(ctx, migrationID)
}

func (c *MigrationCoordinator) reconcileLocked(ctx context.Context, migrationID string) (MigrationStatus, error) {
	var journal models.MigrationJournal
	if err := c.db.WithContext(ctx).Where("migration_id = ?", migrationID).First(&journal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return MigrationStatus{}, migrationError("migration_not_found", "reconcile", ErrMigrationNotFound)
		}
		return MigrationStatus{}, migrationError("migration_reconcile_failed", "reconcile", err)
	}

	packageData, packageErr := c.store.Load(ctx, migrationID)
	if packageErr != nil && !errors.Is(packageErr, ErrMigrationNotFound) {
		return MigrationStatus{}, migrationError("migration_reconcile_failed", "reconcile", packageErr)
	}
	packageAvailable := packageErr == nil
	if packageAvailable && (packageData.Metadata.MigrationID != migrationID || packageData.Digest != journal.Digest) {
		return MigrationStatus{}, migrationError("migration_reconcile_failed", "reconcile", errors.New("staged package does not match journal"))
	}

	originalStatus := journal.Status
	status, inconsistent, err := c.inspectMigrationResources(ctx, &journal, packageData, packageAvailable)
	if err != nil {
		return MigrationStatus{}, err
	}
	if originalStatus == "applying" || (inconsistent && originalStatus != "committed") {
		journal.Status = "failed"
	}
	if inconsistent || originalStatus == "applying" {
		journal.FailureCode = "migration_reconcile_failed"
	}
	previousFailureCode := journal.FailureCode
	if originalStatus == "committed" && !inconsistent {
		journal.FailureCode = ""
	}
	status.Status = journal.Status

	changed := originalStatus != journal.Status || inconsistent || previousFailureCode != journal.FailureCode
	if changed {
		now := time.Now().UTC()
		updates := map[string]interface{}{
			"status": journal.Status, "failure_code": journal.FailureCode, "updated_at": now,
		}
		if originalStatus == "applying" || (journal.Status == "failed" && (journal.Stage == "" || journal.Stage == "received" || journal.Stage == "staged_in_redis")) {
			journal.Stage = "reconcile"
			updates["stage"] = journal.Stage
		}
		if err := c.db.WithContext(ctx).Model(&models.MigrationJournal{}).Where("migration_id = ?", migrationID).Updates(updates).Error; err != nil {
			return MigrationStatus{}, migrationError("migration_reconcile_failed", "reconcile", err)
		}
		journal.UpdatedAt = now
	}
	if journal.UpdatedAt.IsZero() {
		journal.UpdatedAt = journal.CreatedAt
	}
	if journal.UpdatedAt.IsZero() {
		journal.UpdatedAt = time.Now().UTC()
	}
	status.UpdatedAt = journal.UpdatedAt
	if journal.Status == "failed" || inconsistent {
		code := journal.FailureCode
		if code == "" {
			code = "migration_reconcile_failed"
		}
		status.LastError = &MigrationStatusError{
			Code: code, Message: "Migration resources require reconciliation", Stage: "reconcile",
		}
	}
	if journal.Status == "committed" && !inconsistent {
		if result, err := c.results.Load(ctx, migrationID); err == nil && c.cachedResultMatchesJournal(result, &journal) {
			status.Result = &result
		}
	}
	return status, nil
}

func (c *MigrationCoordinator) acquireOperation(ctx context.Context, migrationID, action string) (func(), error) {
	if !ValidateMigrationID(migrationID) {
		return nil, migrationError("invalid_request", action, ErrInvalidMigrationRequest)
	}
	releaseLock, err := c.store.Acquire(ctx, migrationID)
	if err != nil {
		if action == "rollback" && errors.Is(err, ErrMigrationLocked) {
			return nil, migrationError("migration_invalid_state", action, err)
		}
		return nil, migrationError("migration_"+action+"_failed", action, err)
	}
	select {
	case c.commitMu <- struct{}{}:
		return func() {
			<-c.commitMu
			releaseLock()
		}, nil
	case <-ctx.Done():
		releaseLock()
		return nil, migrationError("migration_"+action+"_failed", action, ctx.Err())
	}
}

func (c *MigrationCoordinator) inspectMigrationResources(
	ctx context.Context,
	journal *models.MigrationJournal,
	packageData StagedMigrationPackage,
	packageAvailable bool,
) (MigrationStatus, bool, error) {
	status := MigrationStatus{
		MigrationID: journal.MigrationID,
		Status:      journal.Status,
		Resources:   make([]MigrationResource, 0, 5),
	}
	inconsistent := false
	if journal.CoreUUID != "" {
		coreStatus, err := c.core.InspectMigration(ctx, journal.MigrationID)
		if err != nil {
			return MigrationStatus{}, false, migrationError("migration_reconcile_failed", "reconcile", err)
		}
		present := coreStatus.User != nil && coreStatus.User.Present
		expectedOwnership := "preexisting"
		if journal.CoreCreated {
			expectedOwnership = "created_by_migration"
		}
		if coreStatus.Ownership != expectedOwnership {
			inconsistent = true
		}
		if coreStatus.User != nil {
			expectedEmail := ""
			if packageAvailable {
				expectedEmail = packageData.Metadata.Email
				if expectedEmail == "" {
					expectedEmail = "migration-" + journal.MigrationID + "@placeholder.invalid"
				}
			}
			if strconv.FormatUint(uint64(coreStatus.User.UID), 10) != journal.CoreUID ||
				!strings.EqualFold(coreStatus.User.UUID, journal.CoreUUID) || coreStatus.User.Username != journal.ProfileName ||
				(expectedEmail != "" && coreStatus.User.Email != expectedEmail) {
				inconsistent = true
			}
		} else {
			inconsistent = true
		}
		if journal.CoreCreated {
			if coreStatus.Status == "rolled_back" {
				if present {
					inconsistent = true
				}
			} else if coreStatus.Status != "created" || (!present && (journal.Status == "committed" || journal.Status == "applying" || journal.Status == "rolled_back")) {
				inconsistent = true
			}
		} else if coreStatus.Status != "not_created" || !present {
			inconsistent = true
		}
		status.Resources = append(status.Resources, MigrationResource{
			Kind: "core_user", ResourceID: journal.CoreUUID, Ownership: expectedOwnership, Present: present,
		})
	}

	if journal.AccountID != nil {
		var account models.Account
		present := false
		err := c.db.WithContext(ctx).Where("id = ?", *journal.AccountID).First(&account).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return MigrationStatus{}, false, migrationError("migration_reconcile_failed", "reconcile", err)
		}
		if err == nil {
			present = true
			if journal.Status == "rolled_back" || !migrationAccountUnmodified(account, journal.CoreUUID) {
				inconsistent = true
			}
		} else if journal.Status == "committed" || journal.Status == "applying" {
			inconsistent = true
		}
		status.Resources = append(status.Resources, MigrationResource{
			Kind: "game_account", ResourceID: strconv.FormatUint(uint64(*journal.AccountID), 10),
			Ownership: "created_by_migration", Present: present,
		})
	}

	var profile models.Profile
	profilePresent := false
	err := c.db.WithContext(ctx).Where("id = ?", journal.ProfileID).First(&profile).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return MigrationStatus{}, false, migrationError("migration_reconcile_failed", "reconcile", err)
	}
	if err == nil {
		profilePresent = true
		expectedModel := ""
		if packageAvailable {
			expectedModel = packageData.Metadata.ProfileModel
		}
		if journal.Status == "rolled_back" || profile.Name != journal.ProfileName ||
			(journal.AccountID != nil && uint(profile.AccountID) != *journal.AccountID) ||
			(expectedModel != "" && profile.Model != expectedModel) {
			inconsistent = true
		}
	} else if journal.Status == "committed" || journal.Status == "applying" {
		inconsistent = true
	}
	status.Resources = append(status.Resources, MigrationResource{
		Kind: "profile", ResourceID: journal.ProfileID, Ownership: "created_by_migration", Present: profilePresent,
	})

	if packageAvailable {
		for _, asset := range []struct {
			kind string
			data *StagedMigrationAsset
		}{
			{kind: "skin", data: packageData.Skin},
			{kind: "cape", data: packageData.Cape},
		} {
			if asset.data == nil {
				continue
			}
			hash := asset.data.PNGSHA256
			present, err := c.verifier.HasStoredAsset(hash)
			if err != nil {
				return MigrationStatus{}, false, migrationError("migration_reconcile_failed", "reconcile", err)
			}
			if !present {
				if journal.Status == "committed" || journal.Status == "applying" {
					inconsistent = true
				}
				if journal.Status == "rolled_back" {
					continue
				}
			}
			status.Resources = append(status.Resources, MigrationResource{
				Kind: asset.kind, ResourceID: hash, Ownership: "preexisting", Present: present,
			})
		}
	}
	return status, inconsistent, nil
}
