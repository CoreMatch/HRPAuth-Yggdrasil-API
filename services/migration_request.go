package services

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidMigrationRequest = errors.New("invalid migration request")
	ErrInvalidMigrationTexture = errors.New("invalid migration texture")
	ErrMigrationTooLarge       = errors.New("migration request exceeds size limit")
)

var (
	migrationUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	profileIDPattern     = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

type MigrationMetadata struct {
	MigrationID          string            `json:"migration_id"`
	Identifier           string            `json:"identifier"`
	Password             string            `json:"password"`
	Username             string            `json:"username"`
	Email                string            `json:"email,omitempty"`
	LegacyUserID         string            `json:"legacy_user_id,omitempty"`
	LegacyUserProperties map[string]string `json:"legacy_user_properties,omitempty"`
	ProfileUUID          string            `json:"profile_uuid"`
	ProfileName          string            `json:"profile_name"`
	ProfileModel         string            `json:"profile_model"`
	SkinSHA256           string            `json:"skin_sha256,omitempty"`
	CapeSHA256           string            `json:"cape_sha256,omitempty"`
	SkinSync             bool              `json:"skin_sync"`
}

type StagedMigrationMetadata struct {
	MigrationID          string            `json:"migration_id"`
	Identifier           string            `json:"identifier"`
	Username             string            `json:"username"`
	Email                string            `json:"email,omitempty"`
	LegacyUserID         string            `json:"legacy_user_id,omitempty"`
	LegacyUserProperties map[string]string `json:"legacy_user_properties,omitempty"`
	ProfileUUID          string            `json:"profile_uuid"`
	ProfileName          string            `json:"profile_name"`
	ProfileModel         string            `json:"profile_model"`
	SkinSHA256           string            `json:"skin_sha256,omitempty"`
	CapeSHA256           string            `json:"cape_sha256,omitempty"`
	SkinSync             bool              `json:"skin_sync"`
}

type MigrationAsset struct {
	PNG         []byte
	PixelSHA256 string
	PNGSHA256   string
	Width       int
	Height      int
}

type MigrationCommitRequest struct {
	Metadata StagedMigrationMetadata
	Password string
	Skin     *MigrationAsset
	Cape     *MigrationAsset
	Digest   string
}

type MigrationError struct {
	Code  string
	Stage string
	Err   error
}

func (e *MigrationError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e *MigrationError) Unwrap() error { return e.Err }

func migrationError(code, stage string, err error) error {
	return &MigrationError{Code: code, Stage: stage, Err: err}
}

func DecodeMigrationMetadata(reader io.Reader) (MigrationMetadata, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 64<<10))
	decoder.DisallowUnknownFields()
	var metadata *MigrationMetadata
	if err := decoder.Decode(&metadata); err != nil || metadata == nil {
		if err == nil {
			err = errors.New("metadata must be an object")
		}
		return MigrationMetadata{}, migrationError("invalid_request", "received", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return MigrationMetadata{}, migrationError("invalid_request", "received", errors.New("multiple JSON values"))
	}
	return *metadata, nil
}

func NormalizeMigrationCommit(metadata MigrationMetadata, skin, cape []byte) (MigrationCommitRequest, error) {
	normalized, err := normalizeMigrationMetadata(metadata)
	if err != nil {
		return MigrationCommitRequest{}, err
	}
	request := MigrationCommitRequest{Metadata: normalized, Password: metadata.Password}
	if request.Metadata.SkinSHA256 != "" {
		if skin == nil {
			return MigrationCommitRequest{}, migrationError("invalid_request", "received", errors.New("skin hash without skin file"))
		}
		request.Skin, err = normalizeMigrationAsset("skin", skin)
		if err != nil {
			return MigrationCommitRequest{}, err
		}
		if !strings.EqualFold(request.Skin.PixelSHA256, request.Metadata.SkinSHA256) {
			return MigrationCommitRequest{}, migrationError("migration_asset_conflict", "received", errors.New("skin pixel digest mismatch"))
		}
	} else if skin != nil {
		return MigrationCommitRequest{}, migrationError("invalid_request", "received", errors.New("unexpected skin file"))
	}
	if request.Metadata.CapeSHA256 != "" {
		if cape == nil {
			return MigrationCommitRequest{}, migrationError("invalid_request", "received", errors.New("cape hash without cape file"))
		}
		request.Cape, err = normalizeMigrationAsset("cape", cape)
		if err != nil {
			return MigrationCommitRequest{}, err
		}
		if request.Cape.Width == 22 && request.Cape.Height == 17 {
			return MigrationCommitRequest{}, migrationError("invalid_texture_file", "received", errors.New("legacy cape dimensions are not normalized"))
		}
		if !strings.EqualFold(request.Cape.PixelSHA256, request.Metadata.CapeSHA256) {
			return MigrationCommitRequest{}, migrationError("migration_asset_conflict", "received", errors.New("cape pixel digest mismatch"))
		}
	} else if cape != nil {
		return MigrationCommitRequest{}, migrationError("invalid_request", "received", errors.New("unexpected cape file"))
	}
	request.Digest, err = migrationCommitDigest(request)
	if err != nil {
		return MigrationCommitRequest{}, migrationError("invalid_request", "received", err)
	}
	return request, nil
}

