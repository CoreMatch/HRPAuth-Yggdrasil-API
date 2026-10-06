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

func (as *AuthService) VerifyCredentials(identifier, password string) *UserInfo {
	user, err := as.coreClient.VerifyCredentials(identifier, password)
	if err != nil {
		return nil
	}

	// Ensure local Account exists (1:1 mapping)
	var account models.Account
	result := database.DB.Where("core_user_id = ?", user.UUID).First(&account)
	if result.Error != nil {
		account = models.Account{
			CoreUserID: user.UUID,
		}
		database.DB.Create(&account)
	}

	return &UserInfo{
		UUID:     user.UUID,
		Email:    user.Email,
		Username: user.Username,
	}
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
