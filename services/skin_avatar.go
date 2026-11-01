package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/url"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

var ErrSkinAvatarNotFound = errors.New("skin avatar not found")
var acquireTextureStorageLock = lockTextureStorage

// The MySQL advisory lock coordinates writers and cleanup across service instances.
func lockTextureStorage() (func(), error) {
	db, err := database.DB.DB()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(config.AppConfig.Database.DBName + ":" + config.AppConfig.Database.TablePrefix))
	name := fmt.Sprintf("ygg-textures-%x", sum[:20])
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", name).Scan(&acquired); err != nil || !acquired.Valid || acquired.Int64 != 1 {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("texture storage lock unavailable")
		}
		return nil, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", name); err != nil {
			// Closing a connection with an unreleased lock must not return it to the pool.
			conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
	}, nil
}

func avatarFileName(hash string) string {
	return hash + "_avatar.png"
}

func skinAvatarBounds(width, height int) (int, error) {
	faceSize := width/4 - width/8
	if width < 8 || faceSize < 1 || height < (width+3)/4 {
		return 0, fmt.Errorf("skin is too small to extract its face and head overlay")
	}
	return faceSize, nil
}

func generateSkinAvatar(data []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode skin avatar: %w", err)
	}
	bounds := src.Bounds()
	width := bounds.Dx()
	size, err := skinAvatarBounds(width, bounds.Dy())
	if err != nil {
		return nil, err
	}
	face := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.Draw(face, face.Bounds(), src, bounds.Min.Add(image.Pt(width/8, width/8)), draw.Src)
	overlay := image.NewNRGBA(face.Bounds())
	overlayX, overlayWidth := width*5/8, width*3/4-width*5/8
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			overlay.Set(x, y, src.At(bounds.Min.X+overlayX+x*overlayWidth/size, bounds.Min.Y+width/8+y))
		}
	}
	draw.Draw(face, face.Bounds(), overlay, image.Point{}, draw.Over)
	if size < 64 {
		face = scaleAvatar(face, 64)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, face); err != nil {
		return nil, fmt.Errorf("failed to encode skin avatar: %w", err)
	}
	return buf.Bytes(), nil
}

func scaleAvatar(src image.Image, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dst.Set(x, y, src.At(src.Bounds().Min.X+x*src.Bounds().Dx()/size, src.Bounds().Min.Y+y*src.Bounds().Dy()/size))
		}
	}
	return dst
}

func textureHashes(value string) ([]string, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var payload models.TexturePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	var hashes []string
	for _, info := range payload.Textures {
		if hash := localTextureHash(info.URL); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	return hashes, nil
}

func localTextureHash(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(u.Path, "/textures/")
	if len(parts) != 2 || len(parts[1]) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(parts[1]); err != nil || strings.ToLower(parts[1]) != parts[1] {
		return ""
	}
	return parts[1]
}

func (ts *TextureService) GetProfileSkinAvatar(profileID string) ([]byte, error) {
	unlock, err := acquireTextureStorageLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var prop models.ProfileProperty
	if err := database.DB.Where("profile_id = ? AND name = ? AND delete_when = 0", profileID, "textures").First(&prop).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSkinAvatarNotFound
		}
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(prop.Value)
	if err != nil {
		return nil, err
	}
	var payload models.TexturePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	hash := localTextureHash(payload.Textures["SKIN"].URL)
	if hash == "" {
		return nil, ErrSkinAvatarNotFound
	}
	if data, _, err := ts.GetPreviewByFileName(avatarFileName(hash)); err == nil {
		return data, nil
	}
	skin, _, err := ts.GetTextureByHash(hash)
	if err != nil {
		return nil, err
	}
	avatar, err := generateSkinAvatar(skin)
	if err != nil {
		return nil, err
	}
	if err := ts.SavePreview(avatar, avatarFileName(hash)); err != nil {
		return nil, err
	}
	return avatar, nil
}
