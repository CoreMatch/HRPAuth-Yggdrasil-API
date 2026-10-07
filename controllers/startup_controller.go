package controllers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqldriver "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database/migrations"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"

	"gopkg.in/yaml.v3"
)

type StartupController struct{}

const ConfigFileName = "config.yaml"
const schemaMigrationService = "Yggdrasil-API"

func NewStartupController() *StartupController {
	return &StartupController{}
}

func (sc *StartupController) InitializeConfig() error {
	configPath := filepath.Join(config.ConfigFileDir, config.ConfigFileName)
	log.Printf("Initializing configuration. Looking for config at: %s", configPath)

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Printf("Config file not found at %s, creating default config...", configPath)
		return sc.createDefaultConfig(configPath)
	}

	log.Printf("Config file found at %s", configPath)

	// Check config file version and migrate if necessary
	if err := sc.checkAndMigrateConfig(configPath); err != nil {
		return fmt.Errorf("failed to check/migrate config: %v", err)
	}

	return nil
}

func (sc *StartupController) buildDefaultConfig(publicKeyPath, privateKeyPath string) map[string]interface{} {
	return map[string]interface{}{
		"version": config.ConfigVersion,
		"site": map[string]interface{}{
			"name":           "HRPAuth",
			"implementation": "HRPAuth Yggdrasil API Provider",
			"version":        "1.0.0",
		},
		"server": map[string]interface{}{
			"port":        ":2770",
			"cors_origin": "",
		},
		"callback": map[string]interface{}{
			"url": "http://localhost:2770",
		},
		"microservice": map[string]interface{}{
			"name":        "HRPAuth-Yggdrasil-API",
			"ttl_seconds": 120,
			"relay_url":   "http://localhost:2770",
		},
		"database": map[string]interface{}{
			"host":         "127.0.0.1",
			"db_name":      "hrpa",
			"user":         "hrpa",
			"password":     "hrpa",
			"charset":      "utf8mb4",
			"table_prefix": "haygg_",
		},
		"redis": map[string]interface{}{
			"host":     "127.0.0.1",
			"port":     6379,
			"password": "",
			"db":       0,
			"prefix":   "hrpauth_ygg_",
		},
		"core_api": map[string]interface{}{
			"base_url":      "http://localhost:2778",
			"internal_key":  sc.generateManageToken(),
			"client_id":     "",
			"client_secret": "",
		},
		"security": map[string]interface{}{
			"rate_limit_max_attempts": 10,
			"rate_limit_window_sec":   600,
		},
		"yggdrasil": map[string]interface{}{
			"server": map[string]interface{}{
				"name":                       "HRPAuth",
				"implementation":             "HRPAuth Yggdrasil API Provider",
				"version":                    "1.0.0",
				"signature_public_key_path":  publicKeyPath,
				"signature_private_key_path": privateKeyPath,
				"textures_storage":           "./storage",
				"links": map[string]interface{}{
					"homepage": "",
					"register": "",
				},
				"skin_domains": []string{},
			},
			"security": map[string]interface{}{
				"token_expiry_days":      15,
				"session_expiry_seconds": 28800,
				"max_texture_width":      1024,
				"max_texture_height":     1024,
				"max_tokens_per_user":    10,
			},
			"feature_flags": map[string]interface{}{
				"non_email_login":             true,
				"legacy_skin_api":             true,
				"no_mojang_namespace":         false,
				"enable_mojang_anti_features": false,
				"enable_profile_key":          false,
				"username_check":              true,
				"enable_ip_check":             false,
			},
		},
	}
}

