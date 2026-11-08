package main

import (
	"embed"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/controllers"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/redis"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

//go:embed sdk/*
var sdkFS embed.FS

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := config.AppConfig.Server.CORSOrigin
		if origin == "" {
			origin = strings.TrimRight(config.AppConfig.Runtime.FrontendURL, "/")
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

// statusRecorder 捕获响应状态码，供访问日志使用。
type statusRecorder struct {
	gin.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	return sr.ResponseWriter.Write(b)
}

// AccessLogMiddleware 输出统一前缀 [YGG-HTTP] 的全局访问日志。
// 需要配合 controllers.RequestIDMiddleware 使用（先注册），仅记录请求元信息，
// 不读取请求体，避免日志中出现密码或凭据。
func AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: c.Writer}
		c.Writer = rec

		c.Next()

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("[YGG-HTTP] request_id=%s method=%s path=%s query=%s client_ip=%s status=%d duration_ms=%d",
			c.GetString("request_id"), c.Request.Method, c.Request.URL.Path, c.Request.URL.RawQuery, c.ClientIP(), status, time.Since(start).Milliseconds())
	}
}

func main() {
	// Initialize configuration (creates default if missing)
	startupCtrl := controllers.NewStartupController()
	if err := startupCtrl.InitializeConfig(); err != nil {
		log.Fatalf("Failed to initialize config: %v", err)
	}

	config.Load()

	// Startup credential resolution: when the configured core_api
	// credentials are HRPAuth's built-in super client, they are exchanged
	// for this service's own low-privilege client via POST /oauth/register
	// (RFC 7591 protected registration) and the super secret is discarded
	// from memory. Must run before any core_api client is constructed
	// (utils.GetCoreAPIClient captures the credentials on first use).
	resolveCoreCredentials()

	database.Init()

	// Initial migrations for Yggdrasil-specific tables
	if err := startupCtrl.EnsureMigrations(); err != nil {
		log.Fatalf("Failed to ensure database migrations: %v", err)
	}

	// Fetch metadata from Core before starting server
	if err := startupCtrl.FetchMetadata(); err != nil {
		log.Printf("Warning: Failed to fetch metadata from Core at startup: %v. CORS and some metadata may be incorrect until successfully fetched.", err)
	}

	redis.Init()

	// Background cleanup tasks
	tokenCleanupCtrl := controllers.NewTokenCleanupController()
	tokenCleanupCtrl.Start(1 * time.Hour)

	sessionCleanupCtrl := controllers.NewSessionCleanupController()
	sessionCleanupCtrl.Start(24 * time.Hour)

	botCleanupCtrl := controllers.NewBotUserCleanupController()
	botCleanupCtrl.Start(24 * time.Hour)

	mbeTimeoutCtrl := controllers.NewMBETimeoutCleanupController()
	mbeTimeoutCtrl.Start(30 * time.Second)
	mbeTimeoutCtrl.LogStarted()

	go func() {
		textureService := services.NewTextureService()
		textureService.CleanupOrphanedTextures()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			textureService.CleanupOrphanedTextures()
		}
	}()

	r := gin.Default()

	r.Use(controllers.RequestIDMiddleware())
	r.Use(CORSMiddleware())
	r.Use(AccessLogMiddleware())

	yggdrasilCtrl := controllers.NewYggdrasilController()
	registerCtrl := controllers.NewRegisterController()
	internalCtrl := controllers.NewInternalController()
	playerCtrl := controllers.NewPlayerController()
	adminCtrl := controllers.NewAdminController()

	r.GET("/", yggdrasilCtrl.Meta)
	// 专用存活探针：不查数据库、不做重活，供容器 healthcheck 使用。
	// 契约：HA-Contract docs/api/openapi/hrpauth-yggdrasil-business.yaml `/health`。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "ok",
			"data":    gin.H{"status": "ok"},
			"meta":    gin.H{"request_id": c.GetString("request_id")},
		})
	})
	r.POST("/register", registerCtrl.Register)
	r.POST("/internal/proxy-register", internalCtrl.ProxyRegister)
	r.GET("/internal/heartbeat", internalCtrl.Heartbeat)

	admin := r.Group("/admin")
	{
		admin.POST("/claim-user", adminCtrl.ClaimUser)
		admin.POST("/force-bind", adminCtrl.ForceBind)
	}

	player := r.Group("/player")
	{
		player.GET("", playerCtrl.Info)
		player.GET("/profile", playerCtrl.GetGameProfile)
		player.POST("/profile", playerCtrl.CreateGameProfile)
		player.POST("/mojang-bind-enable", playerCtrl.EnableMojangBind)
		player.POST("/mojang-bind-disable", playerCtrl.DisableMojangBind)
		player.POST("/skin-sync-enable", playerCtrl.EnableSkinSync)
		player.POST("/skin-sync-disable", playerCtrl.DisableSkinSync)
	}

	r.GET("/mojang/profile/:uuid", playerCtrl.Profile)
	r.GET("/texture/mojang/:uuid", playerCtrl.Texture)

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
	r.GET("/texture/avatar/:uuid", yggdrasilCtrl.DownloadSkinAvatar)
	r.POST("/texture/get", yggdrasilCtrl.GetTextures)
	r.POST("/texture/upload/:uuid", yggdrasilCtrl.UploadTexture)
	r.DELETE("/texture/delete/:uuid/:type", yggdrasilCtrl.DeleteTexture)
	r.GET("/skin/:username", yggdrasilCtrl.LegacySkin)
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

	// Register with HRPAuth core in background
	go func() {
		time.Sleep(2 * time.Second) // Wait for server to start
		if err := startupCtrl.RegisterService(); err != nil {
			log.Printf("Warning: Microservice registration failed: %v", err)
		}
		// Upload SDK package (imitating haskinproxy success)
		startupCtrl.UploadSDKPackage(sdkFS)
	}()

	log.Printf("HRPAuth Yggdrasil API Provider listening on %s", config.AppConfig.Server.Port)
	r.Run(config.AppConfig.Server.Port)
}

