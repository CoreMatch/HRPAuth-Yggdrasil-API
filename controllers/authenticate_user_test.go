package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	legacyredis "github.com/lnb/HRPAuth-Yggdrasil-API/redis"
	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAuthenticateReturnsSelectedProfileUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB, oldConfig, oldRedis := database.DB, config.AppConfig, legacyredis.Client
	t.Cleanup(func() {
		database.DB, config.AppConfig, legacyredis.Client = oldDB, oldConfig, oldRedis
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	database.DB = db
	config.AppConfig = &config.Config{}
	for _, statement := range []string{
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY AUTOINCREMENT, core_user_id varchar(32), mojang_uuid varchar(32), password varchar(255), mbe BOOLEAN NOT NULL DEFAULT 0, cbh BOOLEAN NOT NULL DEFAULT 1, skin_sync BOOLEAN NOT NULL DEFAULT 1, skin_sync_mark varchar(128), last_sign_at datetime, created_at datetime, updated_at datetime)`,
		`CREATE TABLE profiles (id varchar(32) PRIMARY KEY, account_id INTEGER NOT NULL, name varchar(30) NOT NULL UNIQUE, model varchar(16) DEFAULT 'default', created_at datetime, updated_at datetime)`,
		`CREATE TABLE profile_properties (id INTEGER PRIMARY KEY AUTOINCREMENT, profile_id varchar(32), name varchar(255), value text, signature text, delete_when bigint NOT NULL DEFAULT 0)`,
		`CREATE TABLE tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, access_token varchar(255) UNIQUE, client_token varchar(255), account_id INTEGER, selected_profile_id varchar(32), issued_at bigint, expires_in_days INTEGER, state varchar(32), created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	config.AppConfig.Yggdrasil.Security.TokenExpiryDays = 15
	config.AppConfig.Yggdrasil.Security.MaxTokensPerUser = 10
	config.AppConfig.Callback.URL = "https://textures.example"
	legacyredis.Client = goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1})
	defer legacyredis.Client.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("secret-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account := models.Account{Password: string(passwordHash), CBH: false, SkinSync: false, RegisterAt: time.Now()}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&account).Updates(map[string]any{"cbh": false, "skin_sync": false}).Error; err != nil {
		t.Fatal(err)
	}
	account.CBH = false
	account.SkinSync = false
	profile := models.Profile{ID: "0123456789abcdef0123456789abcdef", AccountID: account.ID, Name: "LegacyPlayer", Model: "default"}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ProfileProperty{ProfileID: profile.ID, Name: "legacy_rank", Value: "member"}).Error; err != nil {
		t.Fatal(err)
	}
	controller := NewYggdrasilController()
	router := gin.New()
	router.POST("/authenticate", controller.Authenticate)

	authenticate := func(requestUser bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"username": profile.Name, "password": "secret-password", "requestUser": requestUser})
		request := httptest.NewRequest(http.MethodPost, "/authenticate", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	response := authenticate(true)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticate status = %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		SelectedProfile services.ProfileInfo `json:"selectedProfile"`
		User            *struct {
			ID         string `json:"id"`
			Properties []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"properties"`
		} `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SelectedProfile.ID != profile.ID || payload.User == nil || payload.User.ID != profile.ID {
		t.Fatalf("user ID does not match selected profile: %+v", payload)
	}
	properties := map[string]string{}
	for _, property := range payload.User.Properties {
		properties[property.Name] = property.Value
	}
	if properties["legacy_rank"] != "member" || properties["uploadableTextures"] != "skin,cape" {
		t.Fatalf("authoritative properties missing: %+v", properties)
	}

	withoutUser := authenticate(false)
	if withoutUser.Code != http.StatusOK || bytes.Contains(withoutUser.Body.Bytes(), []byte(`"user"`)) {
		t.Fatalf("requestUser=false returned user: %s", withoutUser.Body.String())
	}

	if err := db.Create(&models.ProfileProperty{ProfileID: profile.ID, Name: "textures", Value: "not-base64", DeleteWhen: 0}).Error; err != nil {
		t.Fatal(err)
	}
	failed := authenticate(true)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("malformed properties did not fail closed: %d %s", failed.Code, failed.Body.String())
	}
}