func (sc *StartupController) createDefaultConfig(path string) error {
	cfgDir := filepath.Dir(path)

	publicKeyPath := filepath.Join(cfgDir, "public_key.pem")
	privateKeyPath := filepath.Join(cfgDir, "private_key.pem")

	if err := sc.generateKeyPair(publicKeyPath, privateKeyPath); err != nil {
		log.Printf("Warning: Failed to generate RSA key pair: %v", err)
		log.Printf("Falling back to pseudo-random keys...")
		if err := sc.generatePseudoKeys(publicKeyPath, privateKeyPath); err != nil {
			log.Printf("Warning: Failed to generate pseudo keys: %v", err)
		}
	}

	defaultConfig := sc.buildDefaultConfig(publicKeyPath, privateKeyPath)

	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %v", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	log.Printf("Default config file created at %s", path)
	log.Printf("Key pair generated at %s and %s", publicKeyPath, privateKeyPath)
	log.Printf("Please edit the configuration file and restart the application")
	return nil
}

// checkAndMigrateConfig checks the config file's version and upgrades it step
// by step (chain migration, see config.MigrateConfig). The original file is
// backed up before any migration and is left untouched on failure.
func (sc *StartupController) checkAndMigrateConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %v", err)
	}

	var currentConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &currentConfig); err != nil {
		return fmt.Errorf("failed to parse config file: %v", err)
	}

	currentVersion, _ := currentConfig["version"].(string)
	if currentVersion == config.ConfigVersion {
		log.Printf("Config file version %s is up-to-date", currentVersion)
		return nil
	}

	// A config file newer than the program supports must not be rewritten:
	// an older program cannot safely migrate a newer schema.
	if config.VersionMajor(currentVersion) > config.VersionMajor(config.ConfigVersion) {
		log.Printf("Warning: config file version %q is newer than supported version %q, continuing without migration",
			currentVersion, config.ConfigVersion)
		return nil
	}

	log.Printf("Config file version %q is older than %q, migrating...", currentVersion, config.ConfigVersion)

	// Backup the original file before any migration.
	if err := config.BackupConfigFile(path, currentVersion); err != nil {
		return fmt.Errorf("failed to backup config file: %v", err)
	}

	migrated, changed, err := config.MigrateConfig(currentConfig, sc.generateManageToken)
	if err != nil {
		return fmt.Errorf("failed to migrate config: %v", err)
	}
	if !changed {
		return nil
	}

	// Preserve existing key paths if present; otherwise generate a new key pair
	// and record the paths in the migrated config.
	cfgDir := filepath.Dir(path)
	publicKeyPath := filepath.Join(cfgDir, "public_key.pem")
	privateKeyPath := filepath.Join(cfgDir, "private_key.pem")

	existingPubPath, existingPrivPath := sc.getExistingKeyPaths(migrated)
	if existingPubPath != "" && existingPrivPath != "" {
		publicKeyPath = existingPubPath
		privateKeyPath = existingPrivPath
	} else {
		log.Printf("Signature key paths missing in config, generating new key pair...")
		if err := sc.generateKeyPair(publicKeyPath, privateKeyPath); err != nil {
			log.Printf("Warning: Failed to generate RSA key pair: %v", err)
			log.Printf("Falling back to pseudo-random keys...")
			if err := sc.generatePseudoKeys(publicKeyPath, privateKeyPath); err != nil {
				log.Printf("Warning: Failed to generate pseudo keys: %v", err)
			}
		}
		if ygg, ok := migrated["yggdrasil"].(map[string]interface{}); ok {
			if serverCfg, ok := ygg["server"].(map[string]interface{}); ok {
				serverCfg["signature_public_key_path"] = publicKeyPath
				serverCfg["signature_private_key_path"] = privateKeyPath
			}
		}
	}

	data, err = yaml.Marshal(migrated)
	if err != nil {
		return fmt.Errorf("failed to marshal migrated config: %v", err)
	}

	// Atomic write: write a temp file first, then rename, so a crash never
	// leaves a half-written config.yaml behind.
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write migrated config: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace config file: %v", err)
	}

	log.Printf("Config file migrated to version %s at %s", config.ConfigVersion, path)
	return nil
}

