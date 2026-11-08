package services

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

// ErrMojangUUIDAlreadyBound is returned by ForceBindTransfer when the target
// account already carries a different mojang_uuid.
var ErrMojangUUIDAlreadyBound = errors.New("mojang_uuid already bound to target account")

// AdminService implements the game-side half of the claim and force-bind
// flows (HA-Contract hrpauth-yggdrasil-business.yaml: /admin/claim-user,
// /admin/force-bind). Core-side steps (user provisioning, credential
// verification) are done by the caller through clients.CoreClient before
// these methods run; everything here stays inside the local database so it
// is covered by a single transaction.
type AdminService struct{}

func NewAdminService() *AdminService {
	return &AdminService{}
}

// FindAccountByProfileName resolves a game profile name to its account.
// It returns (nil, nil, nil) when no profile with that name exists.
func (as *AdminService) FindAccountByProfileName(name string) (*models.Account, *models.Profile, error) {
	var profile models.Profile
	if err := database.DB.Where("name = ?", name).First(&profile).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var account models.Account
	if err := database.DB.First(&account, profile.AccountID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &account, &profile, nil
}

// ClaimProxyAccount links the provisioned core user to the proxy account and
// flips cbh to claimed. Restored pre-split semantics: the mojang_uuid stays
// untouched. A bound account additionally gets the best-effort official skin
// sync scheduled.
func (as *AdminService) ClaimProxyAccount(accountID int, coreUserID string) error {
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.Account{}).
			Where("id = ? AND cbh = ?", accountID, false).
			Updates(map[string]interface{}{
				"core_user_id": coreUserID,
				"cbh":          true,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("account %d is not claimable", accountID)
		}
		return nil
	})
	if err == nil {
		NewSkinSyncService().RequestSkinSync(accountID, "claim_user")
	}
	return err
}

// ForceBindTransfer moves mojangUUID from the source (proxy) account to the
// account of targetCoreUserID, creating that game account (cbh=1) when it
// does not exist yet, and deletes the source account. All writes happen in
// one local transaction; the source is deleted before the uuid is written so
// the unique mojang_uuid index never sees two live rows with the same value.
// It returns the target account's ID so the caller can schedule follow-ups
// (official skin sync).
func (as *AdminService) ForceBindTransfer(sourceAccountID int, targetCoreUserID, mojangUUID string) (int, error) {
	targetID := 0
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		var target models.Account
		err := tx.Where("core_user_id = ?", targetCoreUserID).First(&target).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			target = models.Account{
				CoreUserID: &targetCoreUserID,
				CBH:        true,
				RegisterAt: time.Now(),
			}
			if err := tx.Create(&target).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		if target.ID == sourceAccountID {
			return fmt.Errorf("source and target account are identical")
		}
		if target.MojangUUID != nil && *target.MojangUUID != "" && *target.MojangUUID != mojangUUID {
			return ErrMojangUUIDAlreadyBound
		}

		// Delete the proxy account first; profiles/tokens/sessions/profile
		// keys cascade via foreign keys.
		if err := tx.Delete(&models.Account{}, sourceAccountID).Error; err != nil {
			return err
		}

		if err := tx.Model(&models.Account{}).
			Where("id = ?", target.ID).
			Updates(map[string]interface{}{
				"mojang_uuid":  mojangUUID,
				"last_sign_at": time.Now(),
			}).Error; err != nil {
			return err
		}

		targetID = target.ID
		log.Printf("[YGG-ADMIN] action=force_bind source_account=%d target_core_user=%s mojang_uuid=%s result=success", sourceAccountID, targetCoreUserID, mojangUUID)
		return nil
	})
	if err == nil && targetID != 0 {
		NewSkinSyncService().RequestSkinSync(targetID, "force_bind")
	}
	return targetID, err
}
