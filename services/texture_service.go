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
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

type TextureService struct{}

func NewTextureService() *TextureService {
	return &TextureService{}
}

const (
	previewScale   = 8
	texturesSubDir = "textures"
	previewsSubDir = "previews"
)

type TextureValidationResult struct {
	Data     []byte
	Width    int
	Height   int
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
	_, format, err := image.DecodeConfig(reader)
	if err != nil {
		return nil, fmt.Errorf("invalid image format: %v", err)
	}

	if format != "png" {
		return nil, fmt.Errorf("texture must be PNG format")
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode texture: %v", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	result := &TextureValidationResult{
		Width:  width,
		Height: height,
	}

	switch textureType {
	case "skin":
		if _, err := skinAvatarBounds(width, height); err != nil {
			return nil, err
		}
		if !isValidSkinSize(width, height) {
			if isProportional(width, height) {
				result.Notices = append(result.Notices,
					fmt.Sprintf("skin size %dx%d exceeds standard size, but has a valid aspect ratio", width, height))
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("skin size %dx%d does not match standard proportions", width, height))
			}
		}
	case "cape":
		if !isValidCapeSize(width, height) {
			if isProportional(width, height) {
				result.Notices = append(result.Notices,
					fmt.Sprintf("cape size %dx%d exceeds standard size, but has a valid aspect ratio", width, height))
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("cape size %dx%d does not match standard proportions", width, height))
			}
		}
		if width == 22 && height == 17 {
			img = resizeCapeToStandard(img)
			result.Width = 64
			result.Height = 32
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
		storageDir = "./storage"
	}

	texturesDir := filepath.Join(storageDir, texturesSubDir)
	if err := os.MkdirAll(texturesDir, 0755); err != nil {
		return fmt.Errorf("failed to create textures directory: %v", err)
	}

	filePath := filepath.Join(texturesDir, hash)
	return os.WriteFile(filePath, data, 0644)
}

func (ts *TextureService) SavePreview(data []byte, fileName string) error {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./storage"
	}

	previewsDir := filepath.Join(storageDir, previewsSubDir)
	if err := os.MkdirAll(previewsDir, 0755); err != nil {
		return fmt.Errorf("failed to create previews directory: %v", err)
	}

	filePath := filepath.Join(previewsDir, fileName)
	return os.WriteFile(filePath, data, 0644)
}

func (ts *TextureService) DeleteTexture(hash string) error {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./storage"
	}

	filePath := filepath.Join(storageDir, texturesSubDir, hash)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete texture file: %v", err)
	}
	return nil
}

func (ts *TextureService) DeletePreview(fileName string) error {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./storage"
	}

	filePath := filepath.Join(storageDir, previewsSubDir, fileName)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete preview file: %v", err)
	}
	return nil
}

func (ts *TextureService) GetTexturePath(hash string) (string, error) {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./storage"
	}

	filePath := filepath.Join(storageDir, texturesSubDir, hash)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("texture not found")
	}
	return filePath, nil
}

func (ts *TextureService) GetEffectiveCallbackURL() string {
	// If callback URL is explicitly configured, use it
	if config.AppConfig.Callback.URL != "" {
		return strings.TrimRight(config.AppConfig.Callback.URL, "/")
	}

	// Otherwise, automatically construct it: SiteURL + RelayPathPrefix
	siteURL := strings.TrimRight(config.AppConfig.Runtime.SiteURL, "/")
	prefix := config.AppConfig.Microservice.RelayPathPrefix
	if prefix == "" {
		prefix = config.DefaultRelayPathPrefix
	}
	prefix = "/" + strings.Trim(prefix, "/")

	return siteURL + prefix
}

