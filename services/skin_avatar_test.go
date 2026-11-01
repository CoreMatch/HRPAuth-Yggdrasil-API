package services

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testSkin(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := width / 8; y < width/4 && y < height; y++ {
		for x := width / 8; x < width/4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, A: 255})
		}
		for x := width * 5 / 8; x < width*3/4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{B: 200, A: 128})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestGenerateSkinAvatar(t *testing.T) {
	for _, dimensions := range [][2]int{{64, 64}, {64, 32}, {128, 64}, {65, 40}, {8, 2}} {
		t.Run(fmt.Sprintf("%dx%d", dimensions[0], dimensions[1]), func(t *testing.T) {
			data, err := generateSkinAvatar(testSkin(t, dimensions[0], dimensions[1]))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() < 64 || img.Bounds().Dx() != img.Bounds().Dy() {
				t.Fatalf("unexpected avatar bounds: %v", img.Bounds())
			}
			got := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
			if got.A != 255 || got.R < 98 || got.R > 101 || got.B < 99 || got.B > 102 {
				t.Fatalf("face and overlay were not composited: %#v", got)
			}
		})
	}
	if _, err := generateSkinAvatar(testSkin(t, 7, 2)); err == nil {
		t.Fatal("skin without enough pixels should be rejected")
	}
	if _, err := generateSkinAvatar(testSkin(t, 64, 15)); err == nil {
		t.Fatal("skin without a complete face should be rejected")
	}
}

func setupTextureTest(t *testing.T) *TextureService {
	t.Helper()
	oldDB, oldConfig, oldLock := database.DB, config.AppConfig, acquireTextureStorageLock
	config.AppConfig = &config.Config{}
	config.AppConfig.Yggdrasil.Server.TexturesStorage = t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	config.AppConfig.Yggdrasil.Server.SignaturePrivateKey = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	var db *gorm.DB
	dsn := os.Getenv("TEST_TEXTURE_MYSQL_DSN")
	if dsn != "" {
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		config.AppConfig.Database.TablePrefix = "test_" + hex.EncodeToString(suffix[:]) + "_"
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	}
	if err != nil {
		t.Fatal(err)
	}
	database.DB = db
	acquireTextureStorageLock = func() (func(), error) { return func() {}, nil }
	if err := db.AutoMigrate(&models.ProfileProperty{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dsn != "" {
			if err := db.Migrator().DropTable(&models.ProfileProperty{}); err != nil {
				t.Errorf("failed to remove test table: %v", err)
			}
		}
		sqlDB, _ := db.DB()
		sqlDB.Close()
		database.DB, config.AppConfig, acquireTextureStorageLock = oldDB, oldConfig, oldLock
	})
	return NewTextureService()
}

func textureProperty(t *testing.T, profile string, deleteWhen int64, textures map[string]models.TextureInfo) models.ProfileProperty {
	t.Helper()
	data, err := json.Marshal(models.TexturePayload{ProfileID: profile, Textures: textures})
	if err != nil {
		t.Fatal(err)
	}
	return models.ProfileProperty{ProfileID: profile, Name: "textures", Value: base64.StdEncoding.EncodeToString(data), DeleteWhen: deleteWhen}
}

