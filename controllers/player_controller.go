package controllers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
)

// PlayerController 提供面向 SDK 的 Game Account (Mojang bind) 相关端点。
// 这些端点对应 HA-Contract/docs/api/openapi/hrpauth-yggdrasil-business.yaml。
type PlayerController struct {
	playerSvc  *services.PlayerService
	httpClient *http.Client
}

func NewPlayerController() *PlayerController {
	return &PlayerController{
		playerSvc:  services.NewPlayerService(),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// extractBearer 从 Authorization 头里取出 Bearer token 字符串。
func extractBearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if strings.HasPrefix(h, prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return strings.TrimSpace(h)
}

// --- /player 系列 ---

// Info GET /player — 返回当前 Bearer 对应 Game Account 的摘要 (uid/mbe/mojang_uuid/cbh)。
func (pc *PlayerController) Info(c *gin.Context) {
	bearer := extractBearer(c)
	info, err := pc.playerSvc.GetPlayerInfo(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}
	respondOK(c, "ok", info)
}

func (pc *PlayerController) GetGameProfile(c *gin.Context) {
	profile, err := pc.playerSvc.GetProfile(extractBearer(c))
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}
	respondOK(c, "ok", gin.H{"profile": profile})
}

type createGameProfileRequest struct {
	Mode     string `json:"mode" binding:"required"`
	UUID     string `json:"uuid"`
	Username string `json:"username"`
}

type mojangNameLookupResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (pc *PlayerController) CreateGameProfile(c *gin.Context) {
	var req createGameProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request body")
		return
	}

	bearer := extractBearer(c)
	existing, err := pc.playerSvc.GetProfile(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}
	if existing != nil {
		respondError(c, http.StatusConflict, CodeProfileAlreadyExists, "a profile has already been created for this account")
		return
	}

	profileID := ""
	premiumName := ""
	switch req.Mode {
	case "random":
		profileID = utils.GenerateUnsignedUUID()
		if profileID == "" {
			respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to generate profile uuid")
			return
		}
	case "custom":
		profileID = req.UUID
	case "mojang":
		if !isValidMojangUsername(req.Username) {
			respondError(c, http.StatusBadRequest, CodeUsernameInvalid, "invalid Mojang username")
			return
		}

		upstream, err := pc.httpClient.Get("https://api.mojang.com/users/profiles/minecraft/" + url.PathEscape(req.Username))
		if err != nil {
			respondError(c, http.StatusBadGateway, CodeInternalError, "failed to reach Mojang profile service")
			return
		}
		defer upstream.Body.Close()
		if upstream.StatusCode != http.StatusOK {
			if upstream.StatusCode == http.StatusNoContent || upstream.StatusCode == http.StatusNotFound {
				respondError(c, http.StatusBadRequest, CodeUserNotFound, "Mojang username was not found")
				return
			}
			respondError(c, http.StatusBadGateway, CodeInternalError, "Mojang profile service returned an error")
			return
		}

		var mojangProfile mojangNameLookupResponse
		if err := json.NewDecoder(io.LimitReader(upstream.Body, 1<<20)).Decode(&mojangProfile); err != nil {
			respondError(c, http.StatusBadGateway, CodeInternalError, "failed to decode Mojang profile")
			return
		}
		if mojangProfile.ID == "" || mojangProfile.Name == "" {
			respondError(c, http.StatusBadGateway, CodeInternalError, "Mojang profile response was incomplete")
			return
		}
		profileID = mojangProfile.ID
		premiumName = mojangProfile.Name
	default:
		respondError(c, http.StatusBadRequest, CodeInvalidRequest, "mode must be random, custom, or mojang")
		return
	}

	profile, err := pc.playerSvc.CreateProfile(bearer, profileID, premiumName)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrProfileAlreadyExists):
			respondError(c, http.StatusConflict, CodeProfileAlreadyExists, "a profile has already been created for this account")
		case errors.Is(err, services.ErrProfileUUIDInvalid):
			respondError(c, http.StatusBadRequest, CodeInvalidMojangUUID, "profile uuid must be 32 hexadecimal characters")
		case errors.Is(err, services.ErrProfileUUIDConflict):
			respondError(c, http.StatusConflict, CodeProfileUUIDConflict, "profile uuid is already in use")
		case errors.Is(err, services.ErrProfileNameConflict):
			respondError(c, http.StatusConflict, CodeProfileNameConflict, "profile name is already in use")
		case errors.Is(err, services.ErrProfileNameUnavailable):
			respondError(c, http.StatusBadGateway, CodeInternalError, "could not determine a profile name for this account")
		case errors.Is(err, services.ErrAccountNotBound):
			respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		default:
			log.Printf("[YGG-BIZ] request_id=%s action=create_profile result=failed err=%v", c.GetString("request_id"), err)
			respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to create profile")
		}
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=create_profile profile=%s/%s result=success", c.GetString("request_id"), profile.ID, profile.Name)
	respondCreated(c, "profile created", profile)
}

