package services

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
)

const testSkinUUID = "f7c77d999f154a66a87dc4a51ef30d19"

// setupSkinSyncEnv extends setupAdminTestDB with everything the texture
// apply path needs: a stubbed storage lock (sqlite cannot do MySQL
// GET_LOCK), a texture storage directory, a signing key and a callback URL.
func setupSkinSyncEnv(t *testing.T) {
	t.Helper()
	setupAdminTestDB(t)

	// The texture apply path writes profile_properties, which the plain
	// admin DDL does not create.
	if err := database.DB.Exec(`CREATE TABLE profile_properties (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		profile_id varchar(32) NOT NULL,
		name varchar(255) NOT NULL,
		value text NOT NULL,
		signature text,
		delete_when bigint NOT NULL DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create profile_properties: %v", err)
	}

	prevLock := acquireTextureStorageLock
	acquireTextureStorageLock = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { acquireTextureStorageLock = prevLock })

	if config.AppConfig == nil {
		config.AppConfig = &config.Config{}
	}
	config.AppConfig.Yggdrasil.Server.TexturesStorage = t.TempDir()
	config.AppConfig.Callback.URL = "http://ygg.test"
	config.AppConfig.Yggdrasil.Server.SignaturePrivateKey = generateTestRSAKeyPEM(t)
}

func generateTestRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return string(pemBytes)
}

// validSkinPNG returns a minimal valid 64x64 skin PNG.
func validSkinPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 4), uint8(y * 4), 0, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// texturesPropertyValue builds the base64 sessionserver textures property.
func texturesPropertyValue(t *testing.T, skinURL, model string) string {
	t.Helper()
	info := models.TextureInfo{URL: skinURL}
	if model != "" {
		info.Metadata = map[string]interface{}{"model": model}
	}
	payload := models.TexturePayload{
		Timestamp:   1,
		ProfileID:   testSkinUUID,
		ProfileName: "Steve",
		Textures:    map[string]models.TextureInfo{"SKIN": info},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

// startFakeMojang serves the sessionserver profile endpoint with a
// configurable textures property value ("" answers a profile without any
// textures property).
func startFakeMojang(t *testing.T, texturesValue string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if !strings.HasPrefix(r.URL.Path, "/session/minecraft/profile/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		props := "[]"
		if texturesValue != "" {
			props = fmt.Sprintf(`[{"name":"textures","value":%q}]`, texturesValue)
		}
		fmt.Fprintf(w, `{"id":%q,"name":"Steve","properties":%s}`,
			strings.TrimPrefix(r.URL.Path, "/session/minecraft/profile/"), props)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func reloadAccount(t *testing.T, id int) models.Account {
	t.Helper()
	var account models.Account
	if err := database.DB.First(&account, id).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	return account
}

// TestSkinSyncAppliesOfficialSkin covers the happy path end to end: the
// official skin is downloaded, stored locally, wired into the profile
// textures property (signed), and the sync mark makes the repeat run a
// no-op without re-downloading.
func TestSkinSyncAppliesOfficialSkin(t *testing.T) {
	setupSkinSyncEnv(t)
	skinPNG := validSkinPNG(t)
	officialHash := strings.Repeat("ab", 32)

	var textureHits int32
	textureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&textureHits, 1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(skinPNG)
	}))
	t.Cleanup(textureServer.Close)

	sessionServer, sessionHits := startFakeMojang(t,
		texturesPropertyValue(t, textureServer.URL+"/texture/"+officialHash, "slim"), http.StatusOK)

	account := seedProxyAccount(t, "steve", testSkinUUID)
	ss := NewSkinSyncService()
	ss.sessionserverBase = sessionServer.URL

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultSynced {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultSynced)
	}

	// The profile textures property now points at local storage.
	textures, err := ss.textureSvc.GetTextures("psteve")
	if err != nil {
		t.Fatalf("get textures: %v", err)
	}
	skin, ok := textures["SKIN"]
	if !ok {
		t.Fatal("profile has no SKIN texture after sync")
	}
	if !strings.HasPrefix(skin.URL, "http://ygg.test/textures/") {
		t.Fatalf("skin URL = %q, want local /textures/ URL", skin.URL)
	}
	if model, _ := skin.Metadata["model"].(string); model != "slim" {
		t.Fatalf("skin model = %q, want slim", model)
	}
	localHash := strings.TrimPrefix(skin.URL, "http://ygg.test/textures/")
	if _, err := os.Stat(filepath.Join(config.AppConfig.Yggdrasil.Server.TexturesStorage, "textures", localHash)); err != nil {
		t.Fatalf("local texture file missing: %v", err)
	}

	// The mark pins the official hash, not the re-encoded local hash.
	reloaded := reloadAccount(t, account.ID)
	wantMark := testSkinUUID + ":" + officialHash
	if reloaded.SkinSyncMark == nil || *reloaded.SkinSyncMark != wantMark {
		t.Fatalf("mark = %v, want %q", reloaded.SkinSyncMark, wantMark)
	}

	// Repeat bind / repeat run is idempotent: no new upstream calls, the
	// mark short-circuits before sessionserver.
	if got := ss.RunSync(account.ID, "test_repeat"); got != SkinSyncResultUpToDate {
		t.Fatalf("repeat result = %s, want %s", got, SkinSyncResultUpToDate)
	}
	if atomic.LoadInt32(sessionHits) != 1 {
		t.Fatalf("sessionserver hits = %d, want 1 (idempotent rerun must not call upstream)", *sessionHits)
	}
	if atomic.LoadInt32(&textureHits) != 1 {
		t.Fatalf("texture hits = %d, want 1", textureHits)
	}
}

// TestSkinSyncUpstreamUnreachableDoesNotBlock: Mojang down → classified
// failure, no mark, nothing applied; the bind itself is untouched (the
// caller never sees an error).
func TestSkinSyncUpstreamUnreachableDoesNotBlock(t *testing.T) {
	setupSkinSyncEnv(t)
	account := seedProxyAccount(t, "steve", testSkinUUID)

	ss := NewSkinSyncService()
	ss.sessionserverBase = "http://127.0.0.1:1" // closed port

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultUpstreamUnreachable {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultUpstreamUnreachable)
	}

	reloaded := reloadAccount(t, account.ID)
	if reloaded.SkinSyncMark != nil {
		t.Fatalf("mark must stay empty after a failed sync, got %q", *reloaded.SkinSyncMark)
	}
	textures, err := ss.textureSvc.GetTextures("psteve")
	if err != nil {
		t.Fatalf("get textures: %v", err)
	}
	if len(textures) != 0 {
		t.Fatalf("failed sync must not create textures, got %v", textures)
	}
	// The bound account itself is intact.
	if reloaded.MojangUUID == nil || *reloaded.MojangUUID != testSkinUUID {
		t.Fatalf("bind must survive a failed sync, mojang_uuid = %v", reloaded.MojangUUID)
	}
}

// TestSkinSyncUpstreamStatusFailure: sessionserver 5xx is the same class.
func TestSkinSyncUpstreamStatusFailure(t *testing.T) {
	setupSkinSyncEnv(t)
	account := seedProxyAccount(t, "steve", testSkinUUID)
	sessionServer, _ := startFakeMojang(t, "", http.StatusInternalServerError)

	ss := NewSkinSyncService()
	ss.sessionserverBase = sessionServer.URL

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultUpstreamUnreachable {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultUpstreamUnreachable)
	}
}

// TestSkinSyncNoOfficialSkinClearsLocal: the premium account has no custom
// skin → the mirror removes any local skin so the client shows the default
// skin, and the mark records the "default" state.
func TestSkinSyncNoOfficialSkinClearsLocal(t *testing.T) {
	setupSkinSyncEnv(t)
	account := seedProxyAccount(t, "steve", testSkinUUID)

	ss := NewSkinSyncService()
	// The account had a locally uploaded skin before the bind.
	if err := ss.textureSvc.UpdateProfileTexture("psteve", "skin", "http://ygg.test/textures/"+strings.Repeat("cd", 32), "default"); err != nil {
		t.Fatalf("seed local skin: %v", err)
	}

	sessionServer, _ := startFakeMojang(t, "", http.StatusOK) // profile without textures property
	ss.sessionserverBase = sessionServer.URL

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultNoOfficialSkin {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultNoOfficialSkin)
	}

	textures, err := ss.textureSvc.GetTextures("psteve")
	if err != nil {
		t.Fatalf("get textures: %v", err)
	}
	if _, ok := textures["SKIN"]; ok {
		t.Fatal("local skin must be cleared when the official profile has none")
	}
	reloaded := reloadAccount(t, account.ID)
	wantMark := testSkinUUID + ":default"
	if reloaded.SkinSyncMark == nil || *reloaded.SkinSyncMark != wantMark {
		t.Fatalf("mark = %v, want %q", reloaded.SkinSyncMark, wantMark)
	}
}

// TestSkinSyncDisabledNotOverwritten: with the toggle off, neither a bind
// nor a manual run touches the profile textures.
func TestSkinSyncDisabledNotOverwritten(t *testing.T) {
	setupSkinSyncEnv(t)
	account := seedProxyAccount(t, "steve", testSkinUUID)
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).Update("skin_sync", false).Error; err != nil {
		t.Fatalf("disable toggle: %v", err)
	}
	ss := NewSkinSyncService()
	// Seed a local skin that must survive.
	if err := ss.textureSvc.UpdateProfileTexture("psteve", "skin", "http://ygg.test/textures/"+strings.Repeat("cd", 32), "default"); err != nil {
		t.Fatalf("seed local skin: %v", err)
	}

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultDisabled {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultDisabled)
	}
	textures, _ := ss.textureSvc.GetTextures("psteve")
	if _, ok := textures["SKIN"]; !ok {
		t.Fatal("disabled account's skin must not be touched")
	}

	// RequestSkinSync does not even dispatch for a disabled account.
	var dispatched int32
	t.Cleanup(OverrideSkinSyncDispatchForTest(func(int, string, func()) { atomic.AddInt32(&dispatched, 1) }))
	ss.RequestSkinSync(account.ID, "test")
	if atomic.LoadInt32(&dispatched) != 0 {
		t.Fatal("disabled account must not dispatch a sync")
	}
}

// TestBindEntryPointsTriggerSkinSync asserts that every implemented bind
// completion schedules a sync for the right account: proxy-register (new
// creation, idempotent return, mbe-window hit), /register with a
// mojang_uuid, claim and force-bind. The future client direct-bind
// endpoint (SamuelClient ADR-0007) must call the same hook when it lands.
func TestBindEntryPointsTriggerSkinSync(t *testing.T) {
	setupSkinSyncEnv(t)

	var mu sync.Mutex
	var calls []string
	t.Cleanup(OverrideSkinSyncDispatchForTest(func(accountID int, trigger string, _ func()) {
		mu.Lock()
		calls = append(calls, fmt.Sprintf("%d:%s", accountID, trigger))
		mu.Unlock()
	}))
	expectCall := func(want string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(calls) == 0 || calls[len(calls)-1] != want {
			t.Fatalf("last sync dispatch = %v, want %q", calls, want)
		}
	}

	as := NewAuthService()
	adminSvc := NewAdminService()

	// 1. proxy-register, new proxy account.
	acc1, _, err := as.ProxyRegister("newplayer", "pw123456", testSkinUUID)
	if err != nil {
		t.Fatalf("proxy register: %v", err)
	}
	expectCall(fmt.Sprintf("%d:proxy_register", acc1.ID))

	// 2. proxy-register, idempotent return of the already-bound account.
	if _, _, err := as.ProxyRegister("newplayer", "pw123456", testSkinUUID); err != nil {
		t.Fatalf("idempotent proxy register: %v", err)
	}
	expectCall(fmt.Sprintf("%d:proxy_register", acc1.ID))

	// 3. proxy-register hitting an open mbe window.
	ha := seedHAAccount(t, "mbename", "core-mbe", "$2a$10$existinghash", true)
	const uuid2 = "0000000000000000000000000000abcd"
	if _, _, err := as.ProxyRegister("mbename", "incoming-pw", uuid2); err != nil {
		t.Fatalf("mbe bind: %v", err)
	}
	expectCall(fmt.Sprintf("%d:proxy_register_mbe_bind", ha.ID))

	// 4. /register with a mojang_uuid (fake core verifies credentials). The
	// AuthService must be constructed after the base URL swap: the core
	// client captures BaseURL at construction time.
	coreServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/verify-credentials" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"uid": 1, "uuid": "core-uuid-reg", "email": "reg@example.com", "username": "reguser", "permission": 0,
		})
	}))
	t.Cleanup(coreServer.Close)
	prevBase := config.AppConfig.CoreAPI.BaseURL
	config.AppConfig.CoreAPI.BaseURL = coreServer.URL
	t.Cleanup(func() { config.AppConfig.CoreAPI.BaseURL = prevBase })
	const uuid3 = "0000000000000000000000000000beef"
	acc3, _, err := NewAuthService().RegisterGameAccount("reguser", "pw123456", uuid3)
	if err != nil {
		t.Fatalf("register with mojang_uuid: %v", err)
	}
	expectCall(fmt.Sprintf("%d:register", acc3.ID))

	// 5. claim of a bound proxy account.
	src := seedProxyAccount(t, "claimme", "00000000000000000000000000001234")
	if err := adminSvc.ClaimProxyAccount(src.ID, "core-claim"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	expectCall(fmt.Sprintf("%d:claim_user", src.ID))

	// 6. force-bind moves the uuid to the target account, which gets the sync.
	fbSource := seedProxyAccount(t, "fbname", "00000000000000000000000000005678")
	targetID, err := adminSvc.ForceBindTransfer(fbSource.ID, "core-target", "00000000000000000000000000005678")
	if err != nil {
		t.Fatalf("force bind: %v", err)
	}
	expectCall(fmt.Sprintf("%d:force_bind", targetID))
}

// TestRequestSkinSyncSkipsWhenMarkCovers: the cheap pre-check avoids any
// dispatch once the mark shows the bound uuid is already mirrored.
func TestRequestSkinSyncSkipsWhenMarkCovers(t *testing.T) {
	setupSkinSyncEnv(t)
	account := seedProxyAccount(t, "steve", testSkinUUID)
	mark := testSkinUUID + ":" + strings.Repeat("ab", 32)
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).Update("skin_sync_mark", mark).Error; err != nil {
		t.Fatalf("set mark: %v", err)
	}

	var dispatched int32
	t.Cleanup(OverrideSkinSyncDispatchForTest(func(int, string, func()) { atomic.AddInt32(&dispatched, 1) }))
	NewSkinSyncService().RequestSkinSync(account.ID, "test")
	if atomic.LoadInt32(&dispatched) != 0 {
		t.Fatal("covered mark must not dispatch")
	}
}

// TestSkinSyncToggleEndpoints covers the account-level switch: disable
// clears the mark (so re-enable resyncs) and stops coverage; enable
// schedules a sync for a bound account.
func TestSkinSyncToggleEndpoints(t *testing.T) {
	setupSkinSyncEnv(t)

	var mu sync.Mutex
	var calls []string
	t.Cleanup(OverrideSkinSyncDispatchForTest(func(accountID int, trigger string, _ func()) {
		mu.Lock()
		calls = append(calls, fmt.Sprintf("%d:%s", accountID, trigger))
		mu.Unlock()
	}))

	account := seedProxyAccount(t, "steve", testSkinUUID)
	mark := testSkinUUID + ":" + strings.Repeat("ab", 32)
	if err := database.DB.Model(&models.Account{}).Where("id = ?", account.ID).Update("skin_sync_mark", mark).Error; err != nil {
		t.Fatalf("set mark: %v", err)
	}
	// A valid local Yggdrasil token for ResolveAccount.
	if err := database.DB.Exec(`CREATE TABLE IF NOT EXISTS tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		access_token varchar(255) UNIQUE,
		client_token varchar(255),
		account_id INTEGER,
		selected_profile_id varchar(32),
		issued_at bigint,
		expires_in_days INTEGER DEFAULT 15,
		state varchar(20) DEFAULT 'valid',
		created_at datetime
	)`).Error; err != nil {
		t.Fatalf("create tokens table: %v", err)
	}
	token := models.Token{AccessToken: "tok-skin-sync", ClientToken: "ct", AccountID: account.ID, SelectedProfileID: "psteve", IssuedAt: utils.CurrentTimestampMillis(), ExpiresInDays: 15, State: "valid"}
	if err := database.DB.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	ps := NewPlayerService()

	info, err := ps.DisableSkinSync("tok-skin-sync")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if info.SkinSync {
		t.Fatal("skin_sync must be false after disable")
	}
	reloaded := reloadAccount(t, account.ID)
	if reloaded.SkinSyncMark != nil {
		t.Fatalf("disable must clear the mark, got %q", *reloaded.SkinSyncMark)
	}
	// While disabled, a bind-time request does not dispatch.
	ps2 := NewSkinSyncService()
	ps2.RequestSkinSync(account.ID, "test_while_disabled")
	mu.Lock()
	if len(calls) != 0 {
		t.Fatalf("disabled account dispatched %v", calls)
	}
	mu.Unlock()

	info, err = ps.EnableSkinSync("tok-skin-sync")
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !info.SkinSync {
		t.Fatal("skin_sync must be true after enable")
	}
	mu.Lock()
	defer mu.Unlock()
	want := fmt.Sprintf("%d:manual_enable", account.ID)
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("enable dispatch = %v, want [%s]", calls, want)
	}
}

// TestLoginRetryNeeded pins the retry predicate used by the authenticate
// hook: retry when bound + enabled + mark absent or covering another uuid;
// no retry when unbound, disabled, or already covered.
func TestLoginRetryNeeded(t *testing.T) {
	uuid := testSkinUUID
	other := uuid + ":deadbeef"
	covered := uuid + ":" + strings.Repeat("ab", 32)
	stale := "0000000000000000000000000000ffff:" + strings.Repeat("ab", 32)

	cases := []struct {
		name    string
		account models.Account
		want    bool
	}{
		{name: "unbound", account: models.Account{SkinSync: true}, want: false},
		{name: "disabled", account: models.Account{SkinSync: false, MojangUUID: &uuid}, want: false},
		{name: "never synced", account: models.Account{SkinSync: true, MojangUUID: &uuid}, want: true},
		{name: "covered", account: models.Account{SkinSync: true, MojangUUID: &uuid, SkinSyncMark: &covered}, want: false},
		{name: "default mark covers", account: models.Account{SkinSync: true, MojangUUID: &uuid, SkinSyncMark: &other}, want: false},
		{name: "stale uuid in mark", account: models.Account{SkinSync: true, MojangUUID: &uuid, SkinSyncMark: &stale}, want: true},
	}
	ss := NewSkinSyncService()
	for _, tc := range cases {
		if got := ss.LoginRetryNeeded(&tc.account); got != tc.want {
			t.Errorf("%s: LoginRetryNeeded = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// TestSkinSyncInvalidTexture: the official endpoint serves a payload that
// fails texture validation → classified, not applied.
func TestSkinSyncInvalidTexture(t *testing.T) {
	setupSkinSyncEnv(t)
	officialHash := strings.Repeat("ef", 32)

	textureServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("this is not a png"))
	}))
	t.Cleanup(textureServer.Close)
	sessionServer, _ := startFakeMojang(t,
		texturesPropertyValue(t, textureServer.URL+"/texture/"+officialHash, ""), http.StatusOK)

	account := seedProxyAccount(t, "steve", testSkinUUID)
	ss := NewSkinSyncService()
	ss.sessionserverBase = sessionServer.URL

	if got := ss.RunSync(account.ID, "test"); got != SkinSyncResultTextureInvalid {
		t.Fatalf("result = %s, want %s", got, SkinSyncResultTextureInvalid)
	}
	reloaded := reloadAccount(t, account.ID)
	if reloaded.SkinSyncMark != nil {
		t.Fatal("invalid texture must not set the mark")
	}
}
