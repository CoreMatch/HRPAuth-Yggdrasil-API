package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const coordinatorMigrationID = "123e4567-e89b-12d3-a456-426614174000"
const coordinatorCoreUUID = "0123456789abcdef0123456789abcdef"
const coordinatorProfileID = "0123456789ABCDEF0123456789ABCDEF"

type coordinatorMemoryStore struct {
	mu          sync.Mutex
	locked      bool
	packageData StagedMigrationPackage
}

func (s *coordinatorMemoryStore) Acquire(ctx context.Context, migrationID string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return nil, ErrMigrationLocked
	}
	s.locked = true
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.locked = false
	}, nil
}

func (s *coordinatorMemoryStore) Stage(ctx context.Context, migrationID string, packageData StagedMigrationPackage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.packageData.Digest != "" {
		if s.packageData.Digest != packageData.Digest {
			return ErrStagedConflict
		}
		return nil
	}
	s.packageData = packageData
	return nil
}

func (s *coordinatorMemoryStore) Load(ctx context.Context, migrationID string) (StagedMigrationPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.packageData.Digest == "" {
		return StagedMigrationPackage{}, ErrMigrationNotFound
	}
	return s.packageData, nil
}

func (s *coordinatorMemoryStore) Delete(ctx context.Context, migrationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packageData = StagedMigrationPackage{}
	return nil
}

type coordinatorMemoryResults struct {
	mu       sync.Mutex
	result   MigrationCommitResult
	failSave error
}

func (s *coordinatorMemoryResults) Save(ctx context.Context, result MigrationCommitResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave != nil {
		return s.failSave
	}
	s.result = result
	return nil
}

func (s *coordinatorMemoryResults) Load(ctx context.Context, migrationID string) (MigrationCommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result.MigrationID == "" {
		return MigrationCommitResult{}, ErrMigrationResultNotFound
	}
	return s.result, nil
}

func (s *coordinatorMemoryResults) Delete(ctx context.Context, migrationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = MigrationCommitResult{}
	return nil
}

type coordinatorCoreState struct {
	mu                    sync.Mutex
	created               bool
	coreCreated           bool
	coreOwnership         string
	coreStatus            string
	coreEmail             string
	claimCalls            int
	verifyCalls           int
	inspectMigrationCalls int
	rollbackCalls         int
	userMissing           bool
	userMismatch          bool
	migrationMissing      bool
	rollbackFailureStatus int
	loseRollbackResponse  bool
}

type coordinatorVerifier struct {
	mu         sync.Mutex
	assets     map[string][]byte
	properties []models.ProfileProperty
	applyCalls int
	failApply  error
	onApply    func()
	badURL     bool
}

func (v *coordinatorVerifier) ApplyAsset(profileID, textureType, model string, data []byte) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.applyCalls++
	if v.onApply != nil {
		v.onApply()
	}
	if v.failApply != nil {
		return "", v.failApply
	}
	hash := fmt.Sprintf("%x", migrationPNGSHA256(data))
	v.assets[hash] = bytes.Clone(data)
	payload := models.TexturePayload{ProfileID: profileID, ProfileName: "Player", Textures: make(map[string]models.TextureInfo)}
	for _, property := range v.properties {
		if property.Name == "textures" {
			existing, err := decodeMigrationTextures(property.Value)
			if err == nil {
				payload.Textures = existing.Textures
			}
		}
	}
	url := "https://target.example/textures/" + hash
	if v.badURL {
		url = "https://wrong.example/textures/" + hash
	}
	metadata := make(map[string]interface{})
	if textureType == "skin" {
		metadata["model"] = model
	}
	payload.Textures[strings.ToUpper(textureType)] = models.TextureInfo{URL: url, Metadata: metadata}
	encoded, _ := json.Marshal(payload)
	v.properties = []models.ProfileProperty{{ProfileID: profileID, Name: "textures", Value: base64.StdEncoding.EncodeToString(encoded)}}
	return hash, nil
}

func (v *coordinatorVerifier) HasStoredAsset(hash string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, exists := v.assets[hash]
	return exists, nil
}

