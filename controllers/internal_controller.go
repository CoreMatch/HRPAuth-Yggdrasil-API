package controllers

import (
	"log"
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
		log.Printf("[YGG-BIZ] request_id=%s action=sync_username result=bad_internal_key status=%d", c.GetString("request_id"), http.StatusUnauthorized)
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
		log.Printf("[YGG-BIZ] request_id=%s action=sync_username core_user=%s new_username=%s result=failed err=%v status=%d", c.GetString("request_id"), req.CoreUserID, req.NewUsername, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=sync_username core_user=%s new_username=%s result=success status=%d", c.GetString("request_id"), req.CoreUserID, req.NewUsername, http.StatusOK)
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
		log.Printf("[YGG-BIZ] request_id=%s action=proxy_register username=%s result=failed err=%v status=%d", c.GetString("request_id"), req.Username, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=proxy_register username=%s account_id=%d profile=%s result=success status=%d", c.GetString("request_id"), req.Username, account.ID, profile.ID, http.StatusOK)
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
		log.Printf("[YGG-BIZ] request_id=%s action=claim_account mojang_uuid=%s core_user=%s result=failed err=%v status=%d", c.GetString("request_id"), req.MojangUUID, req.CoreUserID, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=claim_account mojang_uuid=%s core_user=%s result=success status=%d", c.GetString("request_id"), req.MojangUUID, req.CoreUserID, http.StatusOK)
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
		log.Printf("[YGG-BIZ] request_id=%s action=delete_account core_user=%s result=failed err=%v status=%d", c.GetString("request_id"), req.CoreUserID, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=delete_account core_user=%s result=success status=%d", c.GetString("request_id"), req.CoreUserID, http.StatusOK)
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
		log.Printf("[YGG-BIZ] request_id=%s action=invalidate_core_tokens core_user=%s result=failed err=%v status=%d", c.GetString("request_id"), req.CoreUserID, err, http.StatusInternalServerError)
		respondError(c, http.StatusInternalServerError, CodeInternalError, err.Error())
		return
	}

	log.Printf("[YGG-BIZ] request_id=%s action=invalidate_core_tokens core_user=%s result=success status=%d", c.GetString("request_id"), req.CoreUserID, http.StatusOK)
	respondOK(c, "Tokens invalidated successfully", nil)
}
