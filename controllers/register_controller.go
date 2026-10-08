package controllers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type RegisterController struct {
	authService *services.AuthService
}

func NewRegisterController() *RegisterController {
	return &RegisterController{
		authService: services.NewAuthService(),
	}
}

type RegisterGameAccountRequest struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
	MojangUUID string `json:"mojang_uuid"`
}

// Register handles POST /register to create a game account for a core user.
func (rc *RegisterController) Register(c *gin.Context) {
	var req RegisterGameAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[YGG-BIZ] request_id=%s action=register result=invalid_json status=%d", c.GetString("request_id"), http.StatusBadRequest)
		respondError(c, http.StatusBadRequest, CodeInvalidJSONBody, "Invalid request body.")
		return
	}

	if rc.authService.IsLoginRateLimited(req.Identifier) {
		log.Printf("[YGG-BIZ] request_id=%s action=register identifier=%s result=rate_limited status=%d", c.GetString("request_id"), req.Identifier, http.StatusTooManyRequests)
		respondError(c, http.StatusTooManyRequests, CodeInternalError, "Too many registration attempts. Please try again later.")
		return
	}

	account, profile, err := rc.authService.RegisterGameAccount(req.Identifier, req.Password, req.MojangUUID)
	if err != nil {
		rc.authService.RecordLoginAttempt(req.Identifier, false)
		log.Printf("[YGG-BIZ] request_id=%s action=register identifier=%s result=failed err=%v status=%d", c.GetString("request_id"), req.Identifier, err, http.StatusForbidden)
		respondError(c, http.StatusForbidden, CodeInvalidCredentials, err.Error())
		return
	}

	rc.authService.RecordLoginAttempt(req.Identifier, true)
	log.Printf("[YGG-BIZ] request_id=%s action=register identifier=%s account_id=%d profile=%s/%s result=success status=%d", c.GetString("request_id"), req.Identifier, account.ID, profile.ID, profile.Name, http.StatusOK)
	respondOK(c, "Game account registered successfully.", gin.H{
		"account_id": account.ID,
		"profile": gin.H{
			"id":   profile.ID,
			"name": profile.Name,
		},
	})
}
