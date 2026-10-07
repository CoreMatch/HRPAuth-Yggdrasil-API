package services

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/redis"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
	"gorm.io/gorm"
)

type AuthService struct {
	coreClient *clients.CoreClient
}

func NewAuthService() *AuthService {
	return &AuthService{
		coreClient: clients.NewCoreClient(),
	}
}

type UserInfo struct {
	UUID     string
	Email    string
	Username string
}

func (as *AuthService) VerifyCredentials(identifier, password string) (*UserInfo, error) {
	// 1. Try local proxy accounts first
	var account models.Account
	// Search by profile name first
	var profile models.Profile
	if err := database.DB.Where("name = ?", identifier).First(&profile).Error; err == nil {
		if err := database.DB.Where("id = ?", profile.AccountID).First(&account).Error; err == nil {
			if account.CBH == false && account.Password != "" {
				if utils.CheckPasswordHash(password, account.Password) {
					database.DB.Model(&account).Update("last_sign_at", time.Now())
					// Proxy account doesn't have email/UUID until claimed?
					// Use a generated internal UUID for proxy users
					proxyUUID := "proxy-" + strconv.Itoa(account.ID)
					if account.MojangUUID != nil {
						proxyUUID = *account.MojangUUID
					}
					return &UserInfo{
						UUID:     proxyUUID,
						Username: profile.Name,
					}, nil
				}
			}
		}
	}

	// 2. Try Core auth
	user, err := as.coreClient.VerifyCredentials(identifier, password)
	if err != nil {
		return nil, err
	}

	// Update last_sign_at if it's a core-linked account
	database.DB.Model(&models.Account{}).Where("core_user_id = ?", user.UUID).Update("last_sign_at", time.Now())

	return &UserInfo{
		UUID:     user.UUID,
		Email:    user.Email,
		Username: user.Username,
	}, nil
}

func (as *AuthService) RegisterGameAccount(identifier, password, mojangUUID string) (*models.Account, *models.Profile, error) {
	user, err := as.coreClient.VerifyCredentials(identifier, password)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid core credentials: %v", err)
	}

	var account models.Account
	err = database.DB.Where("core_user_id = ?", user.UUID).First(&account).Error
	if err == nil {
		return nil, nil, fmt.Errorf("game account already exists for this user")
	}

	// Validate and clean MojangUUID
	var mUUIDPtr *string
	if mojangUUID != "" {
		mojangUUID = utils.NormalizeMojangUUID(mojangUUID)
		if !utils.IsValidMojangUUID(mojangUUID) {
			return nil, nil, fmt.Errorf("invalid mojang_uuid format")
		}

		var existingAccount models.Account
		if err := database.DB.Where("mojang_uuid = ?", mojangUUID).First(&existingAccount).Error; err == nil {
			return nil, nil, fmt.Errorf("mojang_uuid already bound to another account")
		}
		mUUIDPtr = &mojangUUID
	}

	// Check if the username is already taken by another profile
	var existingProfile models.Profile
	if err := database.DB.Where("name = ?", user.Username).First(&existingProfile).Error; err == nil {
		return nil, nil, fmt.Errorf("profile name '%s' is already taken", user.Username)
	}

	// Create Account and default Profile in a transaction
	var profile models.Profile
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		account = models.Account{
			CoreUserID: user.UUID,
			MojangUUID: mUUIDPtr,
		}
		if err := tx.Create(&account).Error; err != nil {
			return err
		}

		profile = models.Profile{
			ID:        utils.GenerateUnsignedUUID(),
			AccountID: account.ID,
			Name:      user.Username, // Inherit username from Core service
			Model:     "default",
		}
		if err := tx.Create(&profile).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return &account, &profile, nil
}

func (as *AuthService) ProxyRegister(username, password, mojangUUID string) (*models.Account, *models.Profile, error) {
	mojangUUID = utils.NormalizeMojangUUID(mojangUUID)
	if !utils.IsValidMojangUUID(mojangUUID) {
		return nil, nil, fmt.Errorf("invalid mojang_uuid format")
	}

	var account models.Account
	err := database.DB.Where("mojang_uuid = ?", mojangUUID).First(&account).Error
	if err == nil {
		// Idempotent: return existing
		var profile models.Profile
		database.DB.Where("account_id = ?", account.ID).First(&profile)
		return &account, &profile, nil
	}

	// Check if username taken
	var existingProfile models.Profile
	if err := database.DB.Where("name = ?", username).First(&existingProfile).Error; err == nil {
		return nil, nil, fmt.Errorf("profile name '%s' already taken", username)
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		return nil, nil, err
	}

	var profile models.Profile
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		account = models.Account{
			MojangUUID: &mojangUUID,
			Password:   hash,
			CBH:        false, // Proxy registration
			RegisterAt: time.Now(),
		}
		if err := tx.Create(&account).Error; err != nil {
			return err
		}

		profile = models.Profile{
			ID:        utils.GenerateUnsignedUUID(),
			AccountID: account.ID,
			Name:      username,
			Model:     "default",
		}
		if err := tx.Create(&profile).Error; err != nil {
			return err
		}
		return nil
	})

	return &account, &profile, err
}

