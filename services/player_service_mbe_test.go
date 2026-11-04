package services

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupPlayerTestDB mirrors setupAdminTestDB but adds the tokens table so
// ResolveAccount can validate a local Yggdrasil token without touching the
// core.
func setupPlayerTestDB(t *testing.T) {
	t.Helper()
	resetMBEPendingForTest()
	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "player-test.db")), &gorm.Config{})
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
}

// seedPlayerAccountWithToken seeds an account, its profile and a valid local
// token, returning the bearer string.
func seedPlayerAccountWithToken(t *testing.T, name string, mojangUUID string) (int, string) {
	t.Helper()
	var uuidPtr *string
	if mojangUUID != "" {
		uuidPtr = &mojangUUID
	}
	account := models.Account{MojangUUID: uuidPtr, CBH: true}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	profile := models.Profile{ID: "p" + name, AccountID: account.ID, Name: name, Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	bearer := "tok-" + name
	token := models.Token{
		AccessToken:   bearer,
		AccountID:     account.ID,
		IssuedAt:      utils.CurrentTimestampMillis(),
		ExpiresInDays: 15,
		State:         "valid",
	}
	if err := database.DB.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return account.ID, bearer
}

// TestEnableMojangBindOpensWindow covers the enable branch of the mbe state
// machine: mbe flips to true and a 15-minute revert is scheduled.
func TestEnableMojangBindOpensWindow(t *testing.T) {
	setupPlayerTestDB(t)
	ps := NewPlayerService()
	accountID, bearer := seedPlayerAccountWithToken(t, "alex", "")

	info, err := ps.EnableMojangBind(bearer)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !info.MBE {
		t.Fatal("player info must report mbe=true after enable")
	}

	var reloaded models.Account
	if err := database.DB.First(&reloaded, accountID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.MBE {
		t.Fatal("mbe must be true in the database after enable")
	}
	if MBETimeoutFor(accountID).IsZero() {
		t.Fatal("a pending revert must be scheduled after enable")
	}
}

// TestEnableMojangBindNoOpWhenAlreadyBound: an account that already carries a
// mojang_uuid cannot re-open the bind window; nothing is written.
func TestEnableMojangBindNoOpWhenAlreadyBound(t *testing.T) {
	setupPlayerTestDB(t)
	ps := NewPlayerService()
	accountID, bearer := seedPlayerAccountWithToken(t, "alex", "f7c77d999f154a66a87dc4a51ef30d19")

	info, err := ps.EnableMojangBind(bearer)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if info.MBE {
		t.Fatal("already-bound account must not report mbe=true")
	}

	var reloaded models.Account
	database.DB.First(&reloaded, accountID)
	if reloaded.MBE {
		t.Fatal("already-bound account must keep mbe=false")
	}
	if !MBETimeoutFor(accountID).IsZero() {
		t.Fatal("no revert may be scheduled for an already-bound account")
	}
}

// TestDisableMojangBindCancelsWindow covers the disable branch: mbe resets to
// false and the pending revert is cancelled.
func TestDisableMojangBindCancelsWindow(t *testing.T) {
	setupPlayerTestDB(t)
	ps := NewPlayerService()
	accountID, bearer := seedPlayerAccountWithToken(t, "alex", "")

	if _, err := ps.EnableMojangBind(bearer); err != nil {
		t.Fatalf("enable: %v", err)
	}
	info, err := ps.DisableMojangBind(bearer)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if info.MBE {
		t.Fatal("player info must report mbe=false after disable")
	}

	var reloaded models.Account
	database.DB.First(&reloaded, accountID)
	if reloaded.MBE {
		t.Fatal("mbe must be false in the database after disable")
	}
	if !MBETimeoutFor(accountID).IsZero() {
		t.Fatal("disable must cancel the pending revert")
	}
}

// TestResolveAccountRejectsUnknownBearer guards the auth error path.
func TestResolveAccountRejectsUnknownBearer(t *testing.T) {
	setupPlayerTestDB(t)
	ps := NewPlayerService()

	_, err := ps.ResolveAccount("no-such-token")
	if !errors.Is(err, ErrAccountNotBound) {
		t.Fatalf("err = %v, want ErrAccountNotBound", err)
	}
	_, err = ps.ResolveAccount("")
	if !errors.Is(err, ErrAccountNotBound) {
		t.Fatalf("err = %v, want ErrAccountNotBound for empty bearer", err)
	}
}

// TestResolveAccountRejectsExpiredToken guards the token-expiry branch of
// ValidateToken used by the mbe endpoints.
func TestResolveAccountRejectsExpiredToken(t *testing.T) {
	setupPlayerTestDB(t)
	ps := NewPlayerService()
	accountID, bearer := seedPlayerAccountWithToken(t, "alex", "")

	// Force the token into the past (issued 30 days ago, valid 15 days).
	if err := database.DB.Model(&models.Token{}).Where("account_id = ?", accountID).
		Update("issued_at", time.Now().Add(-30*24*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatalf("age token: %v", err)
	}

	_, err := ps.ResolveAccount(bearer)
	if !errors.Is(err, ErrAccountNotBound) {
		t.Fatalf("err = %v, want ErrAccountNotBound for expired token", err)
	}
}