func TestRemoveSkinAndCleanupSharedTexture(t *testing.T) {
	ts := setupTextureTest(t)
	skin := testSkin(t, 64, 64)
	hash := ts.CalculateHash(skin)
	if err := ts.SaveTexture(skin, hash); err != nil {
		t.Fatal(err)
	}
	avatar, err := generateSkinAvatar(skin)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.SavePreview(avatar, avatarFileName(hash)); err != nil {
		t.Fatal(err)
	}
	info := models.TextureInfo{URL: "https://example.test/textures/" + hash}
	cape := models.TextureInfo{URL: "https://example.test/textures/" + strings.Repeat("b", 64)}
	first := textureProperty(t, "first", 0, map[string]models.TextureInfo{"SKIN": info, "CAPE": cape})
	second := textureProperty(t, "second", 0, map[string]models.TextureInfo{"SKIN": info})
	for _, prop := range []*models.ProfileProperty{&first, &second} {
		if err := database.DB.Create(prop).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := ts.RemoveProfileTexture("first", "skin"); err != nil {
		t.Fatal(err)
	}
	textures, err := ts.GetTextures("first")
	if err != nil || len(textures) != 1 || textures["CAPE"].URL != cape.URL {
		t.Fatalf("cape should remain active: %#v, %v", textures, err)
	}
	if _, err := ts.GetProfileSkinAvatar("first"); err != ErrSkinAvatarNotFound {
		t.Fatalf("deleted skin should not have an active avatar: %v", err)
	}
	database.DB.Model(&models.ProfileProperty{}).Where("profile_id = ? AND delete_when > 0", "first").Update("delete_when", time.Now().Unix()-1)
	if got := ts.CleanupOrphanedTextures(); got != 1 {
		t.Fatalf("expected one cleaned record, got %d", got)
	}
	if _, err := ts.GetProfileSkinAvatar("second"); err != nil {
		t.Fatalf("shared active avatar must survive cleanup: %v", err)
	}
	if err := ts.RemoveProfileTexture("second", "skin"); err != nil {
		t.Fatal(err)
	}
	database.DB.Model(&models.ProfileProperty{}).Where("profile_id = ? AND delete_when > 0", "second").Update("delete_when", time.Now().Unix()-1)
	if got := ts.CleanupOrphanedTextures(); got != 1 {
		t.Fatalf("expected last record cleaned, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(config.AppConfig.Yggdrasil.Server.TexturesStorage, texturesSubDir, hash)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced skin was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.AppConfig.Yggdrasil.Server.TexturesStorage, previewsSubDir, avatarFileName(hash))); !os.IsNotExist(err) {
		t.Fatalf("unreferenced avatar was not removed: %v", err)
	}
}

func TestCleanupPreservesFutureTombstoneAndMalformedReferences(t *testing.T) {
	ts := setupTextureTest(t)
	skin := testSkin(t, 64, 64)
	hash := ts.CalculateHash(skin)
	if err := ts.SaveTexture(skin, hash); err != nil {
		t.Fatal(err)
	}
	info := models.TextureInfo{URL: "https://example.test/textures/" + hash}
	expired := textureProperty(t, "old", time.Now().Unix()-1, map[string]models.TextureInfo{"SKIN": info})
	future := textureProperty(t, "future", time.Now().Unix()+3600, map[string]models.TextureInfo{"SKIN": info})
	database.DB.Create(&expired)
	database.DB.Create(&future)
	malformed := models.ProfileProperty{ProfileID: "bad", Name: "textures", Value: "invalid", DeleteWhen: 0}
	database.DB.Create(&malformed)
	if got := ts.CleanupOrphanedTextures(); got != 0 {
		t.Fatalf("invalid retained data must block cleanup, got %d", got)
	}
	database.DB.Delete(&malformed)
	if got := ts.CleanupOrphanedTextures(); got != 1 {
		t.Fatalf("expected expired record cleaned, got %d", got)
	}
	if _, err := ts.GetTexturePath(hash); err != nil {
		t.Fatalf("future tombstone must retain texture: %v", err)
	}
}

func TestExistingSkinAvatarIsGeneratedOnFirstAccess(t *testing.T) {
	ts := setupTextureTest(t)
	skin := testSkin(t, 64, 32)
	hash := ts.CalculateHash(skin)
	if err := ts.SaveTexture(skin, hash); err != nil {
		t.Fatal(err)
	}
	prop := textureProperty(t, "existing", 0, map[string]models.TextureInfo{
		"SKIN": {URL: "https://example.test/textures/" + hash},
	})
	if err := database.DB.Create(&prop).Error; err != nil {
		t.Fatal(err)
	}
	first, err := ts.GetProfileSkinAvatar("existing")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ts.GetProfileSkinAvatar("existing")
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("expected stored avatar on subsequent access: %v", err)
	}
}

func TestLocalTextureHashValidation(t *testing.T) {
	hash := strings.Repeat("a", 64)
	tests := map[string]string{
		"https://example.test/yggdrasil/textures/" + hash:          hash,
		"https://example.test/textures/" + hash + "?version=1":     hash,
		"https://example.test/textures/../" + hash:                 "",
		"https://example.test/textures/" + strings.Repeat("g", 64): "",
		"https://example.test/textures/" + strings.ToUpper(hash):   "",
		"https://example.test/textures/" + hash + "/extra":         "",
	}
	for input, expected := range tests {
		if got := localTextureHash(input); got != expected {
			t.Errorf("localTextureHash(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestMySQLTextureStorageLock(t *testing.T) {
	dsn := os.Getenv("TEST_TEXTURE_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_TEXTURE_MYSQL_DSN is not set")
	}
	oldDB, oldConfig := database.DB, config.AppConfig
	config.AppConfig = &config.Config{}
	config.AppConfig.Database.DBName = "avatar_lock_test"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	database.DB = db
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
		database.DB, config.AppConfig = oldDB, oldConfig
	})
	unlock, err := lockTextureStorage()
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 1)
	go func() {
		release, err := lockTextureStorage()
		if err == nil {
			release()
		}
		results <- err
	}()
	select {
	case err := <-results:
		unlock()
		t.Fatalf("second connection should wait for the first lock, got %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("second connection could not acquire the released lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second connection did not acquire the released lock")
	}
}