func (ts *TextureService) UploadTexture(accessToken, profileID, textureType, model string, fileData []byte) ([]string, error) {
	token := NewAuthService().ValidateToken(accessToken, "")
	if token == nil {
		return nil, fmt.Errorf("invalid access token")
	}

	if !NewAuthService().IsProfileOwnedByAccount(profileID, token.AccountID) {
		return nil, fmt.Errorf("profile not owned by user")
	}

	validated, err := ts.ValidateTexture(bytes.NewReader(fileData), textureType, model)
	if err != nil {
		return nil, err
	}

	var avatarData []byte
	if textureType == "skin" {
		avatarData, err = generateSkinAvatar(validated.Data)
		if err != nil {
			return nil, err
		}
	}

	unlock, err := acquireTextureStorageLock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	hash := ts.CalculateHash(validated.Data)

	if err := ts.SaveTexture(validated.Data, hash); err != nil {
		return nil, err
	}
	if textureType == "skin" {
		if err := ts.SavePreview(avatarData, avatarFileName(hash)); err != nil {
			return nil, fmt.Errorf("failed to save skin avatar: %w", err)
		}
	}

	// Generate and save preview
	previewData, err := ts.GeneratePreviewImage(validated.Data, textureType, model)
	previewFileName := ""
	if err == nil {
		previewFileName = hash + "_" + textureType + ".webp"
		if err := ts.SavePreview(previewData, previewFileName); err != nil {
			log.Printf("Warning: failed to save texture preview: %v", err)
		}
	} else {
		log.Printf("Warning: failed to generate texture preview: %v", err)
	}

	// Update profile active texture
	callbackURL := ts.GetEffectiveCallbackURL()
	textureURL := callbackURL + "/textures/" + hash

	if err := ts.UpdateProfileTexture(profileID, textureType, textureURL, model); err != nil {
		return nil, err
	}

	warnings := append(validated.Notices, validated.Warnings...)
	return warnings, nil
}

func (ts *TextureService) UpdateProfileTexture(profileID, textureType, textureURL, model string) error {
	payload := ts.GenerateTexturePayload(profileID, textureType, textureURL, model)
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

func (ts *TextureService) RemoveProfileTexture(profileID, textureType string) error {
	if textureType != "skin" && textureType != "cape" {
		return fmt.Errorf("invalid texture type: %s", textureType)
	}
	unlock, err := acquireTextureStorageLock()
	if err != nil {
		return err
	}
	defer unlock()

	return database.DB.Transaction(func(tx *gorm.DB) error {
		removed, err := ts.removeProfileTextureTx(tx, profileID, textureType)
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("texture not found")
		}
		return nil
	})
}

// removeProfileTextureTx is the transactional core of RemoveProfileTexture,
// callable from flows that already hold the texture-storage lock. It
// returns whether an active binding of that type existed (and was removed);
// a profile without the texture is not an error.
func (ts *TextureService) removeProfileTextureTx(tx *gorm.DB, profileID, textureType string) (bool, error) {
	var existingProp models.ProfileProperty
	if err := tx.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").First(&existingProp).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("textures property not found: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(existingProp.Value)
	if err != nil {
		return false, fmt.Errorf("failed to decode textures property: %w", err)
	}
	var payload models.TexturePayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return false, fmt.Errorf("failed to unmarshal textures payload: %w", err)
	}
	key := strings.ToUpper(textureType)
	if _, ok := payload.Textures[key]; !ok {
		return false, nil
	}
	tombstoneValue := ts.createTombstoneValue(&existingProp, textureType)
	delete(payload.Textures, key)
	deleteWhen := time.Now().Unix() + 7*86400
	if len(payload.Textures) == 0 {
		err := tx.Model(&existingProp).Updates(map[string]interface{}{
			"value": tombstoneValue, "signature": "", "delete_when": deleteWhen,
		}).Error
		return true, err
	}
	newData, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	newValue := base64.StdEncoding.EncodeToString(newData)
	signature, err := ts.SignTextureValue(newValue)
	if err != nil {
		return false, err
	}
	if err := tx.Model(&existingProp).Updates(map[string]interface{}{
		"value": newValue, "signature": signature,
	}).Error; err != nil {
		return false, err
	}
	return true, tx.Create(&models.ProfileProperty{
		ProfileID: profileID, Name: "textures", Value: tombstoneValue, DeleteWhen: deleteWhen,
	}).Error
}