// getExistingKeyPaths extracts the signature key paths from a raw config map.
func (sc *StartupController) getExistingKeyPaths(cfg map[string]interface{}) (string, string) {
	yggdrasil, _ := cfg["yggdrasil"].(map[string]interface{})
	serverCfg, _ := yggdrasil["server"].(map[string]interface{})
	pubPath, _ := serverCfg["signature_public_key_path"].(string)
	privPath, _ := serverCfg["signature_private_key_path"].(string)
	return pubPath, privPath
}

func (sc *StartupController) generateKeyPair(publicKeyPath, privateKeyPath string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate RSA private key: %v", err)
	}

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key file: %v", err)
	}

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %v", err)
	}
	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	if err := os.WriteFile(publicKeyPath, publicKeyPEM, 0644); err != nil {
		return fmt.Errorf("failed to write public key file: %v", err)
	}

	return nil
}

func (sc *StartupController) generatePseudoKeys(publicKeyPath, privateKeyPath string) error {
	publicPseudo := sc.generateRandomString(512)
	privatePseudo := sc.generateRandomString(1024)

	publicKeyContent := fmt.Sprintf("-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----\n", publicPseudo)
	privateKeyContent := fmt.Sprintf("-----BEGIN RSA PRIVATE KEY-----\n%s\n-----END RSA PRIVATE KEY-----\n", privatePseudo)

	if err := os.WriteFile(publicKeyPath, []byte(publicKeyContent), 0644); err != nil {
		return fmt.Errorf("failed to write pseudo public key file: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, []byte(privateKeyContent), 0600); err != nil {
		return fmt.Errorf("failed to write pseudo private key file: %v", err)
	}

	return nil
}

// generateManageToken produces a random 32-byte (64 hex chars) Manage Token.
// It is generated once at config-file creation time and persisted to
// config.yaml under `manage.token`.
func (sc *StartupController) generateManageToken() string {
	return utils.GenerateRandomToken(32)
}

func (sc *StartupController) generateRandomString(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[i%len(charset)]
	}
	return string(b)
}

type prefixedSource struct {
	source.Driver
	prefix string
}

func (s *prefixedSource) ReadUp(version uint) (r io.ReadCloser, identifier string, err error) {
	r, identifier, err = s.Driver.ReadUp(version)
	if err != nil {
		return nil, identifier, err
	}
	return s.wrapReader(r), identifier, nil
}

func (s *prefixedSource) ReadDown(version uint) (r io.ReadCloser, identifier string, err error) {
	r, identifier, err = s.Driver.ReadDown(version)
	if err != nil {
		return nil, identifier, err
	}
	return s.wrapReader(r), identifier, nil
}

func (s *prefixedSource) wrapReader(r io.ReadCloser) io.ReadCloser {
	if s.prefix == "" {
		return r
	}
	content, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return nil
	}

	tables := []string{
		"accounts", "profiles", "profile_properties", "sessions", "tokens", "profile_keys",
		"texture_list_skin", "texture_list_cape",
	}

	sContent := string(content)
	for _, table := range tables {
		// Replace `table` with `prefix_table`
		sContent = strings.ReplaceAll(sContent, "`"+table+"`", "`"+s.prefix+table+"`")
		// Also handle foreign key references that might be like REFERENCES accounts (id)
		sContent = strings.ReplaceAll(sContent, "REFERENCES `"+table+"`", "REFERENCES `"+s.prefix+table+"`")
	}

	return io.NopCloser(strings.NewReader(sContent))
}

