package main

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/controllers"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/redis"
)

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := config.AppConfig.Server.CORSOrigin
		if origin == "" {
			origin = strings.TrimRight(config.AppConfig.Frontend.URL, "/")
		} else if origin == "*" {
			reqOrigin := c.Request.Header.Get("Origin")
			if reqOrigin != "" {
				origin = reqOrigin
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func main() {
	config.Load()
	database.Init()

	// Initial migrations for Yggdrasil-specific tables
	startupCtrl := controllers.NewStartupController()
	if err := startupCtrl.EnsureMigrations(); err != nil {
		log.Fatalf("Failed to ensure database migrations: %v", err)
	}

	redis.Init()

	// Background cleanup tasks
	tokenCleanupCtrl := controllers.NewTokenCleanupController()
	tokenCleanupCtrl.Start(1 * time.Hour)

	sessionCleanupCtrl := controllers.NewSessionCleanupController()
	sessionCleanupCtrl.Start(24 * time.Hour)

	botCleanupCtrl := controllers.NewBotUserCleanupController()
	botCleanupCtrl.Start(24 * time.Hour)

	r := gin.Default()

	r.Use(CORSMiddleware())

	yggdrasilCtrl := controllers.NewYggdrasilController()
	registerCtrl := controllers.NewRegisterController()
	internalCtrl := controllers.NewInternalController()

	r.GET("/", yggdrasilCtrl.Meta)
	r.POST("/register", registerCtrl.Register)
	r.POST("/internal/sync-username", internalCtrl.SyncUsername)
	r.POST("/internal/proxy-register", internalCtrl.ProxyRegister)
	r.POST("/internal/claim-account", internalCtrl.ClaimAccount)
	r.POST("/internal/delete-account", internalCtrl.DeleteAccount)
        r.POST("/internal/invalidate-tokens", internalCtrl.InvalidateTokens)

	auth := r.Group("/authserver")
	{
		auth.POST("/authenticate", yggdrasilCtrl.Authenticate)
		auth.POST("/refresh", yggdrasilCtrl.Refresh)
		auth.POST("/validate", yggdrasilCtrl.Validate)
		auth.POST("/invalidate", yggdrasilCtrl.Invalidate)
		auth.POST("/signout", yggdrasilCtrl.Signout)
	}

	session := r.Group("/sessionserver/session/minecraft")
	{
		session.POST("/join", yggdrasilCtrl.Join)
		session.GET("/hasJoined", yggdrasilCtrl.HasJoined)
		session.GET("/hasjoined", yggdrasilCtrl.HasJoined)
		session.GET("/profile/:uuid", yggdrasilCtrl.ProfileQuery)
	}

	api := r.Group("/api")
	{
		api.POST("/profiles/minecraft", yggdrasilCtrl.BatchProfiles)
		// Texture upload/delete might be delegated to asset service later
	}

	r.GET("/textures/:hash", yggdrasilCtrl.DownloadTexture)
	r.GET("/previews/:fileName", yggdrasilCtrl.DownloadPreview)
	r.POST("/texture/upload/:uuid", yggdrasilCtrl.UploadTexture)
	r.DELETE("/texture/delete/:uuid/:type", yggdrasilCtrl.DeleteTexture)
	r.GET("/skin/:username", yggdrasilCtrl.LegacySkin)
	r.GET("/skin/:username.png", yggdrasilCtrl.LegacySkin)
	r.GET("/skins/MinecraftSkins/:username", yggdrasilCtrl.LegacySkin)

	minecraftServices := r.Group("/minecraftservices")
	{
		minecraftServices.POST("/player/certificates", yggdrasilCtrl.PlayerCertificates)
		minecraftServices.GET("/publickeys", yggdrasilCtrl.PublicKeys)
	}

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":        "Not Found",
			"errorMessage": "The requested endpoint does not exist.",
			"cause":        nil,
		})
	})

	log.Printf("HRPAuth Yggdrasil API Provider listening on %s", config.AppConfig.Server.Port)
	r.Run(config.AppConfig.Server.Port)
}
