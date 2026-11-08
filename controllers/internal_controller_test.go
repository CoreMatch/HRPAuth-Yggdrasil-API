package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupInternalControllerEnv points database.DB at a fresh sqlite database,
// configures a fixed internal key, and returns a gin engine serving the
// /internal/proxy-register route.
func setupInternalControllerEnv(t *testing.T) *gin.Engine {
	t.Helper()
	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	config.AppConfig.Runtime.InternalKey = "test-internal-key"
	// Bind paths now schedule skin syncs; keep them out of unrelated tests.
	t.Cleanup(services.OverrideSkinSyncDispatchForTest(func(int, string, func()) {}))

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "internal-test.db")), &gorm.Config{})
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
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create tables: %v", err)
		}
	}
	database.DB = db

	gin.SetMode(gin.TestMode)
	r := gin.New()
	ic := NewInternalController()
	r.POST("/internal/proxy-register", ic.ProxyRegister)
	return r
}

func proxyRegisterRequest(r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/proxy-register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Internal-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func envelopeCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v (body=%s)", err, w.Body.String())
	}
	return env.Code
}

func TestProxyRegisterRejectsWrongInternalKey(t *testing.T) {
	r := setupInternalControllerEnv(t)

	w := proxyRegisterRequest(r, "wrong-key", `{"username":"alex","password":"pw123456","mojang_uuid":"f7c77d999f154a66a87dc4a51ef30d19"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if code := envelopeCode(t, w); code != CodeInvalidCredentials {
		t.Fatalf("code = %q, want %q", code, CodeInvalidCredentials)
	}

	w = proxyRegisterRequest(r, "", `{}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want 401", w.Code)
	}
}

func TestProxyRegisterRejectsInvalidBody(t *testing.T) {
	r := setupInternalControllerEnv(t)

	w := proxyRegisterRequest(r, "test-internal-key", `{not-json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if code := envelopeCode(t, w); code != CodeInvalidJSONBody {
		t.Fatalf("code = %q, want %q", code, CodeInvalidJSONBody)
	}

	w = proxyRegisterRequest(r, "test-internal-key", `{"username":"alex","password":"pw123456","mojang_uuid":"not-a-uuid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if code := envelopeCode(t, w); code != CodeInvalidMojangUUID {
		t.Fatalf("code = %q, want %q", code, CodeInvalidMojangUUID)
	}
}

func TestProxyRegisterCollisionRespondsConflict(t *testing.T) {
	r := setupInternalControllerEnv(t)

	// Seed an HA account (mbe=0) with profile "alex".
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

	w := proxyRegisterRequest(r, "test-internal-key", `{"username":"alex","password":"pw123456","mojang_uuid":"f7c77d999f154a66a87dc4a51ef30d19"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if code := envelopeCode(t, w); code != CodeUsernameAlreadyBound {
		t.Fatalf("code = %q, want %q", code, CodeUsernameAlreadyBound)
	}

	// The account must remain untouched.
	var reloaded models.Account
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID != nil {
		t.Fatalf("rejected collision must not bind mojang_uuid, got %v", *reloaded.MojangUUID)
	}
}

func TestProxyRegisterMBEBindOverHTTP(t *testing.T) {
	r := setupInternalControllerEnv(t)

	// Seed an HA account with an open mbe window.
	account := models.Account{CBH: true, MBE: true}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	profile := models.Profile{ID: "palex", AccountID: account.ID, Name: "alex", Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	w := proxyRegisterRequest(r, "test-internal-key", `{"username":"alex","password":"incoming","mojang_uuid":"f7c77d999f154a66a87dc4a51ef30d19"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var reloaded models.Account
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != "f7c77d999f154a66a87dc4a51ef30d19" {
		t.Fatalf("mojang_uuid = %v, want bound", reloaded.MojangUUID)
	}
	if reloaded.MBE {
		t.Fatal("mbe must be reset after the bind")
	}
	if !reloaded.CBH {
		t.Fatal("cbh must be preserved by the bind")
	}
}

func TestProxyRegisterCreatesProxyAccount(t *testing.T) {
	r := setupInternalControllerEnv(t)

	w := proxyRegisterRequest(r, "test-internal-key", `{"username":"newguy","password":"pw123456","mojang_uuid":"f7c77d999f154a66a87dc4a51ef30d19"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var account models.Account
	if err := database.DB.Where("mojang_uuid = ?", "f7c77d999f154a66a87dc4a51ef30d19").First(&account).Error; err != nil {
		t.Fatalf("created account missing: %v", err)
	}
	if account.CBH {
		t.Fatal("proxy-registered account must persist cbh=false")
	}
	if account.RegisterAt.IsZero() || account.RegisterAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("register_at not set sensibly: %v", account.RegisterAt)
	}
}
