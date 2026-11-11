package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	reporedis "github.com/lnb/HRPAuth-Yggdrasil-API/redis"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newMCMock returns a Minecraft Services mock whose /minecraft/profile answer
// is controlled by the caller. It also points the Microsoft re-verification
// base at the mock for the duration of the test.
func newMCMock(t *testing.T, profileStatus int, profileBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/minecraft/profile":
			w.WriteHeader(profileStatus)
			if profileBody != "" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(profileBody))
			}
		case "/entitlements/mcstore":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	old := config.AppConfig.Yggdrasil.Microsoft
	config.AppConfig.Yggdrasil.Microsoft.MinecraftServicesBase = srv.URL
	config.AppConfig.Yggdrasil.Microsoft.TimeoutSeconds = 5
	t.Cleanup(func() { config.AppConfig.Yggdrasil.Microsoft = old })
	return srv
}

// setupMicrosoftEnv wires a fresh sqlite database (accounts/profiles/tokens),
// a dead redis client (rate limiting degrades to "not limited") and the
// microsoft routes, returning the gin engine.
func setupMicrosoftEnv(t *testing.T) *gin.Engine {
	t.Helper()
	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	config.AppConfig.Yggdrasil.Security.TokenExpiryDays = 15
	// Skin sync dispatch runs inline jobs in a goroutine by default; keep it
	// out of these tests.
	t.Cleanup(services.OverrideSkinSyncDispatchForTest(func(int, string, func()) {}))

	// Dead redis: IsLoginRateLimited treats any error as "not limited".
	// MaxRetries 0 keeps the dial-failure noise and latency down.
	reporedis.Client = goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: 0})

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "microsoft-test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			core_user_id varchar(32),
			mojang_uuid varchar(32),
			password varchar(255),
			mbe BOOLEAN NOT NULL DEFAULT 0,
			cbh BOOLEAN NOT NULL DEFAULT 1,
			skin_sync BOOLEAN NOT NULL DEFAULT 1,
			skin_sync_mark varchar(128),
			last_sign_at datetime,
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE TABLE profiles (
			id varchar(32) PRIMARY KEY,
			account_id INTEGER NOT NULL,
			name varchar(30) NOT NULL UNIQUE,
			model varchar(16) DEFAULT 'default',
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE TABLE tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			access_token varchar(255) UNIQUE,
			client_token varchar(255),
			account_id INTEGER,
			selected_profile_id varchar(32),
			issued_at bigint,
			expires_in_days INTEGER DEFAULT 15,
			state varchar(20) DEFAULT 'valid',
			created_at datetime
		)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create tables: %v", err)
		}
	}
	database.DB = db

	gin.SetMode(gin.TestMode)
	r := gin.New()
	mc := NewMicrosoftController()
	r.POST("/authserver/microsoft", mc.Exchange)
	r.POST("/player/microsoft-bind", mc.Bind)
	r.POST("/player/microsoft-unbind", mc.Unbind)
	return r
}