func (as *AuthService) ClaimAccount(mojangUUID, coreUserID string) error {
	mojangUUID = utils.NormalizeMojangUUID(mojangUUID)
	var account models.Account
	if err := database.DB.Where("mojang_uuid = ? AND cbh = 0", mojangUUID).First(&account).Error; err != nil {
		return fmt.Errorf("proxy account not found for claiming")
	}

	return database.DB.Model(&account).Updates(map[string]interface{}{
		"core_user_id": coreUserID,
		"cbh":          true, // Now claimed by human
	}).Error
}

func (as *AuthService) SyncUsername(coreUserID, newUsername string) error {
	var account models.Account
	if err := database.DB.Where("core_user_id = ?", coreUserID).First(&account).Error; err != nil {
		return nil // No game account to sync
	}

	// In 1:1 mapping, we assume the user has a default profile with the same name.
	// We sync all profiles owned by this account that match the old core username?
	// Actually, the requirement says "直接继承原主服务的 username", and sync is "自动同步".
	// We'll update all profiles for this account to the new username if they were matching the old one?
	// Or just update the "primary" profile. Let's update all for simplicity in 1:1.

	return database.DB.Model(&models.Profile{}).Where("account_id = ?", account.ID).Update("name", newUsername).Error
}

func (as *AuthService) DeleteAccount(coreUserID string) error {
	var account models.Account
	if err := database.DB.Where("core_user_id = ?", coreUserID).First(&account).Error; err != nil {
		return nil // Already gone
	}

	return database.DB.Transaction(func(tx *gorm.DB) error {
		// Cascade deletion is handled by DB constraints (ON DELETE CASCADE),
		// but we can also manually delete for safety if needed.
		// Profiles, Tokens, Sessions, ProfileKeys are all linked to Account.
		return tx.Delete(&account).Error
	})
}

// CleanupInactiveBotUsers removes accounts with cbh=0 that haven't signed in for 30 days.
func (as *AuthService) CleanupInactiveBotUsers() int {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	var candidates []models.Account
	// Find accounts with cbh=0, and either no sign-in or sign-in > 30 days ago.
	// We also check created_at for users who never signed in.
	if err := database.DB.Where("cbh = 0 AND created_at < ? AND (last_sign_at IS NULL OR last_sign_at < ?)",
		cutoff, cutoff).Find(&candidates).Error; err != nil {
		log.Printf("[cleanup] failed to query candidates: %v", err)
		return 0
	}

	deleted := 0
	for _, a := range candidates {
		if err := database.DB.Delete(&a).Error; err != nil {
			log.Printf("[cleanup] ERROR deleting account id=%d: %v", a.ID, err)
			continue
		}
		deleted++
	}
	if len(candidates) > 0 {
		log.Printf("[cleanup] scanned %d proxy accounts, deleted %d", len(candidates), deleted)
	}
	return deleted
}

type ProfileInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

func (as *AuthService) GetUserProfiles(userUUID string) []ProfileInfo {
	var account models.Account
	if err := database.DB.Where("core_user_id = ?", userUUID).First(&account).Error; err != nil {
		return nil
	}

	var profiles []models.Profile
	if err := database.DB.Where("account_id = ?", account.ID).Find(&profiles).Error; err != nil {
		return nil
	}

	result := make([]ProfileInfo, 0, len(profiles))
	for _, p := range profiles {
		pi := ProfileInfo{
			ID:   p.ID,
			Name: p.Name,
		}
		if p.Model != "" {
			pi.Model = p.Model
		}
		result = append(result, pi)
	}
	return result
}

func (as *AuthService) CreateToken(accessToken, clientToken, userID, profileID string, expiresInDays int) bool {
	as.EnforceTokenLimit(userID)

	token := models.Token{
		AccessToken:       accessToken,
		ClientToken:       clientToken,
		UserID:            userID,
		SelectedProfileID: profileID,
		IssuedAt:          utils.CurrentTimestampMillis(),
		ExpiresInDays:     expiresInDays,
		State:             "valid",
	}
	result := database.DB.Create(&token)
	return result.Error == nil
}

