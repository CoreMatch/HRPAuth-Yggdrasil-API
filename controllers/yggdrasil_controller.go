package controllers

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
)

type YggdrasilController struct {
	authService    *services.AuthService
	textureService *services.TextureService
}

func NewYggdrasilController() *YggdrasilController {
	return &YggdrasilController{
		authService:    services.NewAuthService(),
		textureService: services.NewTextureService(),
	}
}

type AgentInfo struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

type AuthenticateRequest struct {
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	Agent       AgentInfo `json:"agent"`
	ClientToken string    `json:"clientToken"`
	RequestUser bool      `json:"requestUser"`
}

type RefreshRequest struct {
	AccessToken     string `json:"accessToken"`
	ClientToken     string `json:"clientToken"`
	SelectedProfile *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"selectedProfile"`
	RequestUser bool `json:"requestUser"`
}

type ValidateRequest struct {
	AccessToken string `json:"accessToken"`
	ClientToken string `json:"clientToken"`
}

type InvalidateRequest struct {
	AccessToken string `json:"accessToken"`
	ClientToken string `json:"clientToken"`
}

type SignoutRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type JoinRequest struct {
	AccessToken     string `json:"accessToken"`
	SelectedProfile string `json:"selectedProfile"`
	ServerID        string `json:"serverId"`
}

func sendYggdrasilError(c *gin.Context, errType, errMessage string, statusCode int) {
	c.JSON(statusCode, gin.H{
		"error":        errType,
		"errorMessage": errMessage,
		"cause":        nil,
	})
}

// maskToken 对敏感凭据（accessToken/clientToken）做摘要输出，避免完整 token 落入日志。
func maskToken(s string) string {
	if len(s) <= 16 {
		return "***"
	}
	return s[:8] + "..." + s[len(s)-4:]
}

func (yc *YggdrasilController) Meta(c *gin.Context) {
	c.Header("X-Authlib-Injector-API-Location", "/")
	c.JSON(http.StatusOK, gin.H{
		"status":  "online",
		"success": true,
		"message": "HRPAuth Yggdrasil API Provider is running.",
		"site": gin.H{
			"name":        config.AppConfig.Site.Name,
			"url":         config.AppConfig.Runtime.SiteURL,
			"version":     config.AppConfig.Site.Version,
			"server_time": time.Now().Format("2006-01-02 15:04:05"),
		},
		"yggdrasil": yc.GetMetadata(),
	})
	log.Printf("[YGG-BIZ] request_id=%s action=meta result=success status=%d", c.GetString("request_id"), http.StatusOK)
}

func (yc *YggdrasilController) GetMetadata() gin.H {
	cfg := config.AppConfig.Yggdrasil.Server
	frontendURL := config.AppConfig.Runtime.FrontendURL

	homepage := cfg.Links.Homepage
	register := cfg.Links.Register

	// Prioritize frontendURL from backend for automatic splicing
	if frontendURL != "" {
		homepage = frontendURL
		register = strings.TrimRight(frontendURL, "/") + "/register"
	}

	links := gin.H{
		"homepage": homepage,
		"register": register,
	}

	skinDomains := cfg.SkinDomains
	if len(skinDomains) == 0 {
		skinDomains = []string{
			utils.ExtractDomain(config.AppConfig.Runtime.SiteURL),
			"." + utils.ExtractDomain(config.AppConfig.Runtime.SiteURL),
		}
	}

	return gin.H{
		"meta": gin.H{
			"serverName":                          cfg.Name,
			"implementationName":                  cfg.Implementation,
			"implementationVersion":               cfg.Version,
			"links":                               links,
			"feature.non_email_login":             config.AppConfig.Yggdrasil.FeatureFlags.NonEmailLogin,
			"feature.legacy_skin_api":             config.AppConfig.Yggdrasil.FeatureFlags.LegacySkinAPI,
			"feature.no_mojang_namespace":         config.AppConfig.Yggdrasil.FeatureFlags.NoMojangNamespace,
			"feature.enable_mojang_anti_features": config.AppConfig.Yggdrasil.FeatureFlags.EnableMojangAntiFeatures,
			"feature.enable_profile_key":          config.AppConfig.Yggdrasil.FeatureFlags.EnableProfileKey,
			"feature.username_check":              config.AppConfig.Yggdrasil.FeatureFlags.UsernameCheck,
		},
		"skinDomains":        skinDomains,
		"signaturePublickey": cfg.SignaturePublicKey,
	}
}

func (yc *YggdrasilController) Authenticate(c *gin.Context) {
	var req AuthenticateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=authenticate result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	if req.Username == "" || req.Password == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=authenticate result=missing_credentials status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	start := time.Now()
	if yc.authService.IsLoginRateLimited(req.Username) {
		log.Printf("[YGG-BIZ] request_id=%s action=authenticate username=%s result=rate_limited status=%d", c.GetString("request_id"), req.Username, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Too many login attempts. Please try again later.", http.StatusForbidden)
		return
	}

	user, err := yc.authService.VerifyCredentials(req.Username, req.Password)
	if err != nil || user == nil {
		yc.authService.RecordLoginAttempt(req.Username, false)
		log.Printf("[YGG-BIZ] request_id=%s action=authenticate username=%s result=invalid_credentials duration_ms=%d status=%d", c.GetString("request_id"), req.Username, time.Since(start).Milliseconds(), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	yc.authService.RecordLoginAttempt(req.Username, true)

	profiles := yc.authService.GetUserProfiles(user.AccountID)
	if len(profiles) == 0 {
		log.Printf("[YGG-BIZ] request_id=%s action=authenticate username=%s result=no_profiles status=%d", c.GetString("request_id"), req.Username, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "User has no profiles.", http.StatusForbidden)
		return
	}

	clientToken := req.ClientToken
	if clientToken == "" {
		clientToken = utils.GenerateClientToken()
	}

	accessToken := utils.GenerateAccessToken()
	selectedProfile := profiles[0]
	expiresInDays := config.AppConfig.Yggdrasil.Security.TokenExpiryDays

	if existing := yc.authService.GetValidTokenByClientToken(user.AccountID, clientToken); existing != nil {
		for _, p := range profiles {
			if p.ID == existing.SelectedProfileID {
				selectedProfile = p
				break
			}
		}
		accessToken = existing.AccessToken
		yc.authService.RefreshTokenExpiry(existing.AccessToken, expiresInDays)
	} else {
		yc.authService.MarkOtherClientTokensTemporarilyInvalid(user.AccountID, clientToken)
		if !yc.authService.CreateToken(accessToken, clientToken, user.AccountID, selectedProfile.ID, expiresInDays) {
			log.Printf("[YGG-BIZ] request_id=%s action=authenticate username=%s result=create_token_failed status=%d", c.GetString("request_id"), req.Username, http.StatusForbidden)
			sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session. Please try again.", http.StatusForbidden)
			return
		}
	}

	log.Printf("[YGG-BIZ] request_id=%s action=authenticate username=%s account=%d result=success profile=%s/%s client_token=%s duration_ms=%d status=%d",
		c.GetString("request_id"), req.Username, user.AccountID, selectedProfile.ID, selectedProfile.Name, maskToken(clientToken), time.Since(start).Milliseconds(), http.StatusOK)

	response := gin.H{
		"accessToken":       accessToken,
		"clientToken":       clientToken,
		"availableProfiles": profiles,
		"selectedProfile":   selectedProfile,
	}

	if req.RequestUser {
		response["user"] = gin.H{
			"id":         user.UUID,
			"email":      user.Email,
			"username":   user.Username,
			"properties": []gin.H{},
		}
	}

	c.JSON(http.StatusOK, response)
}

func (yc *YggdrasilController) Refresh(c *gin.Context) {
	var req RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=refresh result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateTokenForRefresh(req.AccessToken, req.ClientToken)
	if token == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=refresh access_token=%s result=invalid_token status=%d", c.GetString("request_id"), maskToken(req.AccessToken), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	profiles := yc.authService.GetUserProfiles(token.AccountID)
	if len(profiles) == 0 {
		log.Printf("[YGG-BIZ] request_id=%s action=refresh result=no_profiles status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "User has no profiles.", http.StatusForbidden)
		return
	}

	newAccessToken := utils.GenerateAccessToken()
	yc.authService.InvalidateToken(req.AccessToken)

	selectedProfileID := token.SelectedProfileID
	if req.SelectedProfile != nil {
		if yc.authService.IsProfileOwnedByAccount(req.SelectedProfile.ID, token.AccountID) {
			selectedProfileID = req.SelectedProfile.ID
		}
	}

	var selectedProfile *services.ProfileInfo
	for _, p := range profiles {
		if p.ID == selectedProfileID {
			selectedProfile = &p
			break
		}
	}
	if selectedProfile == nil {
		selectedProfile = &profiles[0]
	}

	expiresInDays := config.AppConfig.Yggdrasil.Security.TokenExpiryDays
	yc.authService.MarkOtherClientTokensTemporarilyInvalid(token.AccountID, req.ClientToken)
	yc.authService.CreateToken(newAccessToken, req.ClientToken, token.AccountID, selectedProfile.ID, expiresInDays)

	response := gin.H{
		"accessToken":     newAccessToken,
		"clientToken":     req.ClientToken,
		"selectedProfile": selectedProfile,
	}

	if req.RequestUser {
		// Simplified: assuming user info is part of the token or core API provides it.
		// For now, we skip detailed user info if not available.
	}

	log.Printf("[YGG-BIZ] request_id=%s action=refresh account=%d old_access_token=%s new_access_token=%s profile=%s/%s client_token=%s result=success status=%d",
		c.GetString("request_id"), token.AccountID, maskToken(req.AccessToken), maskToken(newAccessToken), selectedProfile.ID, selectedProfile.Name, maskToken(req.ClientToken), http.StatusOK)

	c.JSON(http.StatusOK, response)
}

func (yc *YggdrasilController) Validate(c *gin.Context) {
	var req ValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=validate result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateToken(req.AccessToken, req.ClientToken)
	if token == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=validate access_token=%s result=invalid_token status=%d", c.GetString("request_id"), maskToken(req.AccessToken), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=validate account=%d access_token=%s result=valid status=%d", c.GetString("request_id"), token.AccountID, maskToken(req.AccessToken), http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Invalidate(c *gin.Context) {
	var req InvalidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=invalidate result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	yc.authService.InvalidateToken(req.AccessToken)
	log.Printf("[YGG-BIZ] request_id=%s action=invalidate access_token=%s result=success status=%d", c.GetString("request_id"), maskToken(req.AccessToken), http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Signout(c *gin.Context) {
	var req SignoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=signout result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	if yc.authService.IsLoginRateLimited(req.Username) {
		log.Printf("[YGG-BIZ] request_id=%s action=signout username=%s result=rate_limited status=%d", c.GetString("request_id"), req.Username, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Too many signout attempts. Please try again later.", http.StatusForbidden)
		return
	}

	user, err := yc.authService.VerifyCredentials(req.Username, req.Password)
	if err != nil || user == nil {
		yc.authService.RecordLoginAttempt(req.Username, false)
		log.Printf("[YGG-BIZ] request_id=%s action=signout username=%s result=invalid_credentials status=%d", c.GetString("request_id"), req.Username, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	yc.authService.RecordLoginAttempt(req.Username, true)
	yc.authService.InvalidateAllAccountTokens(user.AccountID)
	log.Printf("[YGG-BIZ] request_id=%s action=signout username=%s account=%d result=success status=%d", c.GetString("request_id"), req.Username, user.AccountID, http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Join(c *gin.Context) {
	var req JoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=join result=invalid_json status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateToken(req.AccessToken, "")
	if token == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=join access_token=%s result=invalid_token status=%d", c.GetString("request_id"), maskToken(req.AccessToken), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	if req.SelectedProfile != token.SelectedProfileID {
		if !yc.authService.IsProfileOwnedByAccount(req.SelectedProfile, token.AccountID) {
			log.Printf("[YGG-BIZ] request_id=%s action=join account=%d profile=%s result=profile_not_owned status=%d", c.GetString("request_id"), token.AccountID, req.SelectedProfile, http.StatusForbidden)
			sendYggdrasilError(c, "ForbiddenOperationException", "Invalid profile.", http.StatusForbidden)
			return
		}
	}

	ip := c.ClientIP()
	if !yc.authService.CreateSession(req.SelectedProfile, req.ServerID, ip) {
		log.Printf("[YGG-BIZ] request_id=%s action=join account=%d profile=%s server=%s result=create_session_failed status=%d", c.GetString("request_id"), token.AccountID, req.SelectedProfile, req.ServerID, http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session.", http.StatusForbidden)
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=join account=%d profile=%s server=%s ip=%s result=success status=%d", c.GetString("request_id"), token.AccountID, req.SelectedProfile, req.ServerID, ip, http.StatusNoContent)
	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) HasJoined(c *gin.Context) {
	username := c.Query("username")
	serverID := c.Query("serverId")
	ip := c.Query("ip")

	if username == "" || serverID == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s server=%s result=bad_request status=%d", c.GetString("request_id"), username, serverID, http.StatusBadRequest)
		sendYggdrasilError(c, "BadRequestException", "Bad request.", http.StatusBadRequest)
		return
	}

	profile := yc.authService.GetProfileByName(username)
	if profile == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s server=%s ip=%s result=no_profile status=%d", c.GetString("request_id"), username, serverID, ip, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	session := yc.authService.GetSessionByProfileAndServer(username, serverID)
	if session == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s server=%s result=no_session status=%d", c.GetString("request_id"), username, serverID, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	if ip != "" && config.AppConfig.Yggdrasil.FeatureFlags.EnableIPCheck && session.IP != ip {
		log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s server=%s session_ip=%s request_ip=%s result=ip_mismatch status=%d", c.GetString("request_id"), username, serverID, session.IP, ip, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	properties, err := yc.textureService.GetProfileProperties(profile.ID, profile.Name, false)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s server=%s result=success_no_properties status=%d", c.GetString("request_id"), username, serverID, http.StatusOK)
		c.JSON(http.StatusOK, gin.H{
			"id":         profile.ID,
			"name":       profile.Name,
			"properties": []gin.H{},
		})
		return
	}

	props := make([]gin.H, 0)
	for _, prop := range properties {
		p := gin.H{
			"name":  prop.Name,
			"value": prop.Value,
		}
		if prop.Signature != "" {
			p["signature"] = prop.Signature
		}
		props = append(props, p)
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         profile.ID,
		"name":       profile.Name,
		"properties": props,
	})
	log.Printf("[YGG-BIZ] request_id=%s action=hasJoined username=%s profile=%s/%s server=%s ip=%s properties=%d result=success status=%d", c.GetString("request_id"), username, profile.ID, profile.Name, serverID, ip, len(props), http.StatusOK)
}

func (yc *YggdrasilController) ProfileQuery(c *gin.Context) {
	uuid := c.Param("uuid")
	unsignedStr := c.DefaultQuery("unsigned", "true")
	unsigned := unsignedStr == "true"

	profile := yc.authService.GetProfileByID(uuid)
	if profile == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=profile_query uuid=%s result=not_found status=%d", c.GetString("request_id"), uuid, http.StatusNotFound)
		sendYggdrasilError(c, "ProfileNotFoundException", "No such profile.", http.StatusNotFound)
		return
	}

	properties, err := yc.textureService.GetProfileProperties(uuid, profile.Name, unsigned)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=profile_query uuid=%s result=success_no_properties status=%d", c.GetString("request_id"), uuid, http.StatusOK)
		c.JSON(http.StatusOK, gin.H{
			"id":         profile.ID,
			"name":       profile.Name,
			"properties": []gin.H{},
		})
		return
	}

	props := make([]gin.H, 0)
	for _, prop := range properties {
		p := gin.H{
			"name":  prop.Name,
			"value": prop.Value,
		}
		if prop.Signature != "" && !unsigned {
			p["signature"] = prop.Signature
		}
		props = append(props, p)
	}

	c.JSON(http.StatusOK, gin.H{
		"id":         profile.ID,
		"name":       profile.Name,
		"properties": props,
	})
	log.Printf("[YGG-BIZ] request_id=%s action=profile_query uuid=%s profile=%s/%s unsigned=%t properties=%d result=success status=%d", c.GetString("request_id"), uuid, profile.ID, profile.Name, unsigned, len(props), http.StatusOK)
}

type BatchProfileRequest struct {
	Names []string `json:"names"`
}

func (yc *YggdrasilController) BatchProfiles(c *gin.Context) {
	var req BatchProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=batch_profiles result=invalid_json status=%d", c.GetString("request_id"), http.StatusBadRequest)
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "Bad request.")
		return
	}

	result := make([]gin.H, 0)
	for _, name := range req.Names {
		profile := yc.authService.GetProfileByName(name)
		if profile != nil {
			result = append(result, gin.H{
				"id":   profile.ID,
				"name": profile.Name,
			})
		}
	}

	log.Printf("[YGG-BIZ] request_id=%s action=batch_profiles requested=%d matched=%d result=success status=%d", c.GetString("request_id"), len(req.Names), len(result), http.StatusOK)
	respondOK(c, "Batch profile query successful", result)
}

func (yc *YggdrasilController) PlayerCertificates(c *gin.Context) {
	if !config.AppConfig.Yggdrasil.FeatureFlags.EnableProfileKey {
		log.Printf("[YGG-BIZ] request_id=%s action=player_certificates result=feature_disabled status=%d", c.GetString("request_id"), http.StatusForbidden)
		sendYggdrasilError(c, "ForbiddenOperationException", "Profile key feature is disabled.", http.StatusForbidden)
		return
	}

	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=player_certificates result=missing_token status=%d", c.GetString("request_id"), http.StatusUnauthorized)
		c.JSON(http.StatusUnauthorized, gin.H{
			"path":         "/minecraftservices/player/certificates",
			"errorType":    "Unauthorized",
			"error":        "Unauthorized",
			"errorMessage": "Unauthorized",
		})
		return
	}

	token := yc.authService.ValidateToken(accessToken, "")
	if token == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=player_certificates access_token=%s result=invalid_token status=%d", c.GetString("request_id"), maskToken(accessToken), http.StatusUnauthorized)
		c.JSON(http.StatusUnauthorized, gin.H{
			"path":         "/minecraftservices/player/certificates",
			"errorType":    "Unauthorized",
			"error":        "Unauthorized",
			"errorMessage": "Unauthorized",
		})
		return
	}

	pkService := services.NewProfileKeyService()
	issued, err := pkService.IssueOrRotate(token.AccountID, false)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=player_certificates account=%d result=issue_failed err=%v status=%d", c.GetString("request_id"), token.AccountID, err, http.StatusInternalServerError)
		sendYggdrasilError(c, "InternalException", "Failed to issue profile key.", http.StatusInternalServerError)
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=player_certificates account=%d expires_at=%s result=success status=%d", c.GetString("request_id"), token.AccountID, issued.ExpiresAt.Format("2006-01-02 15:04:05"), http.StatusOK)
	c.JSON(http.StatusOK, pkService.BuildResponse(issued))
}

func (yc *YggdrasilController) PublicKeys(c *gin.Context) {
	pkService := services.NewProfileKeyService()
	log.Printf("[YGG-BIZ] request_id=%s action=public_keys result=success status=%d", c.GetString("request_id"), http.StatusOK)
	c.JSON(http.StatusOK, pkService.BuildPublicKeysResponse())
}

func (yc *YggdrasilController) DownloadTexture(c *gin.Context) {
	hash := c.Param("hash")
	if hash == "" {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Bad request.")
		return
	}

	data, contentType, err := yc.textureService.GetTextureByHash(hash)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=download_texture hash=%s result=not_found status=%d", c.GetString("request_id"), hash, http.StatusNotFound)
		respondError(c, http.StatusNotFound, CodeTargetNotFound, "Texture not found.")
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=download_texture hash=%s type=%s size=%d result=success status=%d", c.GetString("request_id"), hash, contentType, len(data), http.StatusOK)
	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

func (yc *YggdrasilController) DownloadPreview(c *gin.Context) {
	fileName := c.Param("fileName")
	if fileName == "" {
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "Bad request.")
		return
	}

	data, contentType, err := yc.textureService.GetPreviewByFileName(fileName)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=download_preview file=%s result=not_found status=%d", c.GetString("request_id"), fileName, http.StatusNotFound)
		respondError(c, http.StatusNotFound, CodeTargetNotFound, "Preview not found.")
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=download_preview file=%s type=%s size=%d result=success status=%d", c.GetString("request_id"), fileName, contentType, len(data), http.StatusOK)
	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

func (yc *YggdrasilController) UploadTexture(c *gin.Context) {
	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=upload_texture result=missing_token status=%d", c.GetString("request_id"), http.StatusUnauthorized)
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "Unauthorized")
		return
	}

	profileID := c.Param("uuid")
	textureType := c.DefaultPostForm("type", "skin")
	model := c.DefaultPostForm("model", "default")
	name := c.PostForm("name")
	description := c.PostForm("description")
	tags := c.PostForm("tags")

	file, err := c.FormFile("file")
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=upload_texture profile=%s type=%s result=file_required status=%d", c.GetString("request_id"), profileID, textureType, http.StatusBadRequest)
		respondError(c, http.StatusBadRequest, CodeTextureFileRequired, "file is required")
		return
	}

	opened, err := file.Open()
	if err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to open file")
		return
	}
	defer opened.Close()

	fileData, err := io.ReadAll(opened)
	if err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to read file")
		return
	}

	warnings, err := yc.textureService.UploadTexture(accessToken, profileID, textureType, model, name, description, tags, fileData)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=upload_texture profile=%s type=%s size=%d result=failed err=%v status=%d", c.GetString("request_id"), profileID, textureType, len(fileData), err, http.StatusBadRequest)
		respondError(c, http.StatusBadRequest, CodeTextureUploadFailed, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=upload_texture profile=%s type=%s model=%s size=%d warnings=%d result=success status=%d", c.GetString("request_id"), profileID, textureType, model, len(fileData), len(warnings), http.StatusOK)
	respondOK(c, "Texture uploaded successfully", gin.H{
		"warnings": warnings,
	})
}

func (yc *YggdrasilController) DeleteTexture(c *gin.Context) {
	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
		log.Printf("[YGG-BIZ] request_id=%s action=delete_texture result=missing_token status=%d", c.GetString("request_id"), http.StatusUnauthorized)
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "Unauthorized")
		return
	}

	profileID := c.Param("uuid")
	textureType := c.Param("type")

	token := yc.authService.ValidateToken(accessToken, "")
	if token == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=delete_texture access_token=%s result=invalid_token status=%d", c.GetString("request_id"), maskToken(accessToken), http.StatusUnauthorized)
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "Unauthorized")
		return
	}

	if !yc.authService.IsProfileOwnedByAccount(profileID, token.AccountID) {
		log.Printf("[YGG-BIZ] request_id=%s action=delete_texture account=%d profile=%s type=%s result=access_denied status=%d", c.GetString("request_id"), token.AccountID, profileID, textureType, http.StatusForbidden)
		respondError(c, http.StatusForbidden, CodeProfileAccessDenied, "Forbidden")
		return
	}

	// Logic to remove texture from profile property
	if err := yc.textureService.RemoveProfileTexture(profileID, textureType); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=delete_texture account=%d profile=%s type=%s result=failed err=%v status=%d", c.GetString("request_id"), token.AccountID, profileID, textureType, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeTextureDeleteFailed, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=delete_texture account=%d profile=%s type=%s result=success status=%d", c.GetString("request_id"), token.AccountID, profileID, textureType, http.StatusOK)
	respondOK(c, "Texture deleted successfully", nil)
}

func (yc *YggdrasilController) LegacySkin(c *gin.Context) {
	if !config.AppConfig.Yggdrasil.FeatureFlags.LegacySkinAPI {
		log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin result=feature_disabled status=%d", c.GetString("request_id"), http.StatusNotFound)
		sendYggdrasilError(c, "NotFoundException", "Legacy skin API is disabled.", http.StatusNotFound)
		return
	}

	username := strings.TrimSuffix(c.Param("username"), ".png")
	if username == "" {
		sendYggdrasilError(c, "BadRequestException", "Bad request.", http.StatusBadRequest)
		return
	}

	profile := yc.authService.GetProfileByName(username)
	if profile == nil {
		log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin username=%s result=no_profile status=%d", c.GetString("request_id"), username, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	textures, err := yc.textureService.GetTextures(profile.ID)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin username=%s profile=%s result=get_textures_failed status=%d", c.GetString("request_id"), username, profile.ID, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	skin, ok := textures["SKIN"]
	if !ok {
		log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin username=%s profile=%s result=no_skin status=%d", c.GetString("request_id"), username, profile.ID, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	// The hash is usually the last part of the URL in HRPAuth
	parts := strings.Split(skin.URL, "/")
	hash := parts[len(parts)-1]

	data, contentType, err := yc.textureService.GetTextureByHash(hash)
	if err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin username=%s hash=%s result=texture_missing status=%d", c.GetString("request_id"), username, hash, http.StatusNoContent)
		c.Status(http.StatusNoContent)
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=legacy_skin username=%s profile=%s hash=%s size=%d result=success status=%d", c.GetString("request_id"), username, profile.ID, hash, len(data), http.StatusOK)
	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

func parseYggdrasilBearerToken(authHeader string) string {
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return ""
}