func (v *coordinatorVerifier) StoredAsset(hash string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	data, exists := v.assets[hash]
	if !exists {
		return nil, errors.New("stored asset missing")
	}
	return bytes.Clone(data), nil
}

func (v *coordinatorVerifier) ProfileProperties(profileID, profileName string) ([]models.ProfileProperty, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	properties := append([]models.ProfileProperty{}, v.properties...)
	var stored []models.ProfileProperty
	if err := database.DB.Where("profile_id = ? AND name <> ?", profileID, "textures").Find(&stored).Error; err != nil {
		return nil, err
	}
	properties = append(properties, stored...)
	hasUploadable := false
	for _, property := range properties {
		hasUploadable = hasUploadable || property.Name == "uploadableTextures"
	}
	if !hasUploadable {
		properties = append(properties, models.ProfileProperty{ProfileID: profileID, Name: "uploadableTextures", Value: "skin,cape"})
	}
	return properties, nil
}

func (v *coordinatorVerifier) CallbackURL() string { return "https://target.example" }

func migrationPNGSHA256(data []byte) [32]byte {
	return sha256.Sum256(data)
}

func setupMigrationCoordinator(t *testing.T) (*MigrationCoordinator, *coordinatorMemoryStore, *coordinatorMemoryResults, *coordinatorCoreState, *coordinatorVerifier, *clients.CoreClient) {
	t.Helper()
	previousConfig := config.AppConfig
	previousDB := database.DB
	config.AppConfig = &config.Config{}
	config.AppConfig.Callback.URL = "https://target.example"
	config.AppConfig.Yggdrasil.Security.TokenExpiryDays = 15
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	database.DB = db
	t.Cleanup(func() {
		config.AppConfig = previousConfig
		database.DB = previousDB
	})
	for _, ddl := range []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, core_user_id TEXT UNIQUE, mojang_uuid TEXT UNIQUE, password TEXT, mbe INTEGER, cbh INTEGER DEFAULT 1, skin_sync INTEGER DEFAULT 1, skin_sync_mark TEXT, created_at DATETIME, last_sign_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE profiles (id TEXT PRIMARY KEY, account_id INTEGER, name TEXT, model TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE profile_properties (id INTEGER PRIMARY KEY AUTOINCREMENT, profile_id TEXT, name TEXT, value TEXT, signature TEXT, delete_when INTEGER DEFAULT 0)`,
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, access_token TEXT UNIQUE, client_token TEXT, account_id INTEGER, selected_profile_id TEXT, issued_at INTEGER, expires_in_days INTEGER, state TEXT, created_at DATETIME)`,
		`CREATE TABLE migration_journal (migration_id TEXT PRIMARY KEY, digest TEXT, status TEXT, stage TEXT, failure_code TEXT, core_uid TEXT, core_uuid TEXT, core_created INTEGER, account_id INTEGER, profile_id TEXT, profile_name TEXT, committed_at DATETIME, rolled_back_at DATETIME, created_at DATETIME, updated_at DATETIME)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	state := &coordinatorCoreState{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		switch r.URL.Path {
		case "/internal/claim-user":
			state.claimCalls++
			var body struct {
				Username    string  `json:"username"`
				Email       *string `json:"email"`
				Password    string  `json:"password"`
				MigrationID string  `json:"migration_id"`
				EmailMode   string  `json:"email_mode"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.Password != "secret-password" || body.MigrationID != coordinatorMigrationID {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"success":false,"code":"migration_conflict"}`))
				return
			}
			created := !state.created
			state.created = true
			state.coreCreated = created
			state.coreOwnership = "preexisting"
			state.coreStatus = "not_created"
			if created {
				state.coreOwnership = "created_by_migration"
				state.coreStatus = "created"
			}
			email := "migration-" + coordinatorMigrationID + "@placeholder.invalid"
			if body.Email != nil {
				email = *body.Email
			}
			state.coreEmail = email
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"uid": 7, "uuid": coordinatorCoreUUID, "email": email, "created": created}})
		case "/internal/verify-credentials":
			state.verifyCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["password"] != "secret-password" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"UUID": coordinatorCoreUUID, "Email": "migration-" + coordinatorMigrationID + "@placeholder.invalid", "Username": "Player"})
		case "/internal/migrations/" + coordinatorMigrationID:
			state.inspectMigrationCalls++
			if state.migrationMissing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"success":false,"code":"migration_not_found"}`))
				return
			}
			username := "Player"
			if state.userMismatch {
				username = "Other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"migration_id": coordinatorMigrationID, "status": state.coreStatus, "ownership": state.coreOwnership,
				"user":       map[string]any{"uid": 7, "uuid": coordinatorCoreUUID, "email": state.coreEmail, "username": username, "present": !state.userMissing},
				"updated_at": time.Now().UTC(),
			}})
		case "/internal/migrations/" + coordinatorMigrationID + "/rollback":
			state.rollbackCalls++
			if state.migrationMissing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"success":false,"code":"migration_not_found"}`))
				return
			}
			if state.rollbackFailureStatus != 0 {
				w.WriteHeader(state.rollbackFailureStatus)
				_, _ = w.Write([]byte(`{"success":false,"code":"internal_error"}`))
				return
			}
			if state.coreOwnership != "created_by_migration" || state.userMismatch {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"success":false,"code":"migration_rollback_not_allowed"}`))
				return
			}
			state.coreStatus = "rolled_back"
			state.userMissing = true
			if state.loseRollbackResponse {
				state.loseRollbackResponse = false
				panic(http.ErrAbortHandler)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"migration_id": coordinatorMigrationID, "status": "rolled_back", "uid": 7,
			}})
		case "/services/users/" + coordinatorCoreUUID:
			if state.userMissing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"success":false,"code":"user_not_found"}`))
				return
			}
			username := "Player"
			if state.userMismatch {
				username = "Other"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"uid": 7, "uuid": coordinatorCoreUUID, "email": state.coreEmail, "username": username,
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	core := &clients.CoreClient{BaseURL: server.URL, HTTPClient: server.Client()}
	store := &coordinatorMemoryStore{}
	results := &coordinatorMemoryResults{}
	verifier := &coordinatorVerifier{assets: make(map[string][]byte)}
	coordinator, err := NewMigrationCoordinator(core, store, results, NewAuthService(), verifier, db)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator, store, results, state, verifier, core
}

func coordinatorRequest(t *testing.T, withAssets bool) MigrationCommitRequest {
	t.Helper()
	var skin, cape []byte
	var skinHash, capeHash string
	if withAssets {
		skin = coordinatorPNG(t, 64, 64)
		cape = coordinatorPNG(t, 64, 32)
		normalizedSkin, err := normalizeMigrationAsset("skin", skin)
		if err != nil {
			t.Fatal(err)
		}
		normalizedCape, err := normalizeMigrationAsset("cape", cape)
		if err != nil {
			t.Fatal(err)
		}
		skinHash, capeHash = normalizedSkin.PixelSHA256, normalizedCape.PixelSHA256
	}
	request, err := NormalizeMigrationCommit(MigrationMetadata{
		MigrationID: coordinatorMigrationID, Identifier: "Player", Password: "secret-password",
		Username: "Player", ProfileUUID: coordinatorProfileID, ProfileName: "Player", ProfileModel: "default",
		SkinSHA256: skinHash, CapeSHA256: capeHash,
		LegacyUserProperties: map[string]string{"legacy_rank": "vip", "legacy_empty": ""},
	}, skin, cape)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func coordinatorPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestMigrationCoordinatorCommitReplayAndFreshSession(t *testing.T) {
	coordinator, store, _, state, verifier, _ := setupMigrationCoordinator(t)
	request := coordinatorRequest(t, true)
	result, err := coordinator.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.MigrationID != coordinatorMigrationID || result.CoreUID != "7" || result.ProfileID != coordinatorProfileID || result.User.ID != coordinatorProfileID || !result.CreatedCoreUser || result.ReusedCoreUser {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Assets.Skin.Status != "uploaded" || result.Assets.Skin.SHA256 != request.Skin.PixelSHA256 || result.Assets.Cape.Status != "uploaded" || result.Assets.Cape.SHA256 != request.Cape.PixelSHA256 || len(result.User.Properties) != 3 {
		t.Fatalf("unexpected assets or properties: %+v", result)
	}
	var account models.Account
	if err := database.DB.First(&account).Error; err != nil {
		t.Fatal(err)
	}
	if account.SkinSync || account.CoreUserID == nil || *account.CoreUserID != coordinatorCoreUUID {
		t.Fatalf("unexpected account: %+v", account)
	}
	var journal models.MigrationJournal
	if err := database.DB.First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "committed" || journal.CommittedAt == nil || !journal.CoreCreated {
		t.Fatalf("unexpected journal: %+v", journal)
	}
	staged, err := store.Load(context.Background(), coordinatorMigrationID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeStagedMigration(staged)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("secret-password")) || bytes.Contains(encoded, []byte(`"password"`)) {
		t.Fatal("plaintext password reached staged package")
	}
	replayed, err := coordinator.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Session.AccessToken != result.Session.AccessToken || verifier.applyCalls != 2 || state.claimCalls != 1 {
		t.Fatalf("replay was not idempotent: token=%s applies=%d claims=%d", replayed.Session.AccessToken, verifier.applyCalls, state.claimCalls)
	}
	coordinator.auth.InvalidateToken(result.Session.AccessToken)
	fresh, err := coordinator.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Session.AccessToken == result.Session.AccessToken || verifier.applyCalls != 2 || fresh.CommittedAt != result.CommittedAt {
		t.Fatalf("fresh session replay mutated assets: %+v", fresh)
	}
	if token := coordinator.auth.ValidateToken(fresh.Session.AccessToken, fresh.Session.ClientToken); token == nil {
		t.Fatal("fresh replay session did not verify")
	}

	conflict := request
	conflict.Metadata.LegacyUserProperties = cloneMigrationProperties(request.Metadata.LegacyUserProperties)
	conflict.Metadata.LegacyUserProperties["legacy_rank"] = "admin"
	conflict, err = NormalizeMigrationCommit(MigrationMetadata{
		MigrationID: coordinatorMigrationID, Identifier: "Player", Password: "secret-password", Username: "Player",
		ProfileUUID: coordinatorProfileID, ProfileName: "Player", ProfileModel: "default", SkinSHA256: request.Metadata.SkinSHA256,
		CapeSHA256: request.Metadata.CapeSHA256, LegacyUserProperties: conflict.Metadata.LegacyUserProperties,
	}, request.Skin.PNG, request.Cape.PNG)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Commit(context.Background(), conflict); !migrationErrorCode(err, "migration_conflict") {
		t.Fatalf("digest conflict error = %v", err)
	}
}

func TestMigrationCoordinatorCommitWithoutAssets(t *testing.T) {
	coordinator, _, _, _, verifier, _ := setupMigrationCoordinator(t)
	result, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if result.Assets.Skin.Status != "absent" || result.Assets.Cape.Status != "absent" || verifier.applyCalls != 0 {
		t.Fatalf("unexpected absent asset result: %+v", result)
	}
	if len(result.User.Properties) != 3 {
		t.Fatalf("unexpected user properties: %+v", result.User.Properties)
	}
	var textureProperties int64
	if err := database.DB.Model(&models.ProfileProperty{}).Where("name = ?", "textures").Count(&textureProperties).Error; err != nil || textureProperties != 0 {
		t.Fatalf("unexpected texture properties: count=%d err=%v", textureProperties, err)
	}
}

func TestMigrationCoordinatorFailureCompensatesAndRetryPreservesCoreOwnership(t *testing.T) {
	coordinator, _, _, state, verifier, _ := setupMigrationCoordinator(t)
	request := coordinatorRequest(t, true)
	verifier.failApply = errors.New("storage unavailable")
	if _, err := coordinator.Commit(context.Background(), request); !migrationErrorCode(err, "migration_commit_failed") {
		t.Fatalf("commit error = %v", err)
	}
	assertNoMigrationLocalResources(t)
	var journal models.MigrationJournal
	if err := database.DB.First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "failed" || journal.Stage != "asset_apply" || journal.RolledBackAt == nil || !journal.CoreCreated {
		t.Fatalf("unexpected failed journal: %+v", journal)
	}
	verifier.failApply = nil
	result, err := coordinator.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CreatedCoreUser || result.ReusedCoreUser || state.claimCalls != 2 {
		t.Fatalf("retry lost Core ownership: result=%+v claims=%d", result, state.claimCalls)
	}
}

func TestMigrationCoordinatorCompensationFailsClosedAfterOwnedAccountMutation(t *testing.T) {
	coordinator, _, _, _, verifier, _ := setupMigrationCoordinator(t)
	verifier.failApply = errors.New("storage unavailable")
	verifier.onApply = func() {
		if err := database.DB.Model(&models.Account{}).Where("1 = 1").Update("skin_sync", true).Error; err != nil {
			t.Errorf("mutate account: %v", err)
		}
	}
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, true)); !migrationErrorCode(err, "migration_commit_failed") {
		t.Fatalf("commit error = %v", err)
	}
	var journal models.MigrationJournal
	if err := database.DB.First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "failed" || journal.RolledBackAt != nil {
		t.Fatalf("modified owned account was compensated: %+v", journal)
	}
	var accountCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil || accountCount != 1 {
		t.Fatalf("modified account should remain for reconcile: count=%d err=%v", accountCount, err)
	}
}

func TestMigrationCoordinatorPreflightsProfileConflictBeforeCoreClaim(t *testing.T) {
	coordinator, _, _, state, _, _ := setupMigrationCoordinator(t)
	if err := database.DB.Create(&models.Profile{ID: strings.Repeat("b", 32), AccountID: 99, Name: "Player", Model: "default"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, false)); !migrationErrorCode(err, "migration_profile_conflict") {
		t.Fatalf("commit error = %v", err)
	}
	if state.claimCalls != 0 {
		t.Fatalf("Core claim ran before target preflight: %d", state.claimCalls)
	}
}

func TestMigrationCoordinatorCacheFailureKeepsCommittedResources(t *testing.T) {
	coordinator, _, results, _, verifier, _ := setupMigrationCoordinator(t)
	results.failSave = errors.New("Redis unavailable")
	request := coordinatorRequest(t, true)
	if _, err := coordinator.Commit(context.Background(), request); !migrationErrorCode(err, "migration_commit_failed") {
		t.Fatalf("commit error = %v", err)
	}
	var journal models.MigrationJournal
	if err := database.DB.First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if journal.Status != "committed" {
		t.Fatalf("cache failure rolled back committed migration: %+v", journal)
	}
	var accountCount int64
	if err := database.DB.Model(&models.Account{}).Count(&accountCount).Error; err != nil || accountCount != 1 {
		t.Fatalf("committed account count=%d err=%v", accountCount, err)
	}
	var validTokens int64
	if err := database.DB.Model(&models.Token{}).Where("state = ?", "valid").Count(&validTokens).Error; err != nil || validTokens != 0 {
		t.Fatalf("failed finalization left valid token: count=%d err=%v", validTokens, err)
	}
	results.failSave = nil
	result, err := coordinator.Commit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if verifier.applyCalls != 2 || result.Session.AccessToken == "" {
		t.Fatalf("committed retry reapplied assets: calls=%d", verifier.applyCalls)
	}
}

func TestMigrationCoordinatorRejectsTamperedAuthoritativeTextureURL(t *testing.T) {
	coordinator, _, _, _, verifier, _ := setupMigrationCoordinator(t)
	verifier.badURL = true
	if _, err := coordinator.Commit(context.Background(), coordinatorRequest(t, true)); !migrationErrorCode(err, "migration_asset_conflict") {
		t.Fatalf("commit error = %v", err)
	}
	assertNoMigrationLocalResources(t)
}

func migrationErrorCode(err error, code string) bool {
	var migrationErr *MigrationError
	return errors.As(err, &migrationErr) && migrationErr.Code == code
}

func assertNoMigrationLocalResources(t *testing.T) {
	t.Helper()
	for _, model := range []any{&models.Account{}, &models.Profile{}, &models.ProfileProperty{}, &models.Token{}} {
		var count int64
		if err := database.DB.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("compensation left %T rows: %d", model, count)
		}
	}
}