func (as *AuthService) EnforceTokenLimit(userID string) int64 {
	limit := config.AppConfig.Yggdrasil.Security.MaxTokensPerUser
	if limit <= 0 {
		limit = 10
	}

	var count int64
	if err := database.DB.Model(&models.Token{}).
		Where("user_id = ? AND state = ?", userID, "valid").
		Count(&count).Error; err != nil {
		log.Printf("[token-limit] count failed for user=%s: %v", userID, err)
		return 0
	}
	if count < int64(limit) {
		return 0
	}

	toRevoke := count - int64(limit) + 1
	var oldest []models.Token
	if err := database.DB.
		Where("user_id = ? AND state = ?", userID, "valid").
		Order("issued_at ASC").
		Limit(int(toRevoke)).
		Find(&oldest).Error; err != nil {
		log.Printf("[token-limit] select oldest failed: %v", err)
		return 0
	}

	if len(oldest) == 0 {
		return 0
	}

	ids := make([]int, 0, len(oldest))
	for _, t := range oldest {
		ids = append(ids, t.ID)
	}

	res := database.DB.Model(&models.Token{}).
		Where("id IN ? AND state = ?", ids, "valid").
		Update("state", "invalid")
	if res.Error != nil {
		log.Printf("[token-limit] revoke failed: %v", res.Error)
		return 0
	}
	return res.RowsAffected
}

func (as *AuthService) InvalidateToken(accessToken string) bool {
	result := database.DB.Model(&models.Token{}).
		Where("access_token = ?", accessToken).
		Update("state", "invalid")
	return result.Error == nil
}

func (as *AuthService) InvalidateAllUserTokens(userID string) bool {
	result := database.DB.Model(&models.Token{}).
		Where("user_id = ? AND state = ?", userID, "valid").
		Update("state", "invalid")
	return result.Error == nil
}

func (as *AuthService) GetValidTokenByClientToken(userID, clientToken string) *models.Token {
	if clientToken == "" {
		return nil
	}
	var token models.Token
	result := database.DB.Where("user_id = ? AND client_token = ? AND state = ?",
		userID, clientToken, "valid").First(&token)
	if result.Error != nil {
		return nil
	}
	nowMillis := utils.CurrentTimestampMillis()
	expiryMillis := token.IssuedAt + int64(token.ExpiresInDays)*24*60*60*1000
	if nowMillis > expiryMillis {
		as.InvalidateToken(token.AccessToken)
		return nil
	}
	return &token
}

func (as *AuthService) ValidateToken(accessToken string, clientToken string) *models.Token {
	var token models.Token
	result := database.DB.Where("access_token = ? AND state = ?", accessToken, "valid").First(&token)
	if result.Error != nil {
		return nil
	}

	if clientToken != "" && clientToken != token.ClientToken {
		return nil
	}

	nowMillis := utils.CurrentTimestampMillis()
	expiryMillis := token.IssuedAt + int64(token.ExpiresInDays)*24*60*60*1000
	if nowMillis > expiryMillis {
		as.InvalidateToken(accessToken)
		return nil
	}

	return &token
}

func (as *AuthService) ValidateTokenForRefresh(accessToken string, clientToken string) *models.Token {
	var token models.Token
	result := database.DB.Where("access_token = ? AND state IN ?", accessToken, []string{"valid", "temporarily_invalid"}).
		First(&token)
	if result.Error != nil {
		return nil
	}

	if clientToken != "" && clientToken != token.ClientToken {
		return nil
	}

	nowMillis := utils.CurrentTimestampMillis()
	expiryMillis := token.IssuedAt + int64(token.ExpiresInDays)*24*60*60*1000
	if nowMillis > expiryMillis {
		as.InvalidateToken(accessToken)
		return nil
	}

	return &token
}

func (as *AuthService) RefreshTokenExpiry(accessToken string, expiresInDays int) bool {
	nowMillis := utils.CurrentTimestampMillis()
	result := database.DB.Model(&models.Token{}).
		Where("access_token = ?", accessToken).
		Updates(map[string]interface{}{
			"issued_at":       nowMillis,
			"expires_in_days": expiresInDays,
		})
	return result.Error == nil
}

func (as *AuthService) MarkOtherClientTokensTemporarilyInvalid(userID, currentClientToken string) int64 {
	result := database.DB.Model(&models.Token{}).
		Where("user_id = ? AND client_token != ? AND state = ?", userID, currentClientToken, "valid").
		Update("state", "temporarily_invalid")
	if result.Error != nil {
		return 0
	}
	return result.RowsAffected
}

