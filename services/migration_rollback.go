package services

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

func (c *MigrationCoordinator) Rollback(ctx context.Context, migrationID string) (MigrationStatus, error) {
	release, err := c.acquireOperation(ctx, migrationID, "rollback")
	if err != nil {
		return MigrationStatus{}, err
	}
	defer release()

	var journal models.MigrationJournal
	if err := c.db.WithContext(ctx).Where("migration_id = ?", migrationID).First(&journal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return MigrationStatus{}, migrationError("migration_not_found", "rollback", ErrMigrationNotFound)
		}
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
	}
	if journal.Status == "committed" || journal.CommittedAt != nil {
		return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("committed migrations cannot be rolled back"))
	}
	if journal.Status == "rolled_back" {
		status, err := c.reconcileLocked(ctx, migrationID)
		if err != nil {
			return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
		}
		if status.Status != "rolled_back" {
			return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("rolled-back migration has residual or modified resources"))
		}
		if err := c.cleanupRolledBackPackage(ctx, migrationID); err != nil {
			return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
		}
		return status, nil
	}
	switch journal.Status {
	case "received", "staged_in_redis", "applying", "failed":
	default:
		return MigrationStatus{}, migrationError("migration_invalid_state", "rollback", errors.New("migration state cannot be rolled back"))
	}

	packageData, err := c.store.Load(ctx, migrationID)
	if err != nil {
		return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("staged package is required to verify resource ownership"))
	}
	if packageData.Metadata.MigrationID != migrationID || packageData.Digest != journal.Digest {
		return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("staged package does not match journal ownership"))
	}
	if err := c.verifyRollbackCoreOwnership(ctx, &journal, packageData); err != nil {
		return MigrationStatus{}, err
	}
	request := stagedMigrationCommitRequest(packageData)
	if err := c.compensateLocalResources(ctx, &journal, request); err != nil {
		var migrationErr *MigrationError
		if errors.Is(err, ErrMigrationOwnership) || (errors.As(err, &migrationErr) && migrationErr.Code == "migration_target_not_empty") {
			return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", err)
		}
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
	}
	if journal.CoreCreated {
		if _, err := c.core.RollbackMigration(ctx, migrationID); err != nil {
			if errors.Is(err, clients.ErrMigrationRollbackNotAllowed) || errors.Is(err, clients.ErrMigrationNotFound) ||
				errors.Is(err, clients.ErrMigrationInvalidState) || errors.Is(err, clients.ErrInvalidCoreResponse) {
				return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", err)
			}
			return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
		}
	}

	now := time.Now().UTC()
	result := c.db.WithContext(ctx).Model(&models.MigrationJournal{}).Where("migration_id = ? AND status <> ?", migrationID, "committed").Updates(map[string]interface{}{
		"status": "rolled_back", "stage": "rollback", "failure_code": "", "rolled_back_at": now,
	})
	if result.Error != nil {
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", result.Error)
	}
	if result.RowsAffected != 1 {
		var current models.MigrationJournal
		if err := c.db.WithContext(ctx).Where("migration_id = ?", migrationID).First(&current).Error; err != nil {
			return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
		}
		if current.Status == "committed" {
			return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("migration committed before rollback"))
		}
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", errors.New("migration journal changed during rollback"))
	}
	if err := c.cleanupRolledBackPackage(ctx, migrationID); err != nil {
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
	}
	status, err := c.reconcileLocked(ctx, migrationID)
	if err != nil {
		return MigrationStatus{}, migrationError("migration_rollback_failed", "rollback", err)
	}
	if status.Status != "rolled_back" {
		return MigrationStatus{}, migrationError("migration_rollback_not_allowed", "rollback", errors.New("rollback left residual or modified resources"))
	}
	return status, nil
}

func (c *MigrationCoordinator) verifyRollbackCoreOwnership(ctx context.Context, journal *models.MigrationJournal, packageData StagedMigrationPackage) error {
	if journal.CoreCreated && journal.CoreUUID == "" {
		return migrationError("migration_rollback_not_allowed", "rollback", errors.New("migration-created Core identity is missing"))
	}
	if journal.CoreUUID == "" {
		return nil
	}
	expectedEmail := packageData.Metadata.Email
	if expectedEmail == "" {
		expectedEmail = "migration-" + journal.MigrationID + "@placeholder.invalid"
	}
	if journal.CoreCreated {
		status, err := c.core.InspectMigration(ctx, journal.MigrationID)
		if err != nil {
			if errors.Is(err, clients.ErrMigrationNotFound) || errors.Is(err, clients.ErrInvalidCoreResponse) {
				return migrationError("migration_rollback_not_allowed", "rollback", err)
			}
			return migrationError("migration_rollback_failed", "rollback", err)
		}
		if status.Ownership != "created_by_migration" || status.User == nil ||
			strconv.FormatUint(uint64(status.User.UID), 10) != journal.CoreUID ||
			!strings.EqualFold(status.User.UUID, journal.CoreUUID) || status.User.Username != journal.ProfileName || status.User.Email != expectedEmail {
			return migrationError("migration_rollback_not_allowed", "rollback", errors.New("Core migration ownership does not match journal"))
		}
		if status.Status == "rolled_back" {
			if status.User.Present {
				return migrationError("migration_rollback_not_allowed", "rollback", errors.New("compensated Core user is still present"))
			}
			return nil
		}
		if status.Status != "created" {
			return migrationError("migration_rollback_not_allowed", "rollback", errors.New("Core migration ownership is not compensable"))
		}
		return nil
	}

	user, err := c.core.InspectMigrationUser(ctx, journal.CoreUUID)
	if err != nil {
		if errors.Is(err, clients.ErrCoreUserNotFound) || errors.Is(err, clients.ErrInvalidCoreResponse) {
			return migrationError("migration_rollback_not_allowed", "rollback", err)
		}
		return migrationError("migration_rollback_failed", "rollback", err)
	}
	if journal.CoreUID == "" || strconv.FormatUint(uint64(user.UID), 10) != journal.CoreUID ||
		!strings.EqualFold(user.UUID, journal.CoreUUID) || user.Username != journal.ProfileName || user.Email != expectedEmail {
		return migrationError("migration_rollback_not_allowed", "rollback", errors.New("Core user does not match preexisting migration ownership"))
	}
	return nil
}

func (c *MigrationCoordinator) cleanupRolledBackPackage(ctx context.Context, migrationID string) error {
	if err := c.results.Delete(ctx, migrationID); err != nil {
		return err
	}
	return c.store.Delete(ctx, migrationID)
}

func stagedMigrationCommitRequest(packageData StagedMigrationPackage) MigrationCommitRequest {
	request := MigrationCommitRequest{Metadata: packageData.Metadata, Digest: packageData.Digest}
	if packageData.Skin != nil {
		request.Skin = &MigrationAsset{
			PNG: packageData.Skin.PNG, PixelSHA256: packageData.Skin.PixelSHA256,
			PNGSHA256: packageData.Skin.PNGSHA256, Width: packageData.Skin.Width, Height: packageData.Skin.Height,
		}
	}
	if packageData.Cape != nil {
		request.Cape = &MigrationAsset{
			PNG: packageData.Cape.PNG, PixelSHA256: packageData.Cape.PixelSHA256,
			PNGSHA256: packageData.Cape.PNGSHA256, Width: packageData.Cape.Width, Height: packageData.Cape.Height,
		}
	}
	return request
}
