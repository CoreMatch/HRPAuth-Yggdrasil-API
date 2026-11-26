package services

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
	"gorm.io/gorm"
)

type MigrationCoordinator struct {
	core     *clients.CoreClient
	store    MigrationStore
	results  MigrationResultStore
	auth     *AuthService
	verifier MigrationVerifier
	db       *gorm.DB
	commitMu chan struct{}
}

func NewMigrationCoordinator(
	core *clients.CoreClient,
	store MigrationStore,
	results MigrationResultStore,
	auth *AuthService,
	verifier MigrationVerifier,
	db *gorm.DB,
) (*MigrationCoordinator, error) {
	if core == nil || store == nil || results == nil || auth == nil || verifier == nil || db == nil {
		return nil, errors.New("migration coordinator dependencies are required")
	}
	return &MigrationCoordinator{
		core: core, store: store, results: results, auth: auth, verifier: verifier, db: db,
		commitMu: make(chan struct{}, 1),
	}, nil
}

func (c *MigrationCoordinator) Commit(ctx context.Context, request MigrationCommitRequest) (result MigrationCommitResult, err error) {
	packageData, err := StageMigrationPackage(request)
	if err != nil {
		return MigrationCommitResult{}, err
	}
	release, err := c.store.Acquire(ctx, request.Metadata.MigrationID)
	if err != nil {
		if errors.Is(err, ErrMigrationLocked) {
			return MigrationCommitResult{}, migrationError("migration_conflict", "staged_in_redis", err)
		}
		return MigrationCommitResult{}, migrationError("migration_commit_failed", "staged_in_redis", err)
	}
	defer release()

	// The first release is single-instance; serialize target identity checks and
	// creation so different migration IDs cannot claim the same name concurrently.
	select {
	case c.commitMu <- struct{}{}:
		defer func() { <-c.commitMu }()
	case <-ctx.Done():
		return MigrationCommitResult{}, migrationError("migration_commit_failed", "staged_in_redis", ctx.Err())
	}

	if err := c.store.Stage(ctx, request.Metadata.MigrationID, packageData); err != nil {
		if errors.Is(err, ErrStagedConflict) {
			return MigrationCommitResult{}, migrationError("migration_conflict", "staged_in_redis", err)
		}
		return MigrationCommitResult{}, migrationError("migration_commit_failed", "staged_in_redis", err)
	}
	journal, err := c.ensureJournal(ctx, packageData)
	if err != nil {
		return MigrationCommitResult{}, err
	}
	freshToken := ""
	defer func() {
		if err == nil {
			return
		}
		if freshToken != "" {
			c.auth.InvalidateToken(freshToken)
		}
		c.recordFailure(journal, request, err)
	}()

	if journal.Digest != packageData.Digest {
		return MigrationCommitResult{}, migrationError("migration_conflict", "staged_in_redis", errors.New("migration ID was used with another digest"))
	}
	if journal.Status == "rolled_back" || (journal.CommittedAt != nil && journal.Status != "committed") {
		return MigrationCommitResult{}, migrationError("migration_conflict", "staged_in_redis", errors.New("migration is not retryable"))
	}
	if journal.Status == "committed" {
		result, freshToken, err = c.replayCommitted(ctx, journal, request)
		return result, err
	}
	if err := c.preflightMigrationTarget(ctx, journal, request); err != nil {
		return MigrationCommitResult{}, err
	}

	if err := c.updateApplying(journal, "core_provision"); err != nil {
		return MigrationCommitResult{}, err
	}
	claim, err := c.claimCoreUser(ctx, request)
	if err != nil {
		return MigrationCommitResult{}, err
	}
	journal.CoreUID = strconv.FormatUint(uint64(claim.UID), 10)
	journal.CoreUUID = claim.UUID
	journal.CoreCreated = journal.CoreCreated || claim.Created
	if err := c.db.WithContext(ctx).Model(&models.MigrationJournal{}).Where("migration_id = ?", journal.MigrationID).Updates(map[string]interface{}{
		"core_uid": journal.CoreUID, "core_uuid": journal.CoreUUID, "core_created": journal.CoreCreated,
	}).Error; err != nil {
		return MigrationCommitResult{}, migrationError("migration_commit_failed", "core_provision", err)
	}
	if err := c.updateApplying(journal, "profile_create"); err != nil {
		return MigrationCommitResult{}, err
	}
	if err := c.provisionLocalResources(ctx, journal, request); err != nil {
		return MigrationCommitResult{}, err
	}
	if err := c.updateApplying(journal, "asset_apply"); err != nil {
		return MigrationCommitResult{}, err
	}
	verified, err := verifyMigrationAssets(request, c.verifier, true)
	if err != nil {
		return MigrationCommitResult{}, err
	}
	if err := c.verifyCoreCredentials(ctx, journal, request); err != nil {
		return MigrationCommitResult{}, err
	}
	result, freshToken, err = c.issueMigrationSession(ctx, journal, request, verified)
	if err != nil {
		return MigrationCommitResult{}, err
	}
	committedAt := time.Now().UTC()
	result.CommittedAt = committedAt
	if err := c.markCommitted(ctx, journal, committedAt); err != nil {
		return MigrationCommitResult{}, err
	}
	if err := c.results.Save(ctx, result); err != nil {
		return MigrationCommitResult{}, migrationError("migration_commit_failed", "commit", err)
	}
	return result, nil
}