func (as *AuthService) CleanupExpiredTokens() int64 {
	nowMillis := utils.CurrentTimestampMillis()
	cutoff := nowMillis - int64(config.AppConfig.Yggdrasil.Security.TokenExpiryDays+1)*24*60*60*1000
	result := database.DB.Where("state = ? OR issued_at < ?", "invalid", cutoff).
		Delete(&models.Token{})
	if result.Error != nil {
		return 0
	}
	return result.RowsAffected
}

func (as *AuthService) GetProfileByID(profileID string) *ProfileInfo {
	var profile models.Profile
	if err := database.DB.Where("id = ?", profileID).First(&profile).Error; err != nil {
		return nil
	}

	return &ProfileInfo{
		ID:    profile.ID,
		Name:  profile.Name,
		Model: profile.Model,
	}
}

func (as *AuthService) IsProfileOwnedByUser(profileID, userID string) bool {
	var account models.Account
	if err := database.DB.Where("core_user_id = ?", userID).First(&account).Error; err != nil {
		return false
	}

	var profile models.Profile
	if err := database.DB.Where("id = ? AND account_id = ?", profileID, account.ID).First(&profile).Error; err != nil {
		return false
	}
	return true
}

func (as *AuthService) CreateSession(profileID, serverID, ip string) bool {
	var existingSession models.Session
	result := database.DB.
		Where("profile_id = ? AND server_id = ?", profileID, serverID).
		First(&existingSession)

	if result.Error == nil {
		existingSession.IP = ip
		existingSession.ExpiresAt = time.Now().Add(time.Duration(config.AppConfig.Yggdrasil.Security.SessionExpirySeconds) * time.Second)
		return database.DB.Save(&existingSession).Error == nil
	}

	session := models.Session{
		ProfileID: profileID,
		ServerID:  serverID,
		IP:        ip,
		ExpiresAt: time.Now().Add(time.Duration(config.AppConfig.Yggdrasil.Security.SessionExpirySeconds) * time.Second),
	}
	return database.DB.Create(&session).Error == nil
}

func (as *AuthService) GetSessionByProfileAndServer(profileName, serverID string) *models.Session {
	var profile models.Profile
	if err := database.DB.Where("name = ?", profileName).First(&profile).Error; err != nil {
		return nil
	}

	var session models.Session
	result := database.DB.
		Where("profile_id = ? AND server_id = ?", profile.ID, serverID).
		First(&session)

	if result.Error != nil {
		return nil
	}

	session.ExpiresAt = time.Now().Add(time.Duration(config.AppConfig.Yggdrasil.Security.SessionExpirySeconds) * time.Second)
	database.DB.Save(&session)

	return &session
}

func (as *AuthService) CleanupExpiredSessions() int64 {
	result := database.DB.Where("expires_at < ?", time.Now().Add(-24*time.Hour)).
		Delete(&models.Session{})
	if result.Error != nil {
		return 0
	}
	return result.RowsAffected
}

func (as *AuthService) IsLoginRateLimited(identifier string) bool {
	cfg := config.AppConfig.Security
	key := fmt.Sprintf("%slogin_attempts:%s", config.AppConfig.Redis.Prefix, identifier)

	ctx := context.Background()
	countStr, err := redis.Client.Get(ctx, key).Result()
	if err != nil {
		return false
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return false
	}

	return count >= cfg.RateLimitMaxAttempts
}

func (as *AuthService) RecordLoginAttempt(identifier string, success bool) {
	cfg := config.AppConfig.Security
	key := fmt.Sprintf("%slogin_attempts:%s", config.AppConfig.Redis.Prefix, identifier)
	window := time.Duration(cfg.RateLimitWindowSec) * time.Second

	ctx := context.Background()

	if success {
		redis.Client.Del(ctx, key)
		return
	}

	pipe := redis.Client.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	_, _ = pipe.Exec(ctx)
}

func (as *AuthService) GetUserByID(userUUID string) *UserInfo {
	// For simplicity, we can assume we don't have a direct GetUserByID internal API yet,
	// but we might need it. Let's assume the CoreClient has it or we use profiles to find user.
	// Actually, let's just use VerifyCredentials or a new one.
	// For now, I'll return a placeholder or implement it in CoreClient.
	return nil
}

func (as *AuthService) GetProfileByName(name string) *models.Profile {
	var profile models.Profile
	if err := database.DB.Where("name = ?", name).First(&profile).Error; err != nil {
		return nil
	}
	return &profile
}

func (as *AuthService) IsManageToken(token, authType string) bool {
	return authType == "manage" && token != "" && config.AppConfig.Manage.Token != "" && token == config.AppConfig.Manage.Token
}
