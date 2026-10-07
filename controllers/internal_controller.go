package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type InternalController struct {
	authService *services.AuthService
}

func NewInternalController() *InternalController {
	return &InternalController{
		authService: services.NewAuthService(),
	}
}

func (ic *InternalController) SyncUsername(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.CoreAPI.InternalKey == "" || internalKey != config.AppConfig.CoreAPI.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}

	var req struct {
		CoreUserID  string `json:"core_user_id"`
		NewUsername string `json:"new_username"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request")
		return
	}

	if err := ic.authService.SyncUsername(req.CoreUserID, req.NewUsername); err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	respondOK(c, "Username synced successfully", nil)
}

func (ic *InternalController) ProxyRegister(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.CoreAPI.InternalKey == "" || internalKey != config.AppConfig.CoreAPI.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}

	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		MojangUUID string `json:"mojang_uuid"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request")
		return
	}

	account, profile, err := ic.authService.ProxyRegister(req.Username, req.Password, req.MojangUUID)
	if err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	respondOK(c, "Proxy account registered", gin.H{
		"account_id": account.ID,
		"profile_id": profile.ID,
	})
}

func (ic *InternalController) ClaimAccount(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.CoreAPI.InternalKey == "" || internalKey != config.AppConfig.CoreAPI.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}

	var req struct {
		MojangUUID string `json:"mojang_uuid"`
		CoreUserID string `json:"core_user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request")
		return
	}

	if err := ic.authService.ClaimAccount(req.MojangUUID, req.CoreUserID); err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	respondOK(c, "Account claimed successfully", nil)
}

func (ic *InternalController) DeleteAccount(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.CoreAPI.InternalKey == "" || internalKey != config.AppConfig.CoreAPI.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}

	var req struct {
		CoreUserID string `json:"core_user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request")
		return
	}

	if err := ic.authService.DeleteAccount(req.CoreUserID); err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	respondOK(c, "Account deleted successfully", nil)
}

func (ic *InternalController) InvalidateTokens(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.CoreAPI.InternalKey == "" || internalKey != config.AppConfig.CoreAPI.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}

	var req struct {
		CoreUserID string `json:"core_user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "invalid request")
		return
	}

	if err := ic.authService.InvalidateCoreUserTokens(req.CoreUserID); err != nil {
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	respondOK(c, "Tokens invalidated successfully", nil)
}
