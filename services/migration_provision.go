package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

var ErrMigrationOwnership = errors.New("migration resource ownership is not verifiable")

func (c *MigrationCoordinator) preflightMigrationTarget(ctx context.Context, journal *models.MigrationJournal, request MigrationCommitRequest) error {
	db := c.db.WithContext(ctx)
	var profile models.Profile
	err := db.Where("id = ?", request.Metadata.ProfileUUID).First(&profile).Error
	if err == nil {
		if journal.AccountID == nil || uint(profile.AccountID) != *journal.AccountID {
			return migrationError("migration_profile_conflict", "profile_create", errors.New("profile ID already exists"))
		}
		return ensureProfileNameAvailableTx(db, request.Metadata.ProfileName, profile.ID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return migrationError("migration_commit_failed", "profile_create", err)
	}
	return ensureProfileNameAvailableTx(db, request.Metadata.ProfileName, "")
}

func (c *MigrationCoordinator) provisionLocalResources(ctx context.Context, journal *models.MigrationJournal, request MigrationCommitRequest) error {
	db := c.db.WithContext(ctx)
	var account models.Account
	accountErr := db.Where("core_user_id = ?", journal.CoreUUID).First(&account).Error
	if accountErr != nil && !errors.Is(accountErr, gorm.ErrRecordNotFound) {
		return migrationError("migration_commit_failed", "profile_create", accountErr)
	}
	if accountErr == nil {
		if journal.AccountID == nil || uint(account.ID) != *journal.AccountID || account.CoreUserID == nil || *account.CoreUserID != journal.CoreUUID || account.Password != "" || account.SkinSync {
			return migrationError("migration_target_not_empty", "profile_create", errors.New("Core identity already has a non-migration game account"))
		}
		return c.ensureOwnedProfile(ctx, journal, request)
	}

	if err := c.ensureProfileNameAvailable(ctx, request.Metadata.ProfileName, ""); err != nil {
		return err
	}
	var existingProfile models.Profile
	if err := db.Where("id = ?", request.Metadata.ProfileUUID).First(&existingProfile).Error; err == nil {
		return migrationError("migration_profile_conflict", "profile_create", errors.New("profile ID already exists"))
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return migrationError("migration_commit_failed", "profile_create", err)
	}

	createdAccount := models.Account{
		CoreUserID: &journal.CoreUUID,
		RegisterAt: time.Now(),
	}
	createdProfile := models.Profile{
		ID:        request.Metadata.ProfileUUID,
		Name:      request.Metadata.ProfileName,
		Model:     request.Metadata.ProfileModel,
		CreatedAt: time.Now(),
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var conflict models.Account
		if err := tx.Where("core_user_id = ?", journal.CoreUUID).First(&conflict).Error; err == nil {
			return migrationError("migration_target_not_empty", "profile_create", errors.New("game account appeared during provisioning"))
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := ensureProfileNameAvailableTx(tx, request.Metadata.ProfileName, ""); err != nil {
			return err
		}
		var conflictProfile models.Profile
		if err := tx.Where("id = ?", request.Metadata.ProfileUUID).First(&conflictProfile).Error; err == nil {
			return migrationError("migration_profile_conflict", "profile_create", errors.New("profile ID appeared during provisioning"))
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&createdAccount).Error; err != nil {
			return err
		}
		if err := tx.Model(&createdAccount).Update("skin_sync", false).Error; err != nil {
			return err
		}
		createdProfile.AccountID = createdAccount.ID
		if err := tx.Create(&createdProfile).Error; err != nil {
			return err
		}
		if err := insertMigrationProperties(tx, request); err != nil {
			return err
		}
		accountID := uint(createdAccount.ID)
		if err := tx.Model(&models.MigrationJournal{}).Where("migration_id = ?", journal.MigrationID).Updates(map[string]interface{}{
			"account_id": accountID,
			"profile_id": createdProfile.ID,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		var migrationErr *MigrationError
		if errors.As(err, &migrationErr) {
			return migrationErr
		}
		return migrationError("migration_commit_failed", "profile_create", err)
	}
	journal.AccountID = func() *uint { id := uint(createdAccount.ID); return &id }()
	journal.ProfileID = createdProfile.ID
	return nil
}

func (c *MigrationCoordinator) ensureOwnedProfile(ctx context.Context, journal *models.MigrationJournal, request MigrationCommitRequest) error {
	db := c.db.WithContext(ctx)
	var account models.Account
	if err := db.Where("id = ?", *journal.AccountID).First(&account).Error; err != nil {
		return migrationError("migration_target_not_empty", "profile_create", err)
	}
	var profile models.Profile
	profileErr := db.Where("id = ?", request.Metadata.ProfileUUID).First(&profile).Error
	if errors.Is(profileErr, gorm.ErrRecordNotFound) {
		if err := c.ensureProfileNameAvailable(ctx, request.Metadata.ProfileName, ""); err != nil {
			return err
		}
		profile = models.Profile{
			ID:        request.Metadata.ProfileUUID,
			AccountID: account.ID,
			Name:      request.Metadata.ProfileName,
			Model:     request.Metadata.ProfileModel,
			CreatedAt: time.Now(),
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := ensureProfileNameAvailableTx(tx, profile.Name, ""); err != nil {
				return err
			}
			var conflict models.Profile
			if err := tx.Where("id = ?", profile.ID).First(&conflict).Error; err == nil {
				return migrationError("migration_profile_conflict", "profile_create", errors.New("profile ID appeared during provisioning"))
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&profile).Error; err != nil {
				return err
			}
			return insertMigrationProperties(tx, request)
		})
		if err != nil {
			var migrationErr *MigrationError
			if errors.As(err, &migrationErr) {
				return migrationErr
			}
			return migrationError("migration_commit_failed", "profile_create", err)
		}
		journal.ProfileID = profile.ID
		return nil
	}
	if profileErr != nil {
		return migrationError("migration_commit_failed", "profile_create", profileErr)
	}
	if profile.AccountID != account.ID || profile.Name != request.Metadata.ProfileName || profile.Model != request.Metadata.ProfileModel {
		return migrationError("migration_target_not_empty", "profile_create", errors.New("journal-owned profile was modified"))
	}
	var profileCount int64
	if err := db.Model(&models.Profile{}).Where("account_id = ?", account.ID).Count(&profileCount).Error; err != nil {
		return migrationError("migration_commit_failed", "profile_create", err)
	}
	if profileCount != 1 {
		return migrationError("migration_target_not_empty", "profile_create", errors.New("migration account has additional profiles"))
	}
	if err := verifyOwnedMigrationProperties(db, request); err != nil {
		return err
	}
	return nil
}

func (c *MigrationCoordinator) ensureProfileNameAvailable(ctx context.Context, name, exceptID string) error {
	return ensureProfileNameAvailableTx(c.db.WithContext(ctx), name, exceptID)
}

func ensureProfileNameAvailableTx(db *gorm.DB, name, exceptID string) error {
	query := db.Model(&models.Profile{}).Where("LOWER(name) = LOWER(?)", name)
	if exceptID != "" {
		query = query.Where("id <> ?", exceptID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return migrationError("migration_commit_failed", "profile_create", err)
	}
	if count != 0 {
		return migrationError("migration_profile_conflict", "profile_create", errors.New("profile name already exists"))
	}
	return nil
}

func insertMigrationProperties(tx *gorm.DB, request MigrationCommitRequest) error {
	properties := request.Metadata.LegacyUserProperties
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		property := models.ProfileProperty{
			ProfileID:  request.Metadata.ProfileUUID,
			Name:       name,
			Value:      properties[name],
			DeleteWhen: 0,
		}
		if err := tx.Create(&property).Error; err != nil {
			return err
		}
	}
	return nil
}

func verifyOwnedMigrationProperties(db *gorm.DB, request MigrationCommitRequest) error {
	var properties []models.ProfileProperty
	if err := db.Where("profile_id = ?", request.Metadata.ProfileUUID).Find(&properties).Error; err != nil {
		return migrationError("migration_commit_failed", "profile_create", err)
	}
	expected := cloneMigrationProperties(request.Metadata.LegacyUserProperties)
	if expected == nil {
		expected = make(map[string]string)
	}
	textureProperties := 0
	for _, property := range properties {
		if property.Name == "textures" {
			textureProperties++
			if textureProperties > 1 {
				return migrationError("migration_target_not_empty", "profile_create", errors.New("journal-owned profile has duplicate textures properties"))
			}
			payload, err := decodeMigrationTextures(property.Value)
			if err != nil || payload.ProfileID != request.Metadata.ProfileUUID || payload.ProfileName != request.Metadata.ProfileName {
				return migrationError("migration_target_not_empty", "profile_create", errors.New("journal-owned profile textures identity was modified"))
			}
			continue
		}
		value, exists := expected[property.Name]
		if !exists || property.Value != value {
			return migrationError("migration_target_not_empty", "profile_create", fmt.Errorf("unexpected profile property %q", property.Name))
		}
		delete(expected, property.Name)
	}
	if len(expected) != 0 {
		return migrationError("migration_target_not_empty", "profile_create", errors.New("journal-owned profile is missing legacy properties"))
	}
	return nil
}

func migrationAccountUnmodified(account models.Account, coreUUID string) bool {
	// Successful authentication refreshes last_sign_at without changing migration ownership.
	return account.CoreUserID != nil && *account.CoreUserID == coreUUID && account.Password == "" &&
		!account.SkinSync && !account.MBE && account.CBH && account.MojangUUID == nil &&
		account.SkinSyncMark == nil
}

func (c *MigrationCoordinator) compensateLocalResources(ctx context.Context, journal *models.MigrationJournal, request MigrationCommitRequest) error {
	if journal.AccountID == nil {
		return nil
	}
	return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		accountID := *journal.AccountID
		var account models.Account
		accountExists := true
		if err := tx.Where("id = ?", accountID).First(&account).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			accountExists = false
		}
		if accountExists && !migrationAccountUnmodified(account, journal.CoreUUID) {
			return fmt.Errorf("%w: journal-owned account identity or migration settings changed", ErrMigrationOwnership)
		}
		var profile models.Profile
		profileExists := false
		if err := tx.Where("id = ? AND account_id = ?", journal.ProfileID, accountID).First(&profile).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else {
			profileExists = true
			if profile.Name != journal.ProfileName || profile.Model != request.Metadata.ProfileModel {
				return fmt.Errorf("%w: journal-owned profile was modified", ErrMigrationOwnership)
			}
		}
		if accountExists || profileExists {
			if err := verifyOwnedMigrationProperties(tx, request); err != nil {
				return err
			}
		}
		if accountExists {
			var profileCount int64
			if err := tx.Model(&models.Profile{}).Where("account_id = ?", accountID).Count(&profileCount).Error; err != nil {
				return err
			}
			if profileCount > 1 {
				return fmt.Errorf("%w: migration account has additional profiles", ErrMigrationOwnership)
			}
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&models.Token{}).Error; err != nil {
			return err
		}
		if journal.ProfileID != "" {
			if err := tx.Where("profile_id = ?", journal.ProfileID).Delete(&models.ProfileProperty{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ? AND account_id = ?", journal.ProfileID, accountID).Delete(&models.Profile{}).Error; err != nil {
				return err
			}
		}
		if accountExists {
			return tx.Where("id = ?", accountID).Delete(&models.Account{}).Error
		}
		return nil
	})
}
