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

func (ic *InternalController) Heartbeat(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.Runtime.InternalKey == "" || internalKey != config.AppConfig.Runtime.InternalKey {
		respondError(c, http.StatusUnauthorized, CodeInvalidCredentials, "invalid internal key")
		return
	}
	respondOK(c, "I am alive", nil)
}

func (ic *InternalController) ProxyRegister(c *gin.Context) {
	internalKey := c.GetHeader("X-Internal-Key")
	if config.AppConfig.Runtime.InternalKey == "" || internalKey != config.AppConfig.Runtime.InternalKey {
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
