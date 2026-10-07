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

func (yc *YggdrasilController) Meta(c *gin.Context) {
	c.Header("X-Authlib-Injector-API-Location", "/")
	c.JSON(http.StatusOK, gin.H{
		"status":  "online",
		"success": true,
		"message": "HRPAuth Yggdrasil API Provider is running.",
		"site": gin.H{
			"name":        config.AppConfig.Site.Name,
			"url":         config.AppConfig.Callback.URL,
			"version":     config.AppConfig.Site.Version,
			"server_time": time.Now().Format("2006-01-02 15:04:05"),
		},
		"yggdrasil": yc.GetMetadata(),
	})
}

func (yc *YggdrasilController) GetMetadata() gin.H {
	cfg := config.AppConfig.Yggdrasil.Server
	frontendURL := config.AppConfig.Frontend.URL

	links := gin.H{
		"homepage": cfg.Links.Homepage,
		"register": cfg.Links.Register,
	}

	if links["homepage"] == "" {
		links["homepage"] = frontendURL
	}
	if links["register"] == "" {
		links["register"] = strings.TrimRight(frontendURL, "/") + "/register"
	}

	skinDomains := cfg.SkinDomains
	if len(skinDomains) == 0 {
		skinDomains = []string{
			utils.ExtractDomain(config.AppConfig.Callback.URL),
			"." + utils.ExtractDomain(config.AppConfig.Callback.URL),
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
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	if req.Username == "" || req.Password == "" {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	if yc.authService.IsLoginRateLimited(req.Username) {
		sendYggdrasilError(c, "ForbiddenOperationException", "Too many login attempts. Please try again later.", http.StatusForbidden)
		return
	}

	user, err := yc.authService.VerifyCredentials(req.Username, req.Password)
	if err != nil || user == nil {
		yc.authService.RecordLoginAttempt(req.Username, false)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	yc.authService.RecordLoginAttempt(req.Username, true)

	profiles := yc.authService.GetUserProfiles(user.AccountID)
	if len(profiles) == 0 {
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
			sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session. Please try again.", http.StatusForbidden)
			return
		}
	}

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
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateTokenForRefresh(req.AccessToken, req.ClientToken)
	if token == nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	profiles := yc.authService.GetUserProfiles(token.AccountID)
	if len(profiles) == 0 {
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

	c.JSON(http.StatusOK, response)
}

func (yc *YggdrasilController) Validate(c *gin.Context) {
	var req ValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateToken(req.AccessToken, req.ClientToken)
	if token == nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Invalidate(c *gin.Context) {
	var req InvalidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	yc.authService.InvalidateToken(req.AccessToken)
	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Signout(c *gin.Context) {
	var req SignoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	if yc.authService.IsLoginRateLimited(req.Username) {
		sendYggdrasilError(c, "ForbiddenOperationException", "Too many signout attempts. Please try again later.", http.StatusForbidden)
		return
	}

	user, err := yc.authService.VerifyCredentials(req.Username, req.Password)
	if err != nil || user == nil {
		yc.authService.RecordLoginAttempt(req.Username, false)
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid credentials.", http.StatusForbidden)
		return
	}

	yc.authService.RecordLoginAttempt(req.Username, true)
	yc.authService.InvalidateAllAccountTokens(user.AccountID)

	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) Join(c *gin.Context) {
	var req JoinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	token := yc.authService.ValidateToken(req.AccessToken, "")
	if token == nil {
		sendYggdrasilError(c, "ForbiddenOperationException", "Invalid token.", http.StatusForbidden)
		return
	}

	if req.SelectedProfile != token.SelectedProfileID {
		if !yc.authService.IsProfileOwnedByAccount(req.SelectedProfile, token.AccountID) {
			sendYggdrasilError(c, "ForbiddenOperationException", "Invalid profile.", http.StatusForbidden)
			return
		}
	}

	ip := c.ClientIP()
	if !yc.authService.CreateSession(req.SelectedProfile, req.ServerID, ip) {
		sendYggdrasilError(c, "ForbiddenOperationException", "Failed to create session.", http.StatusForbidden)
		return
	}

	c.Status(http.StatusNoContent)
}

func (yc *YggdrasilController) HasJoined(c *gin.Context) {
	username := c.Query("username")
	serverID := c.Query("serverId")
	ip := c.Query("ip")

	if username == "" || serverID == "" {
		sendYggdrasilError(c, "BadRequestException", "Bad request.", http.StatusBadRequest)
		return
	}

	log.Printf("[HasJoined] username=%s, serverID=%s, ip=%s", username, serverID, ip)

	profile := yc.authService.GetProfileByName(username)
	if profile == nil {
		c.Status(http.StatusNoContent)
		return
	}

	session := yc.authService.GetSessionByProfileAndServer(username, serverID)
	if session == nil {
		c.Status(http.StatusNoContent)
		return
	}

	if ip != "" && config.AppConfig.Yggdrasil.FeatureFlags.EnableIPCheck && session.IP != ip {
		c.Status(http.StatusNoContent)
		return
	}

	properties, err := yc.textureService.GetProfileProperties(profile.ID, profile.Name, false)
	if err != nil {
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
}

func (yc *YggdrasilController) ProfileQuery(c *gin.Context) {
	uuid := c.Param("uuid")
	unsignedStr := c.DefaultQuery("unsigned", "true")
	unsigned := unsignedStr == "true"

	profile := yc.authService.GetProfileByID(uuid)
	if profile == nil {
		sendYggdrasilError(c, "ProfileNotFoundException", "No such profile.", http.StatusNotFound)
		return
	}

	properties, err := yc.textureService.GetProfileProperties(uuid, profile.Name, unsigned)
	if err != nil {
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
}

type BatchProfileRequest struct {
	Names []string `json:"names"`
}

func (yc *YggdrasilController) BatchProfiles(c *gin.Context) {
	var req BatchProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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

	respondOK(c, "Batch profile query successful", result)
}

func (yc *YggdrasilController) PlayerCertificates(c *gin.Context) {
	if !config.AppConfig.Yggdrasil.FeatureFlags.EnableProfileKey {
		sendYggdrasilError(c, "ForbiddenOperationException", "Profile key feature is disabled.", http.StatusForbidden)
		return
	}

	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
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
		sendYggdrasilError(c, "InternalException", "Failed to issue profile key.", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, pkService.BuildResponse(issued))
}

func (yc *YggdrasilController) PublicKeys(c *gin.Context) {
	pkService := services.NewProfileKeyService()
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
		respondError(c, http.StatusNotFound, CodeTargetNotFound, "Texture not found.")
		return
	}

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
		respondError(c, http.StatusNotFound, CodeTargetNotFound, "Preview not found.")
		return
	}

	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

func (yc *YggdrasilController) UploadTexture(c *gin.Context) {
	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
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
		respondError(c, http.StatusBadRequest, CodeTextureUploadFailed, err.Error())
		return
	}

	respondOK(c, "Texture uploaded successfully", gin.H{
		"warnings": warnings,
	})
}

func (yc *YggdrasilController) DeleteTexture(c *gin.Context) {
	accessToken := parseYggdrasilBearerToken(c.GetHeader("Authorization"))
	if accessToken == "" {
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "Unauthorized")
		return
	}

	profileID := c.Param("uuid")
	textureType := c.Param("type")

	token := yc.authService.ValidateToken(accessToken, "")
	if token == nil {
		respondError(c, http.StatusUnauthorized, CodeOAuthLoginRequired, "Unauthorized")
		return
	}

	if !yc.authService.IsProfileOwnedByAccount(profileID, token.AccountID) {
		respondError(c, http.StatusForbidden, CodeProfileAccessDenied, "Forbidden")
		return
	}

	// Logic to remove texture from profile property
	if err := yc.textureService.RemoveProfileTexture(profileID, textureType); err != nil {
		respondError(c, http.StatusInternalServerError, CodeTextureDeleteFailed, err.Error())
		return
	}

	respondOK(c, "Texture deleted successfully", nil)
}

func (yc *YggdrasilController) LegacySkin(c *gin.Context) {
	if !config.AppConfig.Yggdrasil.FeatureFlags.LegacySkinAPI {
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
		c.Status(http.StatusNoContent)
		return
	}

	textures, err := yc.textureService.GetTextures(profile.ID)
	if err != nil {
		c.Status(http.StatusNoContent)
		return
	}

	skin, ok := textures["SKIN"]
	if !ok {
		c.Status(http.StatusNoContent)
		return
	}

	// The hash is usually the last part of the URL in HRPAuth
	parts := strings.Split(skin.URL, "/")
	hash := parts[len(parts)-1]

	data, contentType, err := yc.textureService.GetTextureByHash(hash)
	if err != nil {
		c.Status(http.StatusNoContent)
		return
	}

	c.Header("Content-Type", contentType)
	c.Data(http.StatusOK, contentType, data)
}

func parseYggdrasilBearerToken(authHeader string) string {
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return ""
}
