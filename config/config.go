package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

type Config struct {
	Version      string
	Site         SiteConfig
	Server       ServerRuntimeConfig
	Database     DatabaseConfig
	Redis        RedisConfig
	Yggdrasil    YggdrasilConfig
	CoreAPI      CoreAPIConfig
	Security     SecurityConfig
	Microservice MicroserviceConfig
	Callback     CallbackConfig

	// Runtime populated from Core API
	Runtime struct {
		SiteURL     string
		FrontendURL string
	}
}

type CallbackConfig struct {
	URL string
}

type MicroserviceConfig struct {
	Name       string
	TTLSeconds int
	RelayURL   string // The external URL of this service that HRPAuth can reach
}

type SecurityConfig struct {
	RateLimitMaxAttempts int
	RateLimitWindowSec   int
}

type CoreAPIConfig struct {
	BaseURL      string
	InternalKey  string
	ClientID     string
	ClientSecret string
}

type ServerRuntimeConfig struct {
	Port       string
	CORSOrigin string
}

type SiteConfig struct {
	Name           string
	Implementation string
	Version        string
}

type DatabaseConfig struct {
	Host        string
	DBName      string
	User        string
	Password    string
	Charset     string
	TablePrefix string
}

type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
	Prefix   string
}

type YggdrasilConfig struct {
	Server       ServerConfig
	Security     YggdrasilSecurityConfig
	FeatureFlags FeatureFlagsConfig
}

type ServerConfig struct {
	Name                    string
	Implementation          string
	Version                 string
	Links                   LinksConfig
	SkinDomains             []string
	SignaturePublicKeyPath  string
	SignaturePrivateKeyPath string
	SignaturePublicKey      string
	SignaturePrivateKey     string
	TexturesStorage         string
}

type LinksConfig struct {
	Homepage string
	Register string
}

type YggdrasilSecurityConfig struct {
	TokenExpiryDays      int
	SessionExpirySeconds int
	MaxTextureWidth      int
	MaxTextureHeight     int
	MaxTextureFileSize   int64
	MaxTokensPerUser     int
}

type FeatureFlagsConfig struct {
	NonEmailLogin            bool
	LegacySkinAPI            bool
	NoMojangNamespace        bool
	EnableMojangAntiFeatures bool
	EnableProfileKey         bool
	UsernameCheck            bool
	EnableIPCheck            bool
}

const ConfigFileName = "config.yaml"

var ConfigFileDir = "./"

const ConfigVersion = "1" // New baseline version

func init() {
	// Try to get the directory of the executable
	if exePath, err := os.Executable(); err == nil {
		// Resolve symlinks to get the actual binary location
		if resolvedPath, err := filepath.EvalSymlinks(exePath); err == nil {
			exePath = resolvedPath
		}
		dir := filepath.Dir(exePath)

		// Check if it's a temporary directory from 'go run'
		// 'go run' binaries are typically in a path containing 'go-build' and 'exe'
		// We only fallback to "./" if it looks like a temporary build artifact.
		isGoRun := strings.Contains(dir, "go-build") && (strings.Contains(dir, "/exe") || strings.Contains(dir, "\\exe"))

		if !isGoRun {
			if absDir, err := filepath.Abs(dir); err == nil {
				ConfigFileDir = absDir
			} else {
				ConfigFileDir = dir
			}
		}
	}
}

var AppConfig *Config

func Load() {
	configPath := filepath.Join(ConfigFileDir, ConfigFileName)

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read config file %s: %v", configPath, err)
	}

	// Parse YAML
	var yamlConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &yamlConfig); err != nil {
		log.Fatalf("Failed to parse config file: %v", err)
	}

	// Validate config version
	configVersion := getString(yamlConfig, "version")
	if configVersion == "" {
		log.Fatalf("Config file is missing version field")
	}
	if configVersion != ConfigVersion {
		log.Printf("Warning: Config file version %s does not match expected version %s", configVersion, ConfigVersion)
	}

	// Map YAML to Config struct
	AppConfig = &Config{
		Version:      getString(yamlConfig, "version"),
		Site:         parseSiteConfig(yamlConfig),
		Server:       parseServerRuntimeConfig(yamlConfig),
		Database:     parseDatabaseConfig(yamlConfig),
		Redis:        parseRedisConfig(yamlConfig),
		Yggdrasil:    parseYggdrasilConfig(yamlConfig),
		CoreAPI:      parseCoreAPIConfig(yamlConfig),
		Security:     parseSecurityConfig(yamlConfig),
		Microservice: parseMicroserviceConfig(yamlConfig),
		Callback:     parseCallbackConfig(yamlConfig),
	}

	log.Println("Configuration loaded successfully")
}

func parseCallbackConfig(config map[string]interface{}) CallbackConfig {
	callback, _ := config["callback"].(map[string]interface{})
	return CallbackConfig{
		URL: getString(callback, "url"),
	}
}

func parseSiteConfig(config map[string]interface{}) SiteConfig {
	site, _ := config["site"].(map[string]interface{})
	return SiteConfig{
		Name:           getString(site, "name"),
		Implementation: getString(site, "implementation"),
		Version:        getString(site, "version"),
	}
}

func parseServerRuntimeConfig(config map[string]interface{}) ServerRuntimeConfig {
	server, _ := config["server"].(map[string]interface{})
	return ServerRuntimeConfig{
		Port:       getString(server, "port"),
		CORSOrigin: getString(server, "cors_origin"),
	}
}

