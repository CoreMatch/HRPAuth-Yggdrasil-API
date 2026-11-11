package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
)

// MicrosoftController implements the Microsoft-login mapping endpoints
// (HA-Contract hrpauth-yggdrasil-business.yaml 1.2.0):
//
//	POST /authserver/microsoft      — exchange an MC token for a Yggdrasil session
//	POST /player/microsoft-bind     — voluntary on-the-spot bind (Bearer)
//	POST /player/microsoft-unbind   — clear the caller's mojang_uuid (Bearer)
//
// Play first, bind later is the default: an unbound genuine identity is
// absorbed with the existing proxy-register semantics and answered with a
// playable session (claimRequired=true), never with an error.
type MicrosoftController struct {
	authService  *services.AuthService
	playerSvc    *services.PlayerService
	microsoftSvc *services.MicrosoftService
	skinSync     *services.SkinSyncService
}

func NewMicrosoftController() *MicrosoftController {
	return &MicrosoftController{
		authService:  services.NewAuthService(),
		playerSvc:    services.NewPlayerService(),
		microsoftSvc: services.NewMicrosoftService(),
		skinSync:     services.NewSkinSyncService(),
	}
}

type MicrosoftExchangeRequest struct {
	MCAccessToken string `json:"mcAccessToken"`
	ClientToken   string `json:"clientToken"`
}

type MicrosoftBindRequest struct {
	MCAccessToken string `json:"mcAccessToken"`
}

// mapVerificationError maps a MicrosoftService re-verification error onto the
// (status, code) pair the caller should surface. yggdrasilEnvelope selects the
// Mojang error shape (authserver exchange) vs the HA business envelope (bind).
func mapVerificationError(err error) (int, string) {
	switch {
	case errors.Is(err, services.ErrMCVerificationFailed):
		return http.StatusForbidden, CodeMinecraftVerificationFailed
	case errors.Is(err, services.ErrMCProfileMissing):
		return http.StatusForbidden, CodeMinecraftProfileMissing
	case errors.Is(err, services.ErrMCLicenseNotFound):
		return http.StatusForbidden, CodeMinecraftLicenseNotFound
	default:
		return http.StatusBadGateway, CodeInternalError
	}
}

// Exchange POST /authserver/microsoft — trade a Minecraft Services access
// token for a standard Yggdrasil session plus claimRequired.
func (mc *MicrosoftController) Exchange(c *gin.Context) {
	var req MicrosoftExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.MCAccessToken) == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange result=invalid_request status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	// Rate limit on the client IP (no username is known before verification);
	// reuses the authenticate login-attempt bucket semantics.
	rateKey := "ms:" + c.ClientIP()
	if mc.authService.IsLoginRateLimited(rateKey) {
		log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange result=rate_limited status=%d", c.GetString("request_id"), http.StatusTooManyRequests)
		sendYggdrasilError(c, "ForbiddenOperationException", "Too many login attempts. Please try again later.", http.StatusTooManyRequests)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	profile, err := mc.microsoftSvc.VerifyMCToken(ctx, req.MCAccessToken)
	if err != nil {
		mc.authService.RecordLoginAttempt(rateKey, false)
		status, code := mapVerificationError(err)
		log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange result=verification_failed code=%s status=%d", c.GetString("request_id"), code, status)
		sendYggdrasilError(c, "ForbiddenOperationException", code, status)
		return
	}

	// Resolve the account for the genuine uuid: an already-linked account is
	// used as-is; an unlinked one is absorbed with the proxy-register decision
	// tree (the same semantics WinnerProxy's hasJoined stage 3 uses).
	var account models.Account
	if err := database.DB.Where("mojang_uuid = ?", profile.ID).First(&account).Error; err != nil {
		absorbed, _, perr := mc.authService.ProxyRegister(profile.Name, utils.GenerateAccessToken(), profile.ID)
		if perr != nil {
			if errors.Is(perr, services.ErrUsernameAlreadyBound) {
				log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange name=%s result=username_already_bound status=%d", c.GetString("request_id"), profile.Name, http.StatusConflict)
				sendYggdrasilError(c, "ForbiddenOperationException", CodeUsernameAlreadyBound, http.StatusConflict)
				return
			}
			log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange result=absorb_failed err=%v status=%d", c.GetString("request_id"), perr, http.StatusInternalServerError)
			sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session. Please try again.", http.StatusInternalServerError)
			return
		}
		account = *absorbed
	}

	mc.authService.RecordLoginAttempt(rateKey, true)
	database.DB.Model(&account).Update("last_sign_at", time.Now())

	// Best-effort skin sync retry, same as password authenticate.
	mc.skinSyncRetryOnLogin(account.ID)

	accessToken, clientToken, selected, profiles, err := mc.issueSession(account.ID, req.ClientToken)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange account=%d result=create_token_failed status=%d", c.GetString("request_id"), account.ID, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session. Please try again.", http.StatusForbidden)
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=microsoft_exchange account=%s result=success profile=%s/%s claim_required=%t status=%d",
		c.GetString("request_id"), profile, selected.ID, selected.Name, !account.CBH, http.StatusOK)

	c.JSON(http.StatusOK, gin.H{
		"accessToken":       accessToken,
		"clientToken":       clientToken,
		"availableProfiles": profiles,
		"selectedProfile":   selected,
		"claimRequired":     !account.CBH,
	})
}