func normalizeMigrationMetadata(metadata MigrationMetadata) (StagedMigrationMetadata, error) {
	invalid := func(err error) (StagedMigrationMetadata, error) {
		return StagedMigrationMetadata{}, migrationError("invalid_request", "received", err)
	}
	if !migrationUUIDPattern.MatchString(metadata.MigrationID) {
		return invalid(errors.New("invalid migration ID"))
	}
	if !profileIDPattern.MatchString(metadata.ProfileUUID) {
		return invalid(errors.New("invalid profile UUID"))
	}
	if metadata.Username != metadata.ProfileName {
		return invalid(errors.New("username and profile name differ"))
	}
	if !validMigrationName(metadata.ProfileName) {
		return invalid(errors.New("invalid profile name"))
	}
	if metadata.ProfileModel != "default" && metadata.ProfileModel != "slim" {
		return StagedMigrationMetadata{}, migrationError("invalid_texture_model", "received", errors.New("unsupported profile model"))
	}
	if metadata.SkinSync {
		return invalid(errors.New("skin sync must be disabled"))
	}
	if !utf8.ValidString(metadata.Password) || utf8.RuneCountInString(metadata.Password) < 6 || len(metadata.Password) > 72 {
		return invalid(errors.New("invalid password length"))
	}
	identifier, err := normalizeMigrationIdentifier(metadata.Identifier)
	if err != nil {
		return StagedMigrationMetadata{}, migrationError("invalid_request", "received", err)
	}
	isEmail := migrationIdentifierIsEmail(identifier)
	email := ""
	if isEmail {
		email = identifier
		if metadata.Email != "" {
			providedEmail, emailErr := normalizeMigrationIdentifier(metadata.Email)
			if emailErr != nil || !migrationIdentifierIsEmail(providedEmail) || providedEmail != email {
				return StagedMigrationMetadata{}, migrationError("invalid_email", "received", errors.New("email does not match identifier"))
			}
		}
	} else if metadata.Email != "" {
		return StagedMigrationMetadata{}, migrationError("invalid_email", "received", errors.New("email must be omitted for username identifiers"))
	}
	if len(metadata.LegacyUserID) > 128 || !utf8.ValidString(metadata.LegacyUserID) || strings.ContainsFunc(metadata.LegacyUserID, unicode.IsControl) {
		return invalid(errors.New("invalid legacy user ID"))
	}
	properties, err := normalizeMigrationProperties(metadata.LegacyUserProperties)
	if err != nil {
		return invalid(err)
	}
	for _, hash := range []string{metadata.SkinSHA256, metadata.CapeSHA256} {
		if hash != "" {
			if _, err := canonicalMigrationSHA256(hash); err != nil {
				return invalid(errors.New("invalid asset digest"))
			}
		}
	}
	return StagedMigrationMetadata{
		MigrationID:          metadata.MigrationID,
		Identifier:           identifier,
		Username:             metadata.Username,
		Email:                email,
		LegacyUserID:         metadata.LegacyUserID,
		LegacyUserProperties: properties,
		ProfileUUID:          metadata.ProfileUUID,
		ProfileName:          metadata.ProfileName,
		ProfileModel:         metadata.ProfileModel,
		SkinSHA256:           strings.ToLower(metadata.SkinSHA256),
		CapeSHA256:           strings.ToLower(metadata.CapeSHA256),
		SkinSync:             false,
	}, nil
}

func normalizeMigrationProperties(properties map[string]string) (map[string]string, error) {
	if len(properties) == 0 {
		return nil, nil
	}
	if len(properties) > 64 {
		return nil, errors.New("too many legacy properties")
	}
	normalized := make(map[string]string, len(properties))
	total := 0
	for name, value := range properties {
		if name == "" || strings.TrimSpace(name) != name || len(name) > 128 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, errors.New("invalid legacy property name")
		}
		if strings.EqualFold(name, "textures") {
			return nil, errors.New("legacy textures property is reserved")
		}
		if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return nil, errors.New("invalid legacy property value")
		}
		if _, exists := normalized[name]; exists {
			return nil, errors.New("duplicate legacy property")
		}
		total += len(name) + len(value)
		normalized[name] = value
	}
	if total > 32<<10 {
		return nil, errors.New("legacy properties exceed size limit")
	}
	return normalized, nil
}

