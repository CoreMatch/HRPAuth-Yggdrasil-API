package services

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

type TextureService struct{}

func NewTextureService() *TextureService {
	return &TextureService{}
}

type TextureInfo struct {
	URL      string                 `json:"url"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type TexturesPayload struct {
	Timestamp   int64                  `json:"timestamp"`
	ProfileID   string                 `json:"profileId"`
	ProfileName string                 `json:"profileName"`
	Textures    map[string]TextureInfo `json:"textures"`
}

type TextureValidationResult struct {
	Data     []byte
	Notices  []string
	Warnings []string
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (ts *TextureService) ValidateTexture(file io.Reader, textureType string, model string) (*TextureValidationResult, error) {
	cfg := config.AppConfig.Yggdrasil.Security
	maxWidth := cfg.MaxTextureWidth
	maxHeight := cfg.MaxTextureHeight

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read texture data: %v", err)
	}

	if cfg.MaxTextureFileSize > 0 && int64(len(data)) > cfg.MaxTextureFileSize {
		return nil, fmt.Errorf("texture file size %d bytes exceeds maximum allowed size %d bytes", len(data), cfg.MaxTextureFileSize)
	}

	reader := bytes.NewReader(data)
	cfgImg, format, err := image.DecodeConfig(reader)
	if err != nil {
		return nil, fmt.Errorf("invalid image format: %v", err)
	}

	if format != "png" {
		return nil, fmt.Errorf("texture must be PNG format")
	}

	width := cfgImg.Width
	height := cfgImg.Height

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode texture: %v", err)
	}

	bounds := img.Bounds()
	actualWidth := bounds.Dx()
	actualHeight := bounds.Dy()

	result := &TextureValidationResult{}

	switch textureType {
	case "skin":
		if !isValidSkinSize(actualWidth, actualHeight) {
			if isProportional(actualWidth, actualHeight) {
				result.Notices = append(result.Notices,
					fmt.Sprintf("skin size %dx%d exceeds standard size, but has a valid aspect ratio", actualWidth, actualHeight))
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("skin size %dx%d does not match standard proportions", actualWidth, actualHeight))
			}
		}
	case "cape":
		if !isValidCapeSize(actualWidth, actualHeight) {
			if isProportional(actualWidth, actualHeight) {
				result.Notices = append(result.Notices,
					fmt.Sprintf("cape size %dx%d exceeds standard size, but has a valid aspect ratio", actualWidth, actualHeight))
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("cape size %dx%d does not match standard proportions", actualWidth, actualHeight))
			}
		}
		if actualWidth == 22 && actualHeight == 17 {
			img = resizeCapeToStandard(img)
		}
	default:
		return nil, fmt.Errorf("invalid texture type: %s", textureType)
	}

	if width > maxWidth || height > maxHeight {
		if isProportional(width, height) {
			result.Notices = append(result.Notices,
				fmt.Sprintf("texture resolution %dx%d exceeds limit %dx%d, but has a valid aspect ratio", width, height, maxWidth, maxHeight))
		} else {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("texture resolution %dx%d exceeds limit %dx%d and has non-standard proportions", width, height, maxWidth, maxHeight))
		}
	}

	resultBuf := new(bytes.Buffer)
	if err := png.Encode(resultBuf, img); err != nil {
		return nil, fmt.Errorf("failed to re-encode texture: %v", err)
	}

	result.Data = resultBuf.Bytes()
	return result, nil
}

func isValidSkinSize(width, height int) bool {
	return (width == 64 && height == 32) || (width == 64 && height == 64)
}

func isValidCapeSize(width, height int) bool {
	return (width == 64 && height == 32) || (width == 22 && height == 17)
}

func isProportional(width, height int) bool {
	g := gcd(width, height)
	return (width/g == 2 && height/g == 1) || (width/g == 1 && height/g == 1)
}

func resizeCapeToStandard(img image.Image) image.Image {
	newImg := image.NewRGBA(image.Rect(0, 0, 64, 32))
	draw.Draw(newImg, newImg.Bounds(), image.Transparent, image.Point{}, draw.Src)
	draw.Draw(newImg, img.Bounds(), img, image.Point{}, draw.Src)
	return newImg
}

func (ts *TextureService) CalculateHash(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (ts *TextureService) SaveTexture(data []byte, hash string) error {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./"
	}

	texturesDir := filepath.Join(storageDir, "textures")
	if err := os.MkdirAll(texturesDir, 0755); err != nil {
		return fmt.Errorf("failed to create textures directory: %v", err)
	}

	filePath := filepath.Join(texturesDir, hash)
	return os.WriteFile(filePath, data, 0644)
}

func (ts *TextureService) DeleteTexture(hash string) error {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./"
	}

	filePath := filepath.Join(storageDir, "textures", hash)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete texture file: %v", err)
	}
	return nil
}

func (ts *TextureService) GetTexturePath(hash string) (string, error) {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./"
	}

	filePath := filepath.Join(storageDir, "textures", hash)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("texture not found")
	}
	return filePath, nil
}

func (ts *TextureService) UploadTexture(accessToken, profileID, textureType, model string, fileData []byte) ([]string, error) {
	token := NewAuthService().ValidateToken(accessToken, "")
	if token == nil {
		return nil, fmt.Errorf("invalid access token")
	}

	if !NewAuthService().IsProfileOwnedByUser(profileID, token.UserID) {
		return nil, fmt.Errorf("profile not owned by user")
	}

	validated, err := ts.ValidateTexture(strings.NewReader(string(fileData)), textureType, model)
	if err != nil {
		return nil, err
	}

	hash := ts.CalculateHash(validated.Data)

	if err := ts.SaveTexture(validated.Data, hash); err != nil {
		return nil, err
	}

	callbackURL := config.AppConfig.Callback.URL
	textureURL := strings.TrimRight(callbackURL, "/") + "/textures/" + hash

	if err := ts.UpdateProfileTexture(profileID, textureType, textureURL, model); err != nil {
		return nil, err
	}

	warnings := append(validated.Notices, validated.Warnings...)
	return warnings, nil
}

func (ts *TextureService) UpdateProfileTexture(profileID, textureType, textureURL, model string) error {
	payload := ts.GenerateTexturesPayload(profileID, textureType, textureURL, model)
	value := base64.StdEncoding.EncodeToString([]byte(payload))

	signature, err := ts.SignTextureValue(value)
	if err != nil {
		return err
	}

	return database.DB.Transaction(func(tx *gorm.DB) error {
		var existingProp models.ProfileProperty
		if err := tx.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").First(&existingProp).Error; err == nil {
			deleteWhen := time.Now().Unix() + 7*86400
			tombstoneValue := ts.createTombstoneValue(&existingProp, textureType)

			if err := tx.Model(&existingProp).Updates(map[string]interface{}{
				"delete_when": deleteWhen,
				"value":       tombstoneValue,
				"signature":   "",
			}).Error; err != nil {
				return fmt.Errorf("failed to mark old profile property as tombstone: %v", err)
			}
		}

		newProp := models.ProfileProperty{
			ProfileID:  profileID,
			Name:       "textures",
			Value:      value,
			Signature:  signature,
			DeleteWhen: 0,
		}
		if err := tx.Create(&newProp).Error; err != nil {
			return fmt.Errorf("failed to create new profile property: %v", err)
		}
		return nil
	})
}

func (ts *TextureService) createTombstoneValue(prop *models.ProfileProperty, textureType string) string {
	decoded, err := base64.StdEncoding.DecodeString(prop.Value)
	if err != nil {
		return prop.Value
	}
	var payload TexturesPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return prop.Value
	}

	info, ok := payload.Textures[strings.ToUpper(textureType)]
	if !ok {
		return prop.Value
	}

	newPayload := TexturesPayload{
		Timestamp:   payload.Timestamp,
		ProfileID:   payload.ProfileID,
		ProfileName: payload.ProfileName,
		Textures: map[string]TextureInfo{
			strings.ToUpper(textureType): info,
		},
	}
	data, _ := json.Marshal(newPayload)
	return base64.StdEncoding.EncodeToString(data)
}

func (ts *TextureService) GenerateTexturesPayload(profileID, textureType, textureURL, model string) string {
	var props map[string]TextureInfo

	var existingProp models.ProfileProperty
	database.DB.
		Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").
		First(&existingProp)

	if existingProp.ID != 0 {
		decoded, _ := base64.StdEncoding.DecodeString(existingProp.Value)
		var existingPayload TexturesPayload
		if err := json.Unmarshal(decoded, &existingPayload); err == nil {
			props = existingPayload.Textures
		}
	}

	if props == nil {
		props = make(map[string]TextureInfo)
	}

	metadata := make(map[string]interface{})
	if textureType == "skin" && model != "" {
		metadata["model"] = model
	}

	props[strings.ToUpper(textureType)] = TextureInfo{
		URL:      textureURL,
		Metadata: metadata,
	}

	authService := NewAuthService()
	profile := authService.GetProfileByID(profileID)
	profileName := ""
	if profile != nil {
		profileName = profile.Name
	}

	payload := TexturesPayload{
		Timestamp:   time.Now().UnixMilli(),
		ProfileID:   profileID,
		ProfileName: profileName,
		Textures:    props,
	}

	data, _ := json.Marshal(payload)
	return string(data)
}

func (ts *TextureService) SignTextureValue(value string) (string, error) {
	privateKeyPEM := config.AppConfig.Yggdrasil.Server.SignaturePrivateKey
	if privateKeyPEM == "" {
		return "", fmt.Errorf("signature private key not configured")
	}

	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return "", fmt.Errorf("invalid RSA private key format")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %v", err)
	}

	hashed := sha1.Sum([]byte(value))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA1, hashed[:])
	if err != nil {
		return "", fmt.Errorf("failed to sign texture value: %v", err)
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

func (ts *TextureService) GetProfileProperties(profileID string, profileName string, unsigned bool) ([]models.ProfileProperty, error) {
	var props []models.ProfileProperty
	result := database.DB.
		Where("profile_id = ? AND (name != ? OR delete_when = 0)", profileID, "textures").
		Find(&props)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get profile properties: %v", result.Error)
	}

	hasUploadable := false
	for _, p := range props {
		if p.Name == "uploadableTextures" {
			hasUploadable = true
			break
		}
	}

	if !hasUploadable {
		uploadable := models.ProfileProperty{
			ProfileID: profileID,
			Name:      "uploadableTextures",
			Value:     "skin,cape",
			Signature: "",
		}
		props = append(props, uploadable)
	}

	if unsigned {
		for i := range props {
			props[i].Signature = ""
		}
	}

	return props, nil
}

func (ts *TextureService) GetSkinTexturePathByProfileName(name string) (string, error) {
	var profile models.Profile
	if err := database.DB.Where("name = ?", name).First(&profile).Error; err != nil {
		return "", fmt.Errorf("profile not found")
	}

	var prop models.ProfileProperty
	if err := database.DB.
		Where("profile_id = ? AND name = ? AND delete_when = 0", profile.ID, "textures").
		First(&prop).Error; err != nil {
		return "", fmt.Errorf("texture not found")
	}

	decoded, err := base64.StdEncoding.DecodeString(prop.Value)
	if err != nil {
		return "", fmt.Errorf("failed to decode texture property: %v", err)
	}

	var payload TexturesPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return "", fmt.Errorf("failed to unmarshal texture payload: %v", err)
	}

	info, ok := payload.Textures["SKIN"]
	if !ok {
		return "", fmt.Errorf("skin not found")
	}

	parts := strings.Split(info.URL, "/textures/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid texture url")
	}
	hash := parts[len(parts)-1]
	if hash == "" {
		return "", fmt.Errorf("invalid texture hash")
	}

	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./"
	}
	filePath := filepath.Join(storageDir, "textures", hash)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("texture file missing")
	}
	return filePath, nil
}

func (ts *TextureService) CleanupOrphanedTextures() int {
	var orphans []models.ProfileProperty
	now := time.Now().Unix()
	if err := database.DB.
		Where("name = ? AND delete_when > 0 AND delete_when <= ?", "textures", now).
		Find(&orphans).Error; err != nil {
		log.Printf("[TextureCleanup] failed to query orphaned textures: %v", err)
		return 0
	}

	deleted := 0
	for _, orphan := range orphans {
		decoded, err := base64.StdEncoding.DecodeString(orphan.Value)
		if err == nil {
			var payload TexturesPayload
			if err := json.Unmarshal(decoded, &payload); err == nil {
				for _, info := range payload.Textures {
					parts := strings.Split(info.URL, "/textures/")
					if len(parts) >= 2 {
						ts.DeleteTexture(parts[len(parts)-1])
					}
				}
			}
		}
		if err := database.DB.Delete(&orphan).Error; err != nil {
			log.Printf("[TextureCleanup] failed to delete orphan row %d: %v", orphan.ID, err)
			continue
		}
		deleted++
	}

	return deleted
}
