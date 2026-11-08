package services

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupAdminTestDB points database.DB at a fresh sqlite database carrying the
// game-side tables. Note: sqlite here does not enforce the MySQL ON DELETE
// CASCADE foreign keys from database/migrations/000001_init.up.sql, so tests
// assert account-level effects only; profile/token cascade is a schema
// property of the production DDL.
func setupAdminTestDB(t *testing.T) {
	t.Helper()
	resetMBEPendingForTest()
	// Bind paths now schedule skin syncs; keep them out of unrelated tests.
	t.Cleanup(OverrideSkinSyncDispatchForTest(func(int, string, func()) {}))
	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "admin-test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// sqlite cannot express the MySQL enum columns via AutoMigrate, so the
	// two tables are created with plain DDL mirroring
	// database/migrations/000001_init.up.sql (minus the enum constraint).
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
}

func seedProxyAccount(t *testing.T, name, mojangUUID string) *models.Account {
	t.Helper()
	var uuidPtr *string
	if mojangUUID != "" {
		uuidPtr = &mojangUUID
	}
	account := models.Account{MojangUUID: uuidPtr, CBH: false}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// gorm omits zero-value fields with a column default on insert, mirror
	// the explicit write ProxyRegister performs.
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).UpdateColumn("cbh", false).Error; err != nil {
		t.Fatalf("seed cbh: %v", err)
	}
	profile := models.Profile{ID: "p" + name, AccountID: account.ID, Name: name, Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return &account
}

func TestFindAccountByProfileName(t *testing.T) {
	setupAdminTestDB(t)
	svc := NewAdminService()
	seedProxyAccount(t, "steve", "f7c77d999f154a66a87dc4a51ef30d19")

	account, profile, err := svc.FindAccountByProfileName("steve")
	if err != nil || account == nil || profile == nil {
		t.Fatalf("lookup failed: account=%v profile=%v err=%v", account, profile, err)
	}
	if account.CBH {
		t.Fatal("seeded proxy account must have cbh=false")
	}

	account, profile, err = svc.FindAccountByProfileName("nobody")
	if err != nil || account != nil || profile != nil {
		t.Fatalf("expected nil lookup, got account=%v profile=%v err=%v", account, profile, err)
	}
}

func TestClaimProxyAccountFlipsCBH(t *testing.T) {
	setupAdminTestDB(t)
	svc := NewAdminService()
	account := seedProxyAccount(t, "steve", "")

	if err := svc.ClaimProxyAccount(account.ID, "core-uuid-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	var claimed models.Account
	if err := database.DB.First(&claimed, account.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !claimed.CBH {
		t.Fatal("cbh must be true after claim")
	}
	if claimed.CoreUserID == nil || *claimed.CoreUserID != "core-uuid-1" {
		t.Fatalf("core_user_id = %v, want core-uuid-1", claimed.CoreUserID)
	}
}

func TestClaimProxyAccountRejectsAlreadyClaimed(t *testing.T) {
	setupAdminTestDB(t)
	svc := NewAdminService()
	account := seedProxyAccount(t, "steve", "")
	if err := svc.ClaimProxyAccount(account.ID, "core-uuid-1"); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	err := svc.ClaimProxyAccount(account.ID, "core-uuid-2")
	if err == nil {
		t.Fatal("second claim must fail (cbh already true)")
	}

	var claimed models.Account
	database.DB.First(&claimed, account.ID)
	if claimed.CoreUserID == nil || *claimed.CoreUserID != "core-uuid-1" {
		t.Fatalf("failed re-claim must not change core_user_id, got %v", claimed.CoreUserID)
	}
}

func TestForceBindTransferMovesUUIDAndDeletesSource(t *testing.T) {
	setupAdminTestDB(t)
	svc := NewAdminService()
	source := seedProxyAccount(t, "mojangplayer", "f7c77d999f154a66a87dc4a51ef30d19")

	if _, err := svc.ForceBindTransfer(source.ID, "target-core-uuid", "f7c77d999f154a66a87dc4a51ef30d19"); err != nil {
		t.Fatalf("force bind: %v", err)
	}

	var deleted models.Account
	if err := database.DB.First(&deleted, source.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("source account must be deleted, got err=%v", err)
	}

	var target models.Account
	if err := database.DB.Where("core_user_id = ?", "target-core-uuid").First(&target).Error; err != nil {
		t.Fatalf("target account missing: %v", err)
	}
	if !target.CBH {
		t.Fatal("created target account must be cbh=true")
	}
	if target.MojangUUID == nil || *target.MojangUUID != "f7c77d999f154a66a87dc4a51ef30d19" {
		t.Fatalf("target mojang_uuid = %v", target.MojangUUID)
	}
}

func TestForceBindTransferRejectsConflictingTargetUUID(t *testing.T) {
	setupAdminTestDB(t)
	svc := NewAdminService()
	source := seedProxyAccount(t, "mojangplayer", "f7c77d999f154a66a87dc4a51ef30d19")

	other := "0000000000000000000000000000abcd"
	target := models.Account{CoreUserID: ptrString("target-core-uuid"), CBH: true, MojangUUID: &other}
	if err := database.DB.Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}

	_, err := svc.ForceBindTransfer(source.ID, "target-core-uuid", "f7c77d999f154a66a87dc4a51ef30d19")
	if !errors.Is(err, ErrMojangUUIDAlreadyBound) {
		t.Fatalf("err = %v, want ErrMojangUUIDAlreadyBound", err)
	}

	var still models.Account
	if err := database.DB.First(&still, source.ID).Error; err != nil {
		t.Fatalf("rejected transfer must keep the source account: %v", err)
	}
}

// TestProxyRegisterPersistsCBHZero guards the regression where gorm dropped
// the zero-value CBH field on insert (column default 1), which made proxy
// accounts indistinguishable from claimed ones and blocked claim/force-bind.
func TestProxyRegisterPersistsCBHZero(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()

	account, _, err := as.ProxyRegister("mojangplayer", "pw123456", "f7c77d999f154a66a87dc4a51ef30d19")
	if err != nil {
		t.Fatalf("proxy register: %v", err)
	}

	var reloaded models.Account
	if err := database.DB.First(&reloaded, account.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.CBH {
		t.Fatal("proxy-registered account must persist cbh=false")
	}
}