func normalizeMigrationIdentifier(identifier string) (string, error) {
	if identifier == "" || identifier != strings.TrimSpace(identifier) || !utf8.ValidString(identifier) || utf8.RuneCountInString(identifier) > 254 || strings.ContainsFunc(identifier, unicode.IsControl) {
		return "", errors.New("invalid identifier")
	}
	identifier = norm.NFC.String(identifier)
	if address, err := mail.ParseAddress(identifier); err == nil && address.Address == identifier {
		at := strings.LastIndexByte(identifier, '@')
		if at > 0 {
			return identifier[:at] + "@" + strings.ToLower(identifier[at+1:]), nil
		}
	}
	return identifier, nil
}

func migrationIdentifierIsEmail(identifier string) bool {
	address, err := mail.ParseAddress(identifier)
	return err == nil && address.Address == identifier && strings.Contains(identifier, "@")
}

func validMigrationName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 3 && utf8.RuneCountInString(name) <= 30 && strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}

func normalizeMigrationAsset(kind string, data []byte) (*MigrationAsset, error) {
	const maxEncoded = 4 << 20
	const maxDimension = 4096
	const maxPixels = 16 << 20
	invalid := func(err error) (*MigrationAsset, error) {
		return nil, migrationError("invalid_texture_file", "received", fmt.Errorf("%s: %w", kind, err))
	}
	if len(data) == 0 || len(data) > maxEncoded {
		return invalid(errors.New("encoded size exceeds limit"))
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return invalid(err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxDimension || config.Height > maxDimension || config.Width > maxPixels/config.Height {
		return invalid(errors.New("dimensions exceed limit"))
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return invalid(err)
	}
	bounds := decoded.Bounds()
	normalized := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(normalized, normalized.Bounds(), decoded, bounds.Min, draw.Src)
	for offset := 3; offset < len(normalized.Pix); offset += 4 {
		if normalized.Pix[offset] == 0 {
			normalized.Pix[offset-3] = 0
			normalized.Pix[offset-2] = 0
			normalized.Pix[offset-1] = 0
		}
	}
	digest := sha256.New()
	_ = binary.Write(digest, binary.BigEndian, uint32(bounds.Dx()))
	_ = binary.Write(digest, binary.BigEndian, uint32(bounds.Dy()))
	_, _ = digest.Write(normalized.Pix)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&encoded, normalized); err != nil {
		return invalid(err)
	}
	if encoded.Len() > maxEncoded {
		return invalid(errors.New("normalized encoded size exceeds limit"))
	}
	if kind == "skin" {
		if _, err := generateSkinAvatar(encoded.Bytes()); err != nil {
			return invalid(err)
		}
	}
	pngDigest := sha256.Sum256(encoded.Bytes())
	return &MigrationAsset{
		PNG:         encoded.Bytes(),
		PixelSHA256: hex.EncodeToString(digest.Sum(nil)),
		PNGSHA256:   hex.EncodeToString(pngDigest[:]),
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
	}, nil
}

func migrationCommitDigest(request MigrationCommitRequest) (string, error) {
	type assetSummary struct {
		PixelSHA256 string `json:"pixel_sha256"`
		PNGSHA256   string `json:"png_sha256"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
	}
	summary := struct {
		Version  int                     `json:"version"`
		Metadata StagedMigrationMetadata `json:"metadata"`
		Skin     *assetSummary           `json:"skin,omitempty"`
		Cape     *assetSummary           `json:"cape,omitempty"`
	}{Version: 1, Metadata: request.Metadata}
	if request.Skin != nil {
		summary.Skin = &assetSummary{request.Skin.PixelSHA256, request.Skin.PNGSHA256, request.Skin.Width, request.Skin.Height}
	}
	if request.Cape != nil {
		summary.Cape = &assetSummary{request.Cape.PixelSHA256, request.Cape.PNGSHA256, request.Cape.Width, request.Cape.Height}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("yggdrasil-migration-v1\x00"))
	_, _ = digest.Write(encoded)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func canonicalMigrationSHA256(value string) (string, error) {
	if len(value) != sha256.Size*2 {
		return "", errors.New("invalid SHA-256 hex string")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(decoded), nil
}