func (c *MigrationCoordinator) ensureJournal(ctx context.Context, packageData StagedMigrationPackage) (*models.MigrationJournal, error) {
	var journal models.MigrationJournal
	err := c.db.WithContext(ctx).Where("migration_id = ?", packageData.Metadata.MigrationID).First(&journal).Error
	if err == nil {
		if journal.Digest != packageData.Digest {
			return nil, migrationError("migration_conflict", "staged_in_redis", errors.New("migration ID was used with another digest"))
		}
		return &journal, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, migrationError("migration_commit_failed", "staged_in_redis", err)
	}
	journal = models.MigrationJournal{
		MigrationID: packageData.Metadata.MigrationID,
		Digest:      packageData.Digest,
		Status:      "staged_in_redis",
		Stage:       "staged_in_redis",
		ProfileID:   packageData.Metadata.ProfileUUID,
		ProfileName: packageData.Metadata.ProfileName,
	}
	if err := c.db.WithContext(ctx).Create(&journal).Error; err != nil {
		return nil, migrationError("migration_commit_failed", "staged_in_redis", err)
	}
	return &journal, nil
}

func (c *MigrationCoordinator) updateApplying(journal *models.MigrationJournal, stage string) error {
	if err := c.db.WithContext(context.Background()).Model(&models.MigrationJournal{}).Where("migration_id = ?", journal.MigrationID).Updates(map[string]interface{}{
		"status": "applying", "stage": stage, "failure_code": "", "rolled_back_at": nil,
	}).Error; err != nil {
		return migrationError("migration_commit_failed", stage, err)
	}
	journal.Status = "applying"
	journal.Stage = stage
	journal.FailureCode = ""
	journal.RolledBackAt = nil
	return nil
}

func (c *MigrationCoordinator) claimCoreUser(ctx context.Context, request MigrationCommitRequest) (*clients.MigrationUserClaim, error) {
	var email *string
	if request.Metadata.Email != "" {
		email = &request.Metadata.Email
	}
	claim, err := c.core.ClaimMigrationUser(ctx, request.Metadata.Username, request.Metadata.MigrationID, request.Password, email)
	if err != nil {
		switch {
		case errors.Is(err, clients.ErrMigrationConflict), errors.Is(err, clients.ErrMigrationInvalidState):
			return nil, migrationError("migration_conflict", "core_provision", err)
		case errors.Is(err, clients.ErrUsernameAlreadyBound), errors.Is(err, clients.ErrEmailAlreadyRegistered):
			return nil, migrationError("migration_credential_conflict", "core_provision", err)
		default:
			return nil, migrationError("migration_commit_failed", "core_provision", err)
		}
	}
	return claim, nil
}

