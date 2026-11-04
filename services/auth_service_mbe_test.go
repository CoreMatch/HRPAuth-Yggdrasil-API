package services

import (
	"errors"
	"testing"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

// resetMBEPendingForTest clears the package-global pending-revert map so
// tests do not leak timeout entries into each other (sqlite autoincrement
// restarts at 1 per test database).
func resetMBEPendingForTest() {
	mbePendingMu.Lock()
	mbePending = make(map[int]time.Time)
	mbePendingMu.Unlock()
}

// seedHAAccount seeds a claimed HA-side game account (cbh=1) with local
// credentials, mirroring a WebUI-registered player who may later open the
// Mojang bind window.
func seedHAAccount(t *testing.T, name, coreUserID, passwordHash string, mbe bool) *models.Account {
	t.Helper()
	account := models.Account{
		CoreUserID: &coreUserID,
		Password:   passwordHash,
		MBE:        mbe,
		CBH:        true,
		RegisterAt: time.Now(),
	}
	if err := database.DB.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	profile := models.Profile{ID: "p" + name, AccountID: account.ID, Name: name, Model: "default"}
	if err := database.DB.Create(&profile).Error; err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return &account
}

// TestProxyRegisterMBEBindCompletesWindow covers the restored pre-split
// semantics: a colliding proxy registration against an account with mbe=1
// binds the mojang_uuid, resets mbe, cancels the 15-minute timeout and
// preserves cbh/password/core_user_id/profile.
func TestProxyRegisterMBEBindCompletesWindow(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()
	account := seedHAAccount(t, "alex", "core-uuid-alex", "$2a$10$existinghash", true)
	RegisterMBETimeout(account.ID)

	const mojangUUID = "f7c77d999f154a66a87dc4a51ef30d19"
	gotAccount, gotProfile, err := as.ProxyRegister("alex", "incoming-password", mojangUUID)
	if err != nil {
		t.Fatalf("proxy register bind: %v", err)
	}
	if gotAccount.ID != account.ID {
		t.Fatalf("bind must return the existing account, got id=%d want %d", gotAccount.ID, account.ID)
	}
	if gotProfile.ID != "palex" {
		t.Fatalf("bind must return the existing profile, got %q", gotProfile.ID)
	}

	var reloaded models.Account
	if err := database.DB.First(&reloaded, account.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != mojangUUID {
		t.Fatalf("mojang_uuid = %v, want %s", reloaded.MojangUUID, mojangUUID)
	}
	if reloaded.MBE {
		t.Fatal("mbe must be reset to false after bind")
	}
	if !reloaded.CBH {
		t.Fatal("cbh must be preserved (true) by the bind")
	}
	if reloaded.Password != "$2a$10$existinghash" {
		t.Fatalf("password must be preserved, got %q", reloaded.Password)
	}
	if reloaded.CoreUserID == nil || *reloaded.CoreUserID != "core-uuid-alex" {
		t.Fatalf("core_user_id must be preserved, got %v", reloaded.CoreUserID)
	}
	if reloaded.LastSignAt == nil {
		t.Fatal("last_sign_at must be refreshed by the bind")
	}
	if !MBETimeoutFor(account.ID).IsZero() {
		t.Fatal("pending mbe timeout must be cancelled after bind")
	}

	// No second account or profile may have been created.
	var count int64
	database.DB.Model(&models.Account{}).Count(&count)
	if count != 1 {
		t.Fatalf("accounts count = %d, want 1", count)
	}
	database.DB.Model(&models.Profile{}).Count(&count)
	if count != 1 {
		t.Fatalf("profiles count = %d, want 1", count)
	}
}

// TestProxyRegisterCollisionWithoutMBERejected: mbe=0 keeps HA priority — the
// colliding Mojang player is rejected and the account stays untouched.
func TestProxyRegisterCollisionWithoutMBERejected(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()
	account := seedHAAccount(t, "alex", "core-uuid-alex", "$2a$10$existinghash", false)

	_, _, err := as.ProxyRegister("alex", "incoming-password", "f7c77d999f154a66a87dc4a51ef30d19")
	if !errors.Is(err, ErrUsernameAlreadyBound) {
		t.Fatalf("err = %v, want ErrUsernameAlreadyBound", err)
	}

	var reloaded models.Account
	if err := database.DB.First(&reloaded, account.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.MojangUUID != nil {
		t.Fatalf("rejected bind must not write mojang_uuid, got %v", *reloaded.MojangUUID)
	}
	if reloaded.MBE {
		t.Fatal("rejected bind must not change mbe")
	}
}

// TestProxyRegisterCollisionWithBoundAccountRejected: an account that already
// carries a different mojang_uuid rejects the collision regardless of mbe.
func TestProxyRegisterCollisionWithBoundAccountRejected(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()
	other := "0000000000000000000000000000abcd"
	account := seedHAAccount(t, "alex", "core-uuid-alex", "$2a$10$existinghash", true)
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).Update("mojang_uuid", other).Error; err != nil {
		t.Fatalf("set mojang_uuid: %v", err)
	}

	_, _, err := as.ProxyRegister("alex", "incoming-password", "f7c77d999f154a66a87dc4a51ef30d19")
	if !errors.Is(err, ErrUsernameAlreadyBound) {
		t.Fatalf("err = %v, want ErrUsernameAlreadyBound", err)
	}

	var reloaded models.Account
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != other {
		t.Fatalf("existing mojang_uuid must be untouched, got %v", reloaded.MojangUUID)
	}
}

// TestMBETimeoutExpiryDisablesBind: after the 15-minute window lapses the
// sweep resets mbe=0, and a later colliding proxy registration is rejected.
func TestMBETimeoutExpiryDisablesBind(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()
	account := seedHAAccount(t, "alex", "core-uuid-alex", "$2a$10$existinghash", true)
	RegisterMBETimeout(account.ID)

	// Force expiry: push the deadline into the past, then sweep.
	mbePendingMu.Lock()
	mbePending[account.ID] = time.Now().Add(-time.Second)
	mbePendingMu.Unlock()
	RunMBETimeoutSweep()

	var expired models.Account
	if err := database.DB.First(&expired, account.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if expired.MBE {
		t.Fatal("mbe must be reset to false after the timeout sweep")
	}

	_, _, err := as.ProxyRegister("alex", "incoming-password", "f7c77d999f154a66a87dc4a51ef30d19")
	if !errors.Is(err, ErrUsernameAlreadyBound) {
		t.Fatalf("err = %v, want ErrUsernameAlreadyBound after timeout", err)
	}
}

// TestMBESweepSkipsBoundAccounts: the sweep must not clobber an account whose
// bind completed between scheduling and the sweep tick.
func TestMBESweepSkipsBoundAccounts(t *testing.T) {
	setupAdminTestDB(t)
	account := seedHAAccount(t, "alex", "core-uuid-alex", "$2a$10$existinghash", true)
	const mojangUUID = "f7c77d999f154a66a87dc4a51ef30d19"
	RegisterMBETimeout(account.ID)
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).Update("mojang_uuid", mojangUUID).Error; err != nil {
		t.Fatalf("set mojang_uuid: %v", err)
	}

	mbePendingMu.Lock()
	mbePending[account.ID] = time.Now().Add(-time.Second)
	mbePendingMu.Unlock()
	RunMBETimeoutSweep()

	var reloaded models.Account
	database.DB.First(&reloaded, account.ID)
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != mojangUUID {
		t.Fatalf("sweep must not touch a bound account's mojang_uuid, got %v", reloaded.MojangUUID)
	}
}

// TestProxyRegisterInvalidUUID guards the 400 path of /internal/proxy-register.
func TestProxyRegisterInvalidUUID(t *testing.T) {
	setupAdminTestDB(t)
	as := NewAuthService()

	_, _, err := as.ProxyRegister("newplayer", "pw123456", "not-a-uuid")
	if !errors.Is(err, ErrInvalidMojangUUID) {
		t.Fatalf("err = %v, want ErrInvalidMojangUUID", err)
	}
}
