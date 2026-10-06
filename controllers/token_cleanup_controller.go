package controllers

import (
	"log"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

type TokenCleanupController struct {
	authService *services.AuthService
}

func NewTokenCleanupController() *TokenCleanupController {
	return &TokenCleanupController{
		authService: services.NewAuthService(),
	}
}

func (tcc *TokenCleanupController) Start(interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	tcc.runOnce()
	go tcc.loop(interval)
}

func (tcc *TokenCleanupController) loop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		tcc.runOnce()
	}
}

func (tcc *TokenCleanupController) runOnce() {
	deleted := tcc.authService.CleanupExpiredTokens()
	if deleted > 0 {
		log.Printf("[TokenCleanup] removed %d expired/invalid tokens", deleted)
	}
}