func isValidMojangUsername(username string) bool {
	if len(username) < 1 || len(username) > 16 {
		return false
	}
	for _, char := range username {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return true
}

// EnableMojangBind POST /player/mojang-bind-enable — mbe=1 并挂 15 分钟自动回收。
func (pc *PlayerController) EnableMojangBind(c *gin.Context) {
	bearer := extractBearer(c)
	info, err := pc.playerSvc.EnableMojangBind(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}
	log.Printf("[YGG-BIZ] request_id=%s action=mojang_bind_enable account=%d mbe=%t", c.GetString("request_id"), info.AccountID, info.MBE)
	respondOK(c, "Mojang bind enabled", info)
}

// DisableMojangBind POST /player/mojang-bind-disable — mbe=0。
func (pc *PlayerController) DisableMojangBind(c *gin.Context) {
	bearer := extractBearer(c)
	info, err := pc.playerSvc.DisableMojangBind(bearer)
	if err != nil {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid or missing bearer token")
		return
	}
	log.Printf("[YGG-BIZ] request_id=%s action=mojang_bind_disable account=%d mbe=%t", c.GetString("request_id"), info.AccountID, info.MBE)
	respondOK(c, "Mojang bind disabled", info)
}

// --- Mojang sessionserver / textures 代理 ---

// mojangProfileResponse 是 SDK fetchMojangProfile 期望的最小字段集合。
type mojangProfileResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	HasCape bool   `json:"has_cape"`
}

// mojangSessionserverEntry 镜像 sessionserver.mojang.com 返回的结构。
type mojangSessionserverEntry struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Properties []mojangProperty `json:"properties"`
}

type mojangProperty struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Signature string `json:"signature"`
}

// mojangTexturesPayload 解码自 properties[].value (base64)。
type mojangTexturesPayload struct {
	Textures map[string]struct {
		URL string `json:"url"`
	} `json:"textures"`
}

// Profile GET /mojang/profile/:uuid — 代理 sessionserver.mojang.com 并解析出 has_cape。
func (pc *PlayerController) Profile(c *gin.Context) {
	uuid := utils.NormalizeMojangUUID(c.Param("uuid"))
	if !utils.IsValidMojangUUID(uuid) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "invalid mojang uuid",
			"code":    CodeInvalidMojangUUID,
		})
		return
	}

	upstream, err := pc.httpClient.Get("https://sessionserver.mojang.com/session/minecraft/profile/" + uuid)
	if err != nil {
		log.Printf("[YGG-MOJANG] request_id=%s action=profile uuid=%s result=upstream_err err=%v", c.GetString("request_id"), uuid, err)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "failed to reach mojang sessionserver",
			"code":    CodeInternalError,
		})
		return
	}
	defer upstream.Body.Close()

	body, _ := io.ReadAll(upstream.Body)
	if upstream.StatusCode != http.StatusOK {
		log.Printf("[YGG-MOJANG] request_id=%s action=profile uuid=%s result=upstream_status status=%d", c.GetString("request_id"), uuid, upstream.StatusCode)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "mojang profile not found",
			"code":    CodeUserNotFound,
		})
		return
	}

	var entry mojangSessionserverEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to decode mojang profile",
			"code":    CodeInternalError,
		})
		return
	}

	hasCape := false
	for _, prop := range entry.Properties {
		if prop.Name != "textures" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(prop.Value)
		if err != nil {
			break
		}
		var payload mojangTexturesPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			break
		}
		if _, ok := payload.Textures["CAPE"]; ok {
			hasCape = true
		}
		break
	}

	c.JSON(http.StatusOK, mojangProfileResponse{
		Success: true,
		ID:      entry.ID,
		Name:    entry.Name,
		HasCape: hasCape,
	})
}

// Texture GET /texture/mojang/:uuid?type=skin|cape — 302 重定向到 textures.minecraft.net。
// SkinViewer3D 直接 <img> 加载这个 URL，避免在浏览器里 cross-origin fetch。
func (pc *PlayerController) Texture(c *gin.Context) {
	uuid := utils.NormalizeMojangUUID(c.Param("uuid"))
	if !utils.IsValidMojangUUID(uuid) {
		respondError(c, http.StatusBadRequest, CodeInvalidMojangUUID, "invalid mojang uuid")
		return
	}
	textureType := strings.ToLower(c.Query("type"))
	if textureType == "" {
		textureType = "skin"
	}
	if textureType != "skin" && textureType != "cape" {
		respondError(c, http.StatusBadRequest, CodeTextureTypeInvalid, "type must be 'skin' or 'cape'")
		return
	}

	upstream, err := pc.httpClient.Get("https://sessionserver.mojang.com/session/minecraft/profile/" + uuid)
	if err != nil || upstream.StatusCode != http.StatusOK {
		if upstream != nil {
			upstream.Body.Close()
		}
		log.Printf("[YGG-MOJANG] request_id=%s action=texture uuid=%s type=%s result=upstream_err err=%v", c.GetString("request_id"), uuid, textureType, err)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "failed to reach mojang sessionserver",
			"code":    CodeInternalError,
		})
		return
	}
	body, _ := io.ReadAll(upstream.Body)
	upstream.Body.Close()

	var entry mojangSessionserverEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, "failed to decode mojang profile")
		return
	}

	textureKey := "SKIN"
	if textureType == "cape" {
		textureKey = "CAPE"
	}

	for _, prop := range entry.Properties {
		if prop.Name != "textures" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(prop.Value)
		if err != nil {
			break
		}
		var payload mojangTexturesPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			break
		}
		info, ok := payload.Textures[textureKey]
		if !ok || info.URL == "" {
			respondError(c, http.StatusNotFound, CodeTextureFetchFailed, "texture not found for given type")
			return
		}
		// 302 让浏览器 / skinview3d 直接去 textures.minecraft.net 拉图片。
		c.Redirect(http.StatusFound, info.URL)
		return
	}

	respondError(c, http.StatusNotFound, CodeTextureFetchFailed, "no textures property on profile")
}