func parseDatabaseConfig(config map[string]interface{}) DatabaseConfig {
	db, _ := config["database"].(map[string]interface{})
	return DatabaseConfig{
		Host:        getString(db, "host"),
		DBName:      getString(db, "db_name"),
		User:        getString(db, "user"),
		Password:    getString(db, "password"),
		Charset:     getString(db, "charset"),
		TablePrefix: getString(db, "table_prefix"),
	}
}

func parseRedisConfig(config map[string]interface{}) RedisConfig {
	redis, _ := config["redis"].(map[string]interface{})
	return RedisConfig{
		Host:     getString(redis, "host"),
		Port:     getInt(redis, "port"),
		Password: getString(redis, "password"),
		DB:       getInt(redis, "db"),
		Prefix:   getString(redis, "prefix"),
	}
}

func parseCoreAPIConfig(config map[string]interface{}) CoreAPIConfig {
	core, _ := config["core_api"].(map[string]interface{})
	return CoreAPIConfig{
		BaseURL:      getString(core, "base_url"),
		InternalKey:  getString(core, "internal_key"),
		ClientID:     getString(core, "client_id"),
		ClientSecret: getString(core, "client_secret"),
	}
}

func parseYggdrasilConfig(config map[string]interface{}) YggdrasilConfig {
	yggdrasil, _ := config["yggdrasil"].(map[string]interface{})
	return YggdrasilConfig{
		Server:       parseServerConfig(yggdrasil),
		Security:     parseYggdrasilSecurityConfig(yggdrasil),
		FeatureFlags: parseFeatureFlagsConfig(yggdrasil),
	}
}

func parseServerConfig(config map[string]interface{}) ServerConfig {
	server, _ := config["server"].(map[string]interface{})
	links, _ := server["links"].(map[string]interface{})
	skinDomains := getStringSlice(server, "skin_domains")

	texturesStorage := getString(server, "textures_storage")
	if texturesStorage == "" {
		texturesStorage = "./"
	}

	publicKeyPath := getString(server, "signature_public_key_path")
	privateKeyPath := getString(server, "signature_private_key_path")

	var publicKey, privateKey string
	if publicKeyPath != "" {
		if data, err := os.ReadFile(publicKeyPath); err == nil {
			publicKey = string(data)
		}
	}
	if privateKeyPath != "" {
		if data, err := os.ReadFile(privateKeyPath); err == nil {
			privateKey = string(data)
		}
	}

	return ServerConfig{
		Name:                    getString(server, "name"),
		Implementation:          getString(server, "implementation"),
		Version:                 getString(server, "version"),
		SignaturePublicKeyPath:  publicKeyPath,
		SignaturePrivateKeyPath: privateKeyPath,
		SignaturePublicKey:      publicKey,
		SignaturePrivateKey:     privateKey,
		Links: LinksConfig{
			Homepage: getString(links, "homepage"),
			Register: getString(links, "register"),
		},
		SkinDomains:     skinDomains,
		TexturesStorage: texturesStorage,
	}
}

func parseYggdrasilSecurityConfig(yggdrasilConfig map[string]interface{}) YggdrasilSecurityConfig {
	security, _ := yggdrasilConfig["security"].(map[string]interface{})
	return YggdrasilSecurityConfig{
		TokenExpiryDays:      getInt(security, "token_expiry_days"),
		SessionExpirySeconds: getInt(security, "session_expiry_seconds"),
		MaxTextureWidth:      getInt(security, "max_texture_width"),
		MaxTextureHeight:     getInt(security, "max_texture_height"),
		MaxTextureFileSize:   int64(getInt(security, "max_texture_file_size")),
		MaxTokensPerUser:     getInt(security, "max_tokens_per_user"),
	}
}

func parseFeatureFlagsConfig(config map[string]interface{}) FeatureFlagsConfig {
	featureFlags, _ := config["feature_flags"].(map[string]interface{})
	return FeatureFlagsConfig{
		NonEmailLogin:            getBool(featureFlags, "non_email_login"),
		LegacySkinAPI:            getBool(featureFlags, "legacy_skin_api"),
		NoMojangNamespace:        getBool(featureFlags, "no_mojang_namespace"),
		EnableMojangAntiFeatures: getBool(featureFlags, "enable_mojang_anti_features"),
		EnableProfileKey:         getBool(featureFlags, "enable_profile_key"),
		UsernameCheck:            getBool(featureFlags, "username_check"),
		EnableIPCheck:            getBool(featureFlags, "enable_ip_check"),
	}
}

func getString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	if value, ok := m[key].(bool); ok {
		return value
	}
	return false
}

func parseSecurityConfig(config map[string]interface{}) SecurityConfig {
	security, _ := config["security"].(map[string]interface{})
	return SecurityConfig{
		RateLimitMaxAttempts: getInt(security, "rate_limit_max_attempts"),
		RateLimitWindowSec:   getInt(security, "rate_limit_window_sec"),
	}
}

func parseMicroserviceConfig(config map[string]interface{}) MicroserviceConfig {
	ms, _ := config["microservice"].(map[string]interface{})
	return MicroserviceConfig{
		Name:       getString(ms, "name"),
		TTLSeconds: getInt(ms, "ttl_seconds"),
		RelayURL:   getString(ms, "relay_url"),
	}
}

func getStringSlice(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	raw, exists := m[key]
	if !exists {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}