func doJSON(r *gin.Engine, path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var genuineUUID = "f7c77d999f154a66a87dc4a51ef30d19"

func TestMicrosoftExchangeAbsorbsNewIdentity(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"GenuinePlayer"}`)
	r := setupMicrosoftEnv(t)

	w := doJSON(r, "/authserver/microsoft", `{"mcAccessToken":"mc-token"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		AccessToken   string `json:"accessToken"`
		ClaimRequired bool   `json:"claimRequired"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.AccessToken == "" {
		t.Fatal("expected a non-empty accessToken")
	}
	if !resp.ClaimRequired {
		t.Fatal("freshly absorbed account must have claimRequired=true")
	}

	// The absorbed account exists, cbh=0, bound to the genuine uuid.
	var account models.Account
	if err := database.DB.Where("mojang_uuid = ?", genuineUUID).First(&account).Error; err != nil {
		t.Fatalf("absorbed account missing: %v", err)
	}
	if account.CBH {
		t.Fatal("absorbed account must be cbh=false (pending claim)")
	}
}

func TestMicrosoftExchangeReturnsExistingBoundAccount(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"BoundPlayer"}`)
	r := setupMicrosoftEnv(t)

	// Seed an already-bound, claimed account with a profile.
	account := models.Account{CBH: true, MojangUUID: &genuineUUID}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	profile := models.Profile{ID: "pbound", AccountID: account.ID, Name: "BoundPlayer", Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	w := doJSON(r, "/authserver/microsoft", `{"mcAccessToken":"mc-token"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		ClaimRequired bool `json:"claimRequired"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ClaimRequired {
		t.Fatal("claimed bound account must have claimRequired=false")
	}

	// No second account was created for the same uuid.
	var count int64
	database.DB.Model(&models.Account{}).Where("mojang_uuid = ?", genuineUUID).Count(&count)
	if count != 1 {
		t.Fatalf("accounts with uuid = %d, want exactly 1", count)
	}
}

func TestMicrosoftExchangeRejectsInvalidToken(t *testing.T) {
	newMCMock(t, http.StatusUnauthorized, "")
	r := setupMicrosoftEnv(t)

	w := doJSON(r, "/authserver/microsoft", `{"mcAccessToken":"bad"}`, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

func TestMicrosoftExchangeNameCollisionConflict(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"alex"}`)
	r := setupMicrosoftEnv(t)

	// Seed an HA account (mbe=0) that already owns the profile name "alex".
	account := models.Account{CBH: true}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := database.DB.Model(&account).UpdateColumn("cbh", true).Error; err != nil {
		t.Fatalf("seed cbh: %v", err)
	}
	profile := models.Profile{ID: "palex", AccountID: account.ID, Name: "alex", Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	w := doJSON(r, "/authserver/microsoft", `{"mcAccessToken":"mc-token"}`, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
}

// seedBearerToken inserts a valid local token for the account and returns it.
func seedBearerToken(t *testing.T, accountID int) string {
	t.Helper()
	token := models.Token{
		AccessToken:   "bearer-token-" + string(rune('a'+accountID%26)),
		ClientToken:   "client-token",
		AccountID:     accountID,
		IssuedAt:      9999999999999,
		ExpiresInDays: 15,
		State:         "valid",
	}
	if err := database.DB.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return token.AccessToken
}

func TestMicrosoftBindAndUnbind(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"GenuinePlayer"}`)
	r := setupMicrosoftEnv(t)

	// Seed a claimed HA account (no mojang_uuid yet) with a profile + token.
	account := models.Account{CBH: true}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := database.DB.Model(&account).UpdateColumn("cbh", true).Error; err != nil {
		t.Fatalf("seed cbh: %v", err)
	}
	profile := models.Profile{ID: "puser", AccountID: account.ID, Name: "hauser", Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	bearer := seedBearerToken(t, account.ID)

	// Bind.
	w := doJSON(r, "/player/microsoft-bind", `{"mcAccessToken":"mc-token"}`, bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("bind status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var reloaded models.Account
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != genuineUUID {
		t.Fatalf("mojang_uuid = %v, want bound", reloaded.MojangUUID)
	}

	// Unbind (idempotent clear).
	w = doJSON(r, "/player/microsoft-unbind", `{}`, bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("unbind status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID != nil {
		t.Fatalf("mojang_uuid after unbind = %v, want nil", *reloaded.MojangUUID)
	}

	// Unbind again still succeeds (idempotent).
	w = doJSON(r, "/player/microsoft-unbind", `{}`, bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("second unbind status = %d, want 200", w.Code)
	}
}

func TestMicrosoftBindRejectsDifferentBoundUUID(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"GenuinePlayer"}`)
	r := setupMicrosoftEnv(t)

	// Caller already bound to a DIFFERENT uuid.
	other := "11111111111111111111111111111111"
	account := models.Account{CBH: true, MojangUUID: &other}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	profile := models.Profile{ID: "puser", AccountID: account.ID, Name: "hauser", Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	bearer := seedBearerToken(t, account.ID)

	w := doJSON(r, "/player/microsoft-bind", `{"mcAccessToken":"mc-token"}`, bearer)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if code := envelopeCode(t, w); code != CodeMojangUUIDAlreadyBound {
		t.Fatalf("code = %q, want %q", code, CodeMojangUUIDAlreadyBound)
	}
}

func TestMicrosoftBindRequiresBearer(t *testing.T) {
	newMCMock(t, http.StatusOK, `{"id":"`+genuineUUID+`","name":"GenuinePlayer"}`)
	r := setupMicrosoftEnv(t)

	w := doJSON(r, "/player/microsoft-bind", `{"mcAccessToken":"mc-token"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