func (c *MigrationCoordinator) verifyCoreCredentials(ctx context.Context, journal *models.MigrationJournal, request MigrationCommitRequest) error {
	if journal.AccountID == nil {
		return migrationError("migration_commit_failed", "verify", errors.New("migration journal has no account ownership"))
	}
	user, err := c.core.VerifyCredentialsContext(ctx, request.Metadata.Identifier, request.Password)
	if err != nil {
		return migrationError("migration_credential_conflict", "verify", err)
	}
	expectedEmail := request.Metadata.Email
	if expectedEmail == "" {
		expectedEmail = "migration-" + request.Metadata.MigrationID + "@placeholder.invalid"
	}
	if user.UID == 0 || strconv.FormatUint(uint64(user.UID), 10) != journal.CoreUID || user.UUID != journal.CoreUUID || user.Username != request.Metadata.Username || user.Email != expectedEmail {
		return migrationError("migration_credential_conflict", "verify", errors.New("Core identity does not match migration ownership"))
	}
	var account models.Account
	if err := c.db.WithContext(ctx).Where("id = ? AND core_user_id = ?", *journal.AccountID, journal.CoreUUID).First(&account).Error; err != nil {
		return migrationError("migration_credential_conflict", "verify", err)
	}
	return nil
}

func (c *MigrationCoordinator) issueMigrationSession(
	ctx context.Context,
	journal *models.MigrationJournal,
	request MigrationCommitRequest,
	verified verifiedMigrationAssets,
) (MigrationCommitResult, string, error) {
	if journal.AccountID == nil {
		return MigrationCommitResult{}, "", migrationError("migration_commit_failed", "verify", errors.New("migration journal has no account ownership"))
	}
	accessToken := utils.GenerateAccessToken()
	clientToken := utils.GenerateClientToken()
	expiresInDays := config.AppConfig.Yggdrasil.Security.TokenExpiryDays
	if expiresInDays <= 0 {
		expiresInDays = 15
	}
	if !c.auth.CreateToken(accessToken, clientToken, int(*journal.AccountID), request.Metadata.ProfileUUID, expiresInDays) {
		return MigrationCommitResult{}, "", migrationError("migration_commit_failed", "verify", errors.New("failed to issue migration session"))
	}
	token := c.auth.ValidateToken(accessToken, clientToken)
	if token == nil || token.AccountID != int(*journal.AccountID) || token.SelectedProfileID != request.Metadata.ProfileUUID {
		return MigrationCommitResult{}, accessToken, migrationError("migration_commit_failed", "verify", errors.New("fresh migration session did not verify"))
	}
	profile := MigrationProfile{ID: request.Metadata.ProfileUUID, Name: request.Metadata.ProfileName, Model: request.Metadata.ProfileModel}
	result := MigrationCommitResult{
		MigrationID:     journal.MigrationID,
		Status:          "committed",
		CoreUID:         journal.CoreUID,
		AccountID:       int64(*journal.AccountID),
		ProfileID:       request.Metadata.ProfileUUID,
		CreatedCoreUser: journal.CoreCreated,
		ReusedCoreUser:  !journal.CoreCreated,
		Session: MigrationSession{
			AccessToken: accessToken, ClientToken: clientToken,
			SelectedProfile: profile, AvailableProfiles: []MigrationProfile{profile},
		},
		User:        MigrationUser{ID: request.Metadata.ProfileUUID, Properties: verified.Properties},
		Assets:      verified.Results,
		CommittedAt: time.Now().UTC(),
	}
	return result, accessToken, nil
}