// issueSession creates (or reuses) a Yggdrasil token for the account, mirroring
// the Authenticate flow. Returns the access/client tokens, the selected profile
// and the full profile list.
func (mc *MicrosoftController) issueSession(accountID int, clientToken string) (string, string, services.ProfileInfo, []services.ProfileInfo, error) {
	profiles := mc.authService.GetUserProfiles(accountID)
	if len(profiles) == 0 {
		return "", "", services.ProfileInfo{}, nil, errors.New("account has no profiles")
	}
	if clientToken == "" {
		clientToken = utils.GenerateClientToken()
	}
	accessToken := utils.GenerateAccessToken()
	selected := profiles[0]
	expiresInDays := config.AppConfig.Yggdrasil.Security.TokenExpiryDays

	if existing := mc.authService.GetValidTokenByClientToken(accountID, clientToken); existing != nil {
		for _, p := range profiles {
			if p.ID == existing.SelectedProfileID {
				selected = p
				break
			}
		}
		accessToken = existing.AccessToken
		mc.authService.RefreshTokenExpiry(existing.AccessToken, expiresInDays)
	} else {
		mc.authService.MarkOtherClientTokensTemporarilyInvalid(accountID, clientToken)
		if !mc.authService.CreateToken(accessToken, clientToken, accountID, selected.ID, expiresInDays) {
			return "", "", services.ProfileInfo{}, nil, errors.New("failed to create token")
		}
	}
	return accessToken, clientToken, selected, profiles, nil
}

// skinSyncRetryOnLogin mirrors YggdrasilController.skinSyncRetryOnLogin.
func (mc *MicrosoftController) skinSyncRetryOnLogin(accountID int) {
	if accountID == 0 {
		return
	}
	var account models.Account
	if err := database.DB.First(&account, accountID).Error; err != nil {
		return
	}
	if !mc.skinSync.LoginRetryNeeded(&account) {
		return
	}
	mc.skinSync.RequestSkinSync(account.ID, "login_retry")
}

// Bind POST /player/microsoft-bind — voluntary on-the-spot bind for the caller.
func (mc *MicrosoftController) Bind(c *gin.Context) {
	bearer := extractBearer(c)
	account, err := mc.playerSvc.ResolveAccount(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}

	var req MicrosoftBindRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.MCAccessToken) == "" {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "mcAccessToken is required")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	profile, err := mc.microsoftSvc.VerifyMCToken(ctx, req.MCAccessToken)
	if err != nil {
		status, code := mapVerificationError(err)
		if status == http.StatusBadGateway {
			respondError(c, http.StatusBadGateway, code, "minecraft services unavailable")
			return
		}
		// Business envelope uses 400 for verification failures.
		respondError(c, http.StatusBadRequest, code, code)
		return
	}

	// Idempotent: already bound to this exact uuid.
	if account.MojangUUID != nil && *account.MojangUUID == profile.ID {
		info, _ := mc.playerSvc.GetPlayerInfo(bearer)
		respondOK(c, "Already bound", info)
		return
	}
	// The caller's account is bound to a different uuid — unbind first.
	if account.MojangUUID != nil && *account.MojangUUID != "" {
		respondError(c, http.StatusConflict, CodeMojangUUIDAlreadyBound, "account already bound to a different mojang_uuid")
		return
	}
	// The uuid belongs to another account — operator force-bind territory.
	var owner models.Account
	if err := database.DB.Where("mojang_uuid = ?", profile.ID).First(&owner).Error; err == nil && owner.ID != account.ID {
		respondError(c, http.StatusConflict, CodeMojangUUIDBoundToAnother, "mojang_uuid already bound to another account")
		return
	}

	if err := database.DB.Model(&models.Account{}).
		Where("id = ?", account.ID).
		Updates(map[string]interface{}{"mojang_uuid": profile.ID, "last_sign_at": time.Now()}).Error; err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to bind")
		return
	}
	log.Printf("[YGG-BIZ] request_id=%s action=microsoft_bind account=%d mojang_uuid=%s result=success", c.GetString("request_id"), account.ID, profile.ID)
	mc.skinSync.RequestSkinSync(account.ID, "microsoft_bind")

	info, err := mc.playerSvc.GetPlayerInfo(bearer)
	if err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to read player info")
		return
	}
	respondOK(c, "Microsoft account bound", info)
}

// Unbind POST /player/microsoft-unbind — clear the caller's mojang_uuid.
func (mc *MicrosoftController) Unbind(c *gin.Context) {
	bearer := extractBearer(c)
	account, err := mc.playerSvc.ResolveAccount(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}

	// Idempotent: clearing an already-unbound account succeeds.
	if err := database.DB.Model(&models.Account{}).
		Where("id = ?", account.ID).
		Update("mojang_uuid", nil).Error; err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to unbind")
		return
	}
	log.Printf("[YGG-BIZ] request_id=%s action=microsoft_unbind account=%d result=success", c.GetString("request_id"), account.ID)

	info, err := mc.playerSvc.GetPlayerInfo(bearer)
	if err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to read player info")
		return
	}
	respondOK(c, "Microsoft account unbound", info)
}