// EnsureMigrations runs all pending database migrations via golang-migrate.
// It is idempotent — if the database is already at the latest version,
// migrate.ErrNoChange is silently ignored.
func (sc *StartupController) EnsureMigrations() error {
	cfg := config.AppConfig.Database
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s)/%s?charset=%s&parseTime=True&loc=Local&multiStatements=true",
		cfg.User, cfg.Password, cfg.Host, cfg.DBName, cfg.Charset,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database for migration check: %v", err)
	}
	defer db.Close()

	if bootstrapErr := sc.ensureSchemaMigrationTable(db, cfg.TablePrefix); bootstrapErr != nil {
		return bootstrapErr
	}

	driver, err := mysqldriver.WithInstance(db, &mysqldriver.Config{
		MigrationsTable: cfg.TablePrefix + "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %v", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to create migration source from embedded files: %v", err)
	}

	// Wrap the source to handle table prefixes
	wrappedSrc := &prefixedSource{
		Driver: src,
		prefix: cfg.TablePrefix,
	}

	m, err := migrate.NewWithInstance("prefixed-iofs", wrappedSrc, cfg.DBName, driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %v", err)
	}
	defer func() {
		_, dbErr := m.Close()
		if dbErr != nil {
			log.Printf("warning: migration close error: %v", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		var dirtyErr migrate.ErrDirty
		if errors.As(err, &dirtyErr) {
			return fmt.Errorf("failed to run migrations: database is dirty at version %d; fix the migration state and force version before restarting", dirtyErr.Version)
		}
		return fmt.Errorf("failed to run migrations: %v", err)
	}

	if err := sc.ensureSchemaMigrationServiceColumn(db, cfg.TablePrefix); err != nil {
		return err
	}

	version, dirty, _ := m.Version()
	log.Printf("Database migration completed at version %d (dirty: %t)", version, dirty)
	return nil
}

func (sc *StartupController) ensureSchemaMigrationTable(db *sql.DB, prefix string) error {
	query := "CREATE TABLE IF NOT EXISTS `" + prefix + "schema_migrations` (" +
		"`version` bigint NOT NULL," +
		"`dirty` boolean NOT NULL," +
		"`service` varchar(16) NOT NULL DEFAULT '" + schemaMigrationService + "'," +
		"PRIMARY KEY (`service`)" +
		")"
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %v", err)
	}
	return nil
}

func (sc *StartupController) ensureSchemaMigrationServiceColumn(db *sql.DB, prefix string) error {
	tableName := prefix + "schema_migrations"
	tableExistsQuery := `
			SELECT COUNT(*)
			FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE()
				AND TABLE_NAME = ?
	`
	columnExistsQuery := `
			SELECT COUNT(*)
			FROM information_schema.COLUMNS
			WHERE TABLE_SCHEMA = DATABASE()
				AND TABLE_NAME = ?
				AND COLUMN_NAME = 'service'
	`

	var tableCount int
	if err := db.QueryRow(tableExistsQuery, tableName).Scan(&tableCount); err != nil {
		return fmt.Errorf("failed to check schema_migrations table: %v", err)
	}
	if tableCount == 0 {
		return nil
	}

	var columnCount int
	if err := db.QueryRow(columnExistsQuery, tableName).Scan(&columnCount); err != nil {
		return fmt.Errorf("failed to check schema_migrations service column: %v", err)
	}

	if columnCount == 0 {
		query := "ALTER TABLE `" + tableName + "` ADD COLUMN `service` varchar(16) NOT NULL DEFAULT '" + schemaMigrationService + "' AFTER `dirty`"
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to add schema_migrations service column: %v", err)
		}
	}

	// Backfill any rows that don't have a service value.
	if _, err := db.Exec("UPDATE `" + tableName + "` SET `service` = '" + schemaMigrationService + "' WHERE `service` IS NULL OR `service` = ''"); err != nil {
		return fmt.Errorf("failed to backfill schema_migrations service: %v", err)
	}

	// Keep the real table shape: PRIMARY KEY (`service`) only.
	pkQuery := `
		SELECT COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = ?
			AND CONSTRAINT_NAME = 'PRIMARY'
		ORDER BY ORDINAL_POSITION
	`
	rows, err := db.Query(pkQuery, tableName)
	if err != nil {
		return fmt.Errorf("failed to check schema_migrations primary key: %v", err)
	}
	defer rows.Close()

	var pkColumns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return fmt.Errorf("failed to read schema_migrations primary key: %v", err)
		}
		pkColumns = append(pkColumns, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate schema_migrations primary key: %v", err)
	}

	if len(pkColumns) == 1 && pkColumns[0] == "service" {
		return nil
	}
	if len(pkColumns) > 0 {
		if _, err := db.Exec("ALTER TABLE `" + tableName + "` DROP PRIMARY KEY"); err != nil {
			return fmt.Errorf("failed to drop schema_migrations primary key: %v", err)
		}
	}
	if _, err := db.Exec("ALTER TABLE `" + tableName + "` ADD PRIMARY KEY (`service`)"); err != nil {
		return fmt.Errorf("failed to add schema_migrations primary key: %v", err)
	}

	return nil
}

