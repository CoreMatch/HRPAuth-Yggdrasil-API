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
	"unicode/utf8"

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
	previewScale        = 8
	maxTextureNameLen   = 20
	defaultSkinModel    = "default"
	texturesSubDir      = "textures"
	previewsSubDir      = "previews"
)

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

func (ts *TextureService) UploadTexture(accessToken, profileID, textureType, model, name, description, tags string, fileData []byte) ([]string, error) {
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

	hash := ts.CalculateHash(validated.Data)

	if err := ts.SaveTexture(validated.Data, hash); err != nil {
		return nil, err
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
	callbackURL := config.AppConfig.Callback.URL
	textureURL := strings.TrimRight(callbackURL, "/") + "/textures/" + hash

	if err := ts.UpdateProfileTexture(profileID, textureType, textureURL, model); err != nil {
		return nil, err
	}

	// Save to texture library
        if err := ts.UpsertTextureRecord(token.AccountID, textureType, hash, model, name, description, tags, validated.Width, validated.Height, previewFileName); err != nil {
		log.Printf("Warning: failed to upsert texture record: %v", err)
	}

	warnings := append(validated.Notices, validated.Warnings...)
	return warnings, nil
}

func (ts *TextureService) UpsertTextureRecord(accountID int, textureType, hash, model, name, description, tags string, width, height int, previewFile string) error {
	normalizedTags := ts.NormalizeTags(tags)
	normalizedName := ts.NormalizeTextureName(strings.TrimSpace(name), maxTextureNameLen)
	if normalizedName == "" {
		normalizedName = hash[:8]
	}

	switch textureType {
	case "skin":
		var existing models.TextureListSkin
		err := database.DB.Where("account_id = ? AND hash = ?", accountID, hash).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newSkin := models.TextureListSkin{
				TextureListSkinBase: models.TextureListSkinBase{
					Hash:        hash,
					AccountID:   accountID,
					Model:       model,
					Width:       width,
					Height:      height,
					PreviewFile: previewFile,
					Name:        normalizedName,
					Description: description,
					Tags:        normalizedTags,
				},
			}
			return database.DB.Create(&newSkin).Error
		} else if err == nil {
			return database.DB.Model(&existing).Updates(map[string]interface{}{
				"model":        model,
				"previewfile":  previewFile,
				"name":         normalizedName,
				"description":  description,
				"tags":         normalizedTags,
			}).Error
		}
		return err
	case "cape":
		var existing models.TextureListCape
		err := database.DB.Where("account_id = ? AND hash = ?", accountID, hash).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newCape := models.TextureListCape{
				Hash:        hash,
				AccountID:   accountID,
				Width:       width,
				Height:      height,
				PreviewFile: previewFile,
				Name:        normalizedName,
				Description: description,
				Tags:        normalizedTags,
			}
			return database.DB.Create(&newCape).Error
		} else if err == nil {
			return database.DB.Model(&existing).Updates(map[string]interface{}{
				"previewfile":  previewFile,
				"name":         normalizedName,
				"description":  description,
				"tags":         normalizedTags,
			}).Error
		}
		return err
	}
	return fmt.Errorf("invalid texture type: %s", textureType)
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

func (ts *TextureService) RemoveProfileTexture(profileID, textureType string) error {
	var existingProp models.ProfileProperty
	if err := database.DB.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").First(&existingProp).Error; err != nil {
		return fmt.Errorf("textures property not found")
	}

	decoded, err := base64.StdEncoding.DecodeString(existingProp.Value)
	if err != nil {
		return fmt.Errorf("failed to decode textures property")
	}

	var payload TexturesPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return fmt.Errorf("failed to unmarshal textures payload")
	}

	delete(payload.Textures, strings.ToUpper(textureType))

	if len(payload.Textures) == 0 {
		// If no textures left, mark property for deletion
		return database.DB.Model(&existingProp).Update("delete_when", time.Now().Unix()).Error
	}

	newData, _ := json.Marshal(payload)
	newValue := base64.StdEncoding.EncodeToString(newData)
	newSignature, err := ts.SignTextureValue(newValue)
	if err != nil {
		return err
	}

	return database.DB.Model(&existingProp).Updates(map[string]interface{}{
		"value":     newValue,
		"signature": newSignature,
	}).Error
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

func (ts *TextureService) GetTextures(profileID string) (map[string]TextureInfo, error) {
	var prop models.ProfileProperty
	if err := database.DB.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").First(&prop).Error; err != nil {
		return nil, err
	}
	decoded, _ := base64.StdEncoding.DecodeString(prop.Value)
	var payload TexturesPayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return nil, err
	}
	return payload.Textures, nil
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

func (ts *TextureService) NormalizeTags(tags string) string {
	if strings.TrimSpace(tags) == "" {
		return ""
	}
	replacer := strings.NewReplacer("，", ",", "\n", ",", "\r", ",", "\t", ",", ";", ",", "|", ",")
	normalized := replacer.Replace(tags)
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, part := range strings.Split(normalized, ",") {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return strings.Join(result, ",")
}

func (ts *TextureService) NormalizeTextureName(name string, maxLength int) string {
	if maxLength <= 0 || utf8.RuneCountInString(name) <= maxLength {
		return name
	}
	var builder strings.Builder
	count := 0
	for _, r := range name {
		if count >= maxLength {
			break
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}
