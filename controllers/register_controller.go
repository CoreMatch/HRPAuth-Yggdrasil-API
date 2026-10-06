package controllers

import (
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
		c.JSON(http.StatusBadRequest, gin.H{
			"error":        "InvalidRequest",
			"errorMessage": "Invalid request body.",
		})
		return
	}

	if rc.authService.IsLoginRateLimited(req.Identifier) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":        "TooManyRequests",
			"errorMessage": "Too many registration attempts. Please try again later.",
		})
		return
	}

	account, profile, err := rc.authService.RegisterGameAccount(req.Identifier, req.Password, req.MojangUUID)
	if err != nil {
		rc.authService.RecordLoginAttempt(req.Identifier, false)
		c.JSON(http.StatusForbidden, gin.H{
			"error":        "RegistrationFailed",
			"errorMessage": err.Error(),
		})
		return
	}

	rc.authService.RecordLoginAttempt(req.Identifier, true)
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "Game account registered successfully.",
		"account_id": account.ID,
		"profile": gin.H{
			"id":   profile.ID,
			"name": profile.Name,
		},
	})
}