func (sc *StartupController) FetchMetadata() error {
	coreAPIClient := utils.GetCoreAPIClient()
	if coreAPIClient == nil {
		log.Println("Metadata fetch skipped: CoreAPI client not configured")
		return nil
	}

	client, err := coreAPIClient.GetClient(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get CoreAPI client: %v", err)
	}

	var meta clients.CoreMetadata
	if err := sc.doJSONRequest(client, "GET", config.AppConfig.CoreAPI.BaseURL+"/", nil, &meta); err != nil {
		return err
	}

	config.AppConfig.Runtime.SiteURL = meta.Site.URL
	config.AppConfig.Runtime.FrontendURL = meta.Yggdrasil.Meta.Links.Homepage
	log.Printf("Fetched runtime config: SiteURL=%s, FrontendURL=%s", config.AppConfig.Runtime.SiteURL, config.AppConfig.Runtime.FrontendURL)
	return nil
}

func (sc *StartupController) RegisterService() error {
	cfg := config.AppConfig.Microservice
	if cfg.Name == "" {
		log.Println("Microservice registration skipped: name not configured")
		return nil
	}

	coreAPIClient := utils.GetCoreAPIClient()
	if coreAPIClient == nil {
		log.Println("Microservice registration skipped: CoreAPI client not configured")
		return nil
	}

	client, err := coreAPIClient.GetClient(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get CoreAPI client: %v", err)
	}

	// 1. Register Presence
	presenceReq := clients.PresenceRequest{
		Name:          cfg.Name,
		TTLSeconds:    cfg.TTLSeconds,
		SecurityLevel: 2, // Level 2 for service registration
	}

	if err := sc.doJSONRequest(client, "POST", config.AppConfig.CoreAPI.BaseURL+"/services/presence", presenceReq, nil); err != nil {
		return fmt.Errorf("failed to register presence: %v", err)
	}
	log.Printf("Microservice presence registered: %s", cfg.Name)

	// 2. Register Relay Rules
	base := strings.TrimRight(cfg.RelayURL, "/")
	relays := []clients.RelayRule{
		{Dest: "/authserver", Source: base + "/authserver"},
		{Dest: "/sessionserver", Source: base + "/sessionserver"},
		{Dest: "/api/profiles/minecraft", Source: base + "/api/profiles/minecraft"},
		{Dest: "/textures", Source: base + "/textures"},
		{Dest: "/previews", Source: base + "/previews"},
		{Dest: "/texture", Source: base + "/texture"},
		{Dest: "/skin", Source: base + "/skin"},
		{Dest: "/skins", Source: base + "/skins"},
		{Dest: "/minecraftservices", Source: base + "/minecraftservices"},
		{Dest: "/register", Source: base + "/register"},
	}

	relayReq := clients.RelayRequest{
		Name:   cfg.Name,
		Relays: relays,
	}
	if err := sc.doJSONRequest(client, "POST", config.AppConfig.CoreAPI.BaseURL+"/services/relay", relayReq, nil); err != nil {
		return fmt.Errorf("failed to register relay rules: %v", err)
	}
	log.Printf("Microservice relay rules registered for %d paths", len(relays))

	return nil
}

func (sc *StartupController) doJSONRequest(client *http.Client, method, url string, body, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}

	return nil
}