func (c *MigrationCoordinator) markCommitted(ctx context.Context, journal *models.MigrationJournal, committedAt time.Time) error {
	result := c.db.WithContext(ctx).Model(&models.MigrationJournal{}).Where("migration_id = ? AND status <> ?", journal.MigrationID, "committed").Updates(map[string]interface{}{
		"status": "committed", "stage": "commit", "failure_code": "", "committed_at": committedAt, "rolled_back_at": nil,
	})
	if result.Error != nil {
		return migrationError("migration_commit_failed", "commit", result.Error)
	}
	if result.RowsAffected == 1 {
		journal.Status = "committed"
		journal.Stage = "commit"
		journal.CommittedAt = &committedAt
		journal.RolledBackAt = nil
		journal.FailureCode = ""
		return nil
	}
	var current models.MigrationJournal
	if err := c.db.WithContext(ctx).Where("migration_id = ?", journal.MigrationID).First(&current).Error; err != nil {
		return migrationError("migration_commit_failed", "commit", err)
	}
	if current.Status != "committed" {
		return migrationError("migration_commit_failed", "commit", errors.New("migration journal changed before commit"))
	}
	journal.Status = "committed"
	journal.CommittedAt = current.CommittedAt
	return nil
}

func (c *MigrationCoordinator) replayCommitted(
	ctx context.Context,
	journal *models.MigrationJournal,
	request MigrationCommitRequest,
) (MigrationCommitResult, string, error) {
	cached, cacheErr := c.results.Load(ctx, journal.MigrationID)
	if cacheErr == nil && c.cachedResultMatchesJournal(cached, journal) {
		token := c.auth.ValidateToken(cached.Session.AccessToken, cached.Session.ClientToken)
		if token != nil && int64(token.AccountID) == cached.AccountID && token.SelectedProfileID == cached.ProfileID {
			return cached, "", nil
		}
	}
	verified, err := verifyMigrationAssets(request, c.verifier, false)
	if err != nil {
		return MigrationCommitResult{}, "", err
	}
	if err := c.verifyCoreCredentials(ctx, journal, request); err != nil {
		return MigrationCommitResult{}, "", err
	}
	result, freshToken, err := c.issueMigrationSession(ctx, journal, request, verified)
	if err != nil {
		return MigrationCommitResult{}, freshToken, err
	}
	if journal.CommittedAt != nil {
		result.CommittedAt = *journal.CommittedAt
	}
	if err := c.results.Save(ctx, result); err != nil {
		return MigrationCommitResult{}, freshToken, migrationError("migration_commit_failed", "commit", err)
	}
	return result, freshToken, nil
}

func (c *MigrationCoordinator) cachedResultMatchesJournal(result MigrationCommitResult, journal *models.MigrationJournal) bool {
	return journal.FailureCode == "" && result.MigrationID == journal.MigrationID && result.Status == "committed" &&
		result.CoreUID == journal.CoreUID && result.ProfileID == journal.ProfileID &&
		journal.AccountID != nil && result.AccountID == int64(*journal.AccountID) &&
		result.CreatedCoreUser == journal.CoreCreated && result.ReusedCoreUser != journal.CoreCreated
}

func (c *MigrationCoordinator) recordFailure(journal *models.MigrationJournal, request MigrationCommitRequest, commitErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var current models.MigrationJournal
	if err := c.db.WithContext(ctx).Where("migration_id = ?", journal.MigrationID).First(&current).Error; err != nil {
		log.Printf("[YGG-MIGRATION] migration_id=%s result=failed_journal_unavailable", journal.MigrationID)
		return
	}
	if current.Status == "committed" {
		return
	}
	var typed *MigrationError
	if !errors.As(commitErr, &typed) {
		typed = &MigrationError{Code: "migration_commit_failed", Stage: "commit", Err: commitErr}
	}
	var rolledBackAt *time.Time
	if err := c.compensateLocalResources(ctx, &current, request); err != nil {
		log.Printf("[YGG-MIGRATION] migration_id=%s stage=%s code=%s compensation=failed", current.MigrationID, typed.Stage, typed.Code)
	} else {
		now := time.Now().UTC()
		rolledBackAt = &now
	}
	if err := c.db.WithContext(ctx).Model(&models.MigrationJournal{}).Where("migration_id = ?", current.MigrationID).Updates(map[string]interface{}{
		"status": "failed", "stage": typed.Stage, "failure_code": typed.Code, "rolled_back_at": rolledBackAt,
	}).Error; err != nil {
		log.Printf("[YGG-MIGRATION] migration_id=%s result=failure_record_failed", current.MigrationID)
	}
}