// resolveCoreCredentials implements the startup credential resolution for
// the core_api client credentials: when they turn out to be HRPAuth's
// built-in super client, they are exchanged for this service's own
// low-privilege client via POST /oauth/register (RFC 7591 protected
// registration) and the super secret is discarded from memory; credentials
// that are already a per-service client are verified and reused unchanged
// (idempotent path — no exchange, no write-back). Any failure aborts
// startup: super credentials must never reach business requests, and there
// is no fallback to running with unverified credentials.
func resolveCoreCredentials() {
	core := &config.AppConfig.CoreAPI
	if core.ClientID == "" || core.ClientSecret == "" {
		return // client not configured; core calls stay disabled as before
	}
	serviceName := config.AppConfig.Microservice.Name
	if serviceName == "" {
		log.Fatalf("credential resolution requires microservice.name (the presence-registered service name)")
	}
	baseURL := strings.TrimRight(core.BaseURL, "/")
	superID, superSecret := core.ClientID, core.ClientSecret

	// The register endpoint only accepts presence-registered service
	// names, so the bonjour handshake goes first. It is a plain request:
	// no token may be attached before the credentials are resolved (the
	// super secret is used for the exchange call only). The background
	// RegisterService re-upserts presence later with root_metadata.
	if err := clients.RegisterPresencePlain(baseURL, clients.PresenceRequest{
		Name:          serviceName,
		TTLSeconds:    config.AppConfig.Microservice.TTLSeconds,
		SecurityLevel: 0, // Level 0 to allow anonymous relay for Yggdrasil
		Scope: &clients.PresenceScope{
			Name:          "yggdrasil-api",
			FrontendAreas: []string{"webui-dash"},
		},
	}, nil); err != nil {
		log.Printf("Presence: bootstrap registration failed: %v (a super-credential exchange will be rejected until presence succeeds)", err)
	}

	res, err := clients.ResolveServiceCredentials(baseURL, serviceName, superID, superSecret, nil)
	if err != nil {
		log.Fatalf("credential resolution failed (refusing to start with unverified or super credentials): %v", err)
	}
	if res.Exchanged {
		log.Printf("credentials: super client detected and exchanged for per-service client %q; super credentials discarded from memory", res.ClientID)
		core.ClientID, core.ClientSecret = res.ClientID, res.ClientSecret
		applyResolvedCredentialsToConfigFile(superID, superSecret, res)
	} else {
		log.Printf("credentials: configured client %q confirmed as per-service client (no exchange needed)", core.ClientID)
	}
}

// applyResolvedCredentialsToConfigFile attempts the atomic config-file
// write-back after a successful exchange. Entrypoint-rendered deployments
// (CONFIG_TEMPLATE/RENDERED_CONFIG) are only warned about; a failed
// write-back is not fatal — the service keeps running with the in-memory
// credentials and the next startup exchanges again (the secret is stable
// server-side via HRPAuth get-or-create).
func applyResolvedCredentialsToConfigFile(superID, superSecret string, res *clients.Resolution) {
	if config.IsRenderedDeployment() {
		log.Printf("WARN: config file is entrypoint-rendered (CONFIG_TEMPLATE/RENDERED_CONFIG); per-service credentials were NOT written back — set core_api.client_id/client_secret on the template/environment side instead; this deployment will exchange again on every start")
		return
	}
	configPath := filepath.Join(config.ConfigFileDir, config.ConfigFileName)
	n, err := config.RewriteClientCredentials(configPath, superID, superSecret, res.ClientID, res.ClientSecret, []string{"core_api"})
	if err != nil {
		log.Printf("ERROR: config file could not be rewritten with the per-service credentials (the service keeps running with the in-memory credentials): %v — next startup will perform the exchange again", err)
		return
	}
	log.Printf("credentials: config file rewritten with per-service client %q (sections replaced=%d, previous content kept at %s.bak)", res.ClientID, n, configPath)
}