func (ts *TextureService) createTombstoneValue(prop *models.ProfileProperty, textureType string) string {
	decoded, err := base64.StdEncoding.DecodeString(prop.Value)
	if err != nil {
		return prop.Value
	}
	var payload models.TexturePayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return prop.Value
	}

	info, ok := payload.Textures[strings.ToUpper(textureType)]
	if !ok {
		return prop.Value
	}

	newPayload := models.TexturePayload{
		Timestamp:   payload.Timestamp,
		ProfileID:   payload.ProfileID,
		ProfileName: payload.ProfileName,
		Textures: map[string]models.TextureInfo{
			strings.ToUpper(textureType): info,
		},
	}
	data, _ := json.Marshal(newPayload)
	return base64.StdEncoding.EncodeToString(data)
}

func (ts *TextureService) GenerateTexturePayload(profileID, textureType, textureURL, model string) string {
	var props map[string]models.TextureInfo

	var existingProp models.ProfileProperty
	database.DB.
		Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").
		First(&existingProp)

	if existingProp.ID != 0 {
		decoded, _ := base64.StdEncoding.DecodeString(existingProp.Value)
		var existingPayload models.TexturePayload
		if err := json.Unmarshal(decoded, &existingPayload); err == nil {
			props = existingPayload.Textures
		}
	}

	if props == nil {
		props = make(map[string]models.TextureInfo)
	}

	metadata := make(map[string]interface{})
	if textureType == "skin" && model != "" {
		metadata["model"] = model
	}

	props[strings.ToUpper(textureType)] = models.TextureInfo{
		URL:      textureURL,
		Metadata: metadata,
	}

	authService := NewAuthService()
	profile := authService.GetProfileByID(profileID)
	profileName := ""
	if profile != nil {
		profileName = profile.Name
	}

	payload := models.TexturePayload{
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

func (ts *TextureService) RewriteTexturesProperty(prop *models.ProfileProperty) error {
	if prop.Name != "textures" {
		return nil
	}

	decoded, err := base64.StdEncoding.DecodeString(prop.Value)
	if err != nil {
		return err
	}

	var payload models.TexturePayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return err
	}

	callbackURL := ts.GetEffectiveCallbackURL()
	changed := false

	for k, info := range payload.Textures {
		// Check if it's a local texture URL (contains /textures/)
		if strings.Contains(info.URL, "/textures/") {
			parts := strings.Split(info.URL, "/textures/")
			hash := parts[len(parts)-1]
			newURL := callbackURL + "/textures/" + hash
			if info.URL != newURL {
				info.URL = newURL
				payload.Textures[k] = info
				changed = true
			}
		}
	}

	if changed {
		newJSON, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		newValue := base64.StdEncoding.EncodeToString(newJSON)
		prop.Value = newValue
		// Re-sign
		signature, err := ts.SignTextureValue(newValue)
		if err != nil {
			return err
		}
		prop.Signature = signature
	}

	return nil
}

func (ts *TextureService) GetProfileProperties(profileID string, profileName string, unsigned bool) ([]models.ProfileProperty, error) {
	var props []models.ProfileProperty
	result := database.DB.
		Where("profile_id = ? AND (name != ? OR delete_when = 0)", profileID, "textures").
		Find(&props)

	if result.Error != nil {
		return nil, fmt.Errorf("failed to get profile properties: %v", result.Error)
	}

	for i := range props {
		if props[i].Name == "textures" {
			ts.RewriteTexturesProperty(&props[i])
		}
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

func (ts *TextureService) CleanupOrphanedTextures() int {
	unlock, err := acquireTextureStorageLock()
	if err != nil {
		log.Printf("[TextureCleanup] failed to lock texture storage: %v", err)
		return 0
	}
	defer unlock()

	var orphans []models.ProfileProperty
	now := time.Now().Unix()
	if err := database.DB.
		Where("name = ? AND delete_when > 0 AND delete_when <= ?", "textures", now).
		Find(&orphans).Error; err != nil {
		log.Printf("[TextureCleanup] failed to query orphaned textures: %v", err)
		return 0
	}

	var retained []models.ProfileProperty
	if err := database.DB.Where("name = ? AND (delete_when = 0 OR delete_when > ?)", "textures", now).Find(&retained).Error; err != nil {
		log.Printf("[TextureCleanup] failed to query retained textures: %v", err)
		return 0
	}
	referenced := make(map[string]bool)
	for _, prop := range retained {
		hashes, err := textureHashes(prop.Value)
		if err != nil {
			log.Printf("[TextureCleanup] cannot inspect retained property %d: %v", prop.ID, err)
			return 0
		}
		for _, hash := range hashes {
			referenced[hash] = true
		}
	}

	deleted := 0
	for _, orphan := range orphans {
		hashes, err := textureHashes(orphan.Value)
		if err != nil {
			log.Printf("[TextureCleanup] cannot inspect orphan property %d: %v", orphan.ID, err)
			continue
		}
		failed := false
		for _, hash := range hashes {
			if !referenced[hash] {
				if err := ts.DeletePreview(avatarFileName(hash)); err != nil {
					log.Printf("[TextureCleanup] failed to delete avatar %s: %v", hash, err)
					failed = true
					break
				}
				if err := ts.DeleteTexture(hash); err != nil {
					log.Printf("[TextureCleanup] failed to delete texture %s: %v", hash, err)
					failed = true
					break
				}
			}
		}
		if failed {
			continue
		}
		if err := database.DB.Delete(&orphan).Error; err != nil {
			log.Printf("[TextureCleanup] failed to delete orphan row %d: %v", orphan.ID, err)
			continue
		}
		deleted++
	}

	return deleted
}

func (ts *TextureService) GetTextureByHash(hash string) ([]byte, string, error) {
	path, err := ts.GetTexturePath(hash)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, "image/png", nil
}

func (ts *TextureService) GetPreviewByFileName(fileName string) ([]byte, string, error) {
	storageDir := config.AppConfig.Yggdrasil.Server.TexturesStorage
	if storageDir == "" {
		storageDir = "./storage"
	}
	path := filepath.Join(storageDir, previewsSubDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	return data, "image/webp", nil
}

func (ts *TextureService) GetTextures(profileID string) (map[string]models.TextureInfo, error) {
	var props []models.ProfileProperty
	if err := database.DB.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").Find(&props).Error; err != nil {
		return nil, err
	}

	textures := make(map[string]models.TextureInfo)
	for _, prop := range props {
		var payload models.TexturePayload
		decoded, err := base64.StdEncoding.DecodeString(prop.Value)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(decoded, &payload); err != nil {
			continue
		}
		for k, v := range payload.Textures {
			textures[k] = v
		}
	}
	return textures, nil
}

func (ts *TextureService) GeneratePreviewImage(fileData []byte, textureType, model string) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(fileData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode texture: %v", err)
	}

	var preview image.Image
	switch textureType {
	case "skin":
		preview = ts.renderSkinPreview(img, model == "slim")
	case "cape":
		preview = ts.renderCapePreview(img)
	default:
		return nil, fmt.Errorf("invalid texture type: %s", textureType)
	}

	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, preview, &nativewebp.Options{
		CompressionLevel: nativewebp.BestCompression,
	}); err != nil {
		return nil, fmt.Errorf("failed to encode webp: %v", err)
	}
	return buf.Bytes(), nil
}

func (ts *TextureService) renderSkinPreview(src image.Image, slim bool) image.Image {
	armWidth := 4
	if slim {
		armWidth = 3
	}

	canvasWidth := 8 + (armWidth * 2)
	base := image.NewNRGBA(image.Rect(0, 0, canvasWidth, 32))
	draw.Draw(base, base.Bounds(), image.Transparent, image.Point{}, draw.Src)

	torsoX := armWidth
	headX := torsoX
	leftArmX := 0
	rightArmX := torsoX + 8
	leftLegX := torsoX
	rightLegX := torsoX + 4

	// Head
	ts.drawPart(base, src, image.Rect(8, 8, 16, 16), image.Pt(headX, 0))
	ts.overlayPart(base, src, image.Rect(40, 8, 48, 16), image.Pt(headX, 0))

	// Torso
	ts.drawPart(base, src, image.Rect(20, 20, 28, 32), image.Pt(torsoX, 8))
	ts.overlayPart(base, src, image.Rect(20, 36, 28, 48), image.Pt(torsoX, 8))

	// Arms
	armFront := image.Rect(44, 20, 44+armWidth, 32)
	armOverlay := image.Rect(44, 36, 44+armWidth, 48)
	ts.drawPart(base, src, armFront, image.Pt(leftArmX, 8))
	ts.drawPart(base, src, armFront, image.Pt(rightArmX, 8))
	ts.overlayPart(base, src, armOverlay, image.Pt(leftArmX, 8))
	ts.overlayPart(base, src, armOverlay, image.Pt(rightArmX, 8))

	// Legs
	legFront := image.Rect(4, 20, 8, 32)
	legOverlay := image.Rect(4, 36, 8, 48)
	ts.drawPart(base, src, legFront, image.Pt(leftLegX, 20))
	ts.drawPart(base, src, legFront, image.Pt(rightLegX, 20))
	ts.overlayPart(base, src, legOverlay, image.Pt(leftLegX, 20))
	ts.overlayPart(base, src, legOverlay, image.Pt(rightLegX, 20))

	return ts.scaleNearest(base, previewScale)
}

func (ts *TextureService) renderCapePreview(src image.Image) image.Image {
	base := image.NewNRGBA(image.Rect(0, 0, 10, 16))
	draw.Draw(base, base.Bounds(), image.Transparent, image.Point{}, draw.Src)
	ts.drawPart(base, src, image.Rect(1, 1, 11, 17), image.Point{})
	return ts.scaleNearest(base, previewScale)
}

func (ts *TextureService) drawPart(dst draw.Image, src image.Image, srcRect image.Rectangle, dstMin image.Point) {
	draw.Draw(dst, image.Rectangle{Min: dstMin, Max: dstMin.Add(srcRect.Size())}, src, srcRect.Min, draw.Src)
}

func (ts *TextureService) overlayPart(dst draw.Image, src image.Image, srcRect image.Rectangle, dstMin image.Point) {
	bounds := src.Bounds()
	if srcRect.Min.X >= bounds.Min.X && srcRect.Min.Y >= bounds.Min.Y &&
		srcRect.Max.X <= bounds.Max.X && srcRect.Max.Y <= bounds.Max.Y {
		draw.Draw(dst, image.Rectangle{Min: dstMin, Max: dstMin.Add(srcRect.Size())}, src, srcRect.Min, draw.Over)
	}
}

func (ts *TextureService) scaleNearest(src image.Image, factor int) image.Image {
	if factor <= 1 {
		return src
	}
	srcBounds := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, srcBounds.Dx()*factor, srcBounds.Dy()*factor))
	for y := 0; y < srcBounds.Dy(); y++ {
		for x := 0; x < srcBounds.Dx(); x++ {
			c := color.NRGBAModel.Convert(src.At(srcBounds.Min.X+x, srcBounds.Min.Y+y)).(color.NRGBA)
			ts.fillScaledPixel(dst, x*factor, y*factor, factor, c)
		}
	}
	return dst
}

func (ts *TextureService) fillScaledPixel(dst *image.NRGBA, startX, startY, size int, c color.NRGBA) {
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dst.SetNRGBA(startX+x, startY+y, c)
		}
	}
}
