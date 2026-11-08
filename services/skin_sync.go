package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"gorm.io/gorm"
)

// Skin sync results — the log vocabulary of the official-skin mirror.
// Every outcome of a sync attempt is exactly one of these, so operators
// can tell from the logs why a bound account does (or does not) carry its
// official skin.
const (
	SkinSyncResultSynced              = "synced"               // official skin downloaded and applied
	SkinSyncResultUpToDate            = "up_to_date"           // mark already matches the bound uuid's official skin
	SkinSyncResultNoOfficialSkin      = "no_official_skin"     // official profile has no custom skin; local binding cleared, client shows the default skin
	SkinSyncResultDisabled            = "disabled"             // account turned skin_sync off
	SkinSyncResultNotBound            = "not_bound"            // account has no mojang_uuid
	SkinSyncResultNoProfile           = "no_profile"           // account has no profile to apply textures to
	SkinSyncResultUpstreamUnreachable = "upstream_unreachable" // sessionserver/textures.minecraft.net network failure, timeout or 5xx
	SkinSyncResultUpstreamNotFound    = "upstream_not_found"   // sessionserver answered 404/204 for the bound uuid
	SkinSyncResultTextureInvalid      = "texture_invalid"      // downloaded payload failed texture validation
	SkinSyncResultApplyFailed         = "apply_failed"         // storage/database/signature write failed
)

// skinSyncDispatch runs a sync job. Production dispatches to a goroutine so
// a bind request never waits on Mojang; tests override it to run inline or
// to record the requests (account ID and trigger are passed along so tests
// can assert which bind entry point asked for a sync).
var skinSyncDispatch = func(accountID int, trigger string, fn func()) { go fn() }

// SkinSyncService mirrors the official skin of a bound Mojang account into
// the local profile textures. The sync is best-effort by contract: it never
// returns an error to the bind flow, every failure class is logged with a
// distinct result, and a missed sync is retried on the next Yggdrasil login
// (controllers/yggdrasil_controller.go::Authenticate) or on any later bind
// of the same account.
type SkinSyncService struct {
	textureSvc        *TextureService
	httpClient        *http.Client
	sessionserverBase string
}

func NewSkinSyncService() *SkinSyncService {
	return &SkinSyncService{
		textureSvc:        NewTextureService(),
		httpClient:        &http.Client{Timeout: 15 * time.Second},
		sessionserverBase: "https://sessionserver.mojang.com",
	}
}

// mojangSessionProfile mirrors the sessionserver.mojang.com response shape
// (subset: only what the skin sync consumes).
type mojangSessionProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Properties []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"properties"`
}

type mojangSkinInfo struct {
	URL          string
	Model        string // "slim" or "default"
	OfficialHash string // Mojang-side texture hash parsed from the URL, "" when unparseable
}

// ShouldSync reports whether the account participates in the official-skin
// mirror: bound to a mojang_uuid with the per-account toggle on.
func (ss *SkinSyncService) ShouldSync(account *models.Account) bool {
	return account != nil && account.SkinSync &&
		account.MojangUUID != nil && *account.MojangUUID != ""
}

// LoginRetryNeeded reports whether a login should schedule a sync attempt:
// the account wants syncing but the mark does not yet cover the bound uuid
// (never synced, or re-bound to a different uuid since the last sync).
func (ss *SkinSyncService) LoginRetryNeeded(account *models.Account) bool {
	if !ss.ShouldSync(account) {
		return false
	}
	return !markCoversUUID(account.SkinSyncMark, *account.MojangUUID)
}

func markCoversUUID(mark *string, uuid string) bool {
	return mark != nil && strings.HasPrefix(*mark, uuid+":")
}

// RequestSkinSync schedules an asynchronous sync for the account unless the
// cheap pre-checks already rule it out (unbound, toggle off, or the mark
// shows the bound uuid's official skin is already mirrored). It never
// fails: scheduling problems are logged inside the job, never returned.
func (ss *SkinSyncService) RequestSkinSync(accountID int, trigger string) {
	var account models.Account
	if err := database.DB.First(&account, accountID).Error; err != nil {
		return
	}
	if !ss.ShouldSync(&account) {
		return
	}
	if markCoversUUID(account.SkinSyncMark, *account.MojangUUID) {
		return
	}
	skinSyncDispatch(accountID, trigger, func() { ss.RunSync(accountID, trigger) })
}

// RunSync performs one full sync attempt synchronously and logs its outcome.
// Returns one of the SkinSyncResult* constants.
func (ss *SkinSyncService) RunSync(accountID int, trigger string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, detail := ss.syncAccount(ctx, accountID, trigger)
	log.Printf("[YGG-SKINSYNC] account=%d trigger=%s result=%s%s", accountID, trigger, result, detail)
	return result
}

func (ss *SkinSyncService) syncAccount(ctx context.Context, accountID int, trigger string) (string, string) {
	var account models.Account
	if err := database.DB.First(&account, accountID).Error; err != nil {
		return SkinSyncResultApplyFailed, fmt.Sprintf(" reason=account_lookup_failed err=%v", err)
	}
	if !account.SkinSync {
		return SkinSyncResultDisabled, ""
	}
	if account.MojangUUID == nil || *account.MojangUUID == "" {
		return SkinSyncResultNotBound, ""
	}
	uuid := *account.MojangUUID
	if markCoversUUID(account.SkinSyncMark, uuid) {
		return SkinSyncResultUpToDate, ""
	}

	var profiles []models.Profile
	if err := database.DB.Where("account_id = ?", accountID).Find(&profiles).Error; err != nil {
		return SkinSyncResultApplyFailed, fmt.Sprintf(" reason=profile_lookup_failed err=%v", err)
	}
	if len(profiles) == 0 {
		return SkinSyncResultNoProfile, ""
	}

	info, result, detail := ss.fetchOfficialSkin(ctx, uuid)
	if result != "" {
		return result, detail
	}

	if info == nil {
		// Official profile has no custom skin: mirror that state by
		// clearing any local skin binding so the client renders the
		// default skin.
		removed, err := ss.clearLocalSkins(profiles)
		if err != nil {
			return SkinSyncResultApplyFailed, fmt.Sprintf(" reason=clear_local_skin_failed err=%v", err)
		}
		ss.writeMark(accountID, uuid+":default")
		return SkinSyncResultNoOfficialSkin, fmt.Sprintf(" cleared_local_skin=%t", removed)
	}

	if info.OfficialHash != "" && markCoversUUIDValue(account.SkinSyncMark, uuid+":"+info.OfficialHash) {
		return SkinSyncResultUpToDate, ""
	}

	data, err := ss.downloadTexture(ctx, info.URL)
	if err != nil {
		return SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=texture_download_failed err=%v", err)
	}

	model := info.Model
	validated, err := ss.textureSvc.ValidateTexture(bytes.NewReader(data), "skin", model)
	if err != nil {
		return SkinSyncResultTextureInvalid, fmt.Sprintf(" err=%v", err)
	}

	if err := ss.applySkin(profiles, validated.Data, model); err != nil {
		return SkinSyncResultApplyFailed, fmt.Sprintf(" err=%v", err)
	}

	mark := uuid + ":" + info.OfficialHash
	if info.OfficialHash == "" {
		// Unparseable official URL: fall back to the local hash so the
		// mark still pins what was applied.
		mark = uuid + ":" + ss.textureSvc.CalculateHash(validated.Data)
	}
	ss.writeMark(accountID, mark)
	return SkinSyncResultSynced, fmt.Sprintf(" uuid=%s model=%s", uuid, model)
}

func markCoversUUIDValue(mark *string, want string) bool {
	return mark != nil && *mark == want
}

// OverrideSkinSyncDispatchForTest swaps the dispatch function used by
// RequestSkinSync and returns a restore function. Test-only.
func OverrideSkinSyncDispatchForTest(fn func(accountID int, trigger string, job func())) func() {
	prev := skinSyncDispatch
	skinSyncDispatch = fn
	return func() { skinSyncDispatch = prev }
}

// fetchOfficialSkin resolves the bound uuid against sessionserver. It
// returns (nil, "", "") when the profile exists but carries no SKIN
// texture; a non-empty result string signals a failure classification.
func (ss *SkinSyncService) fetchOfficialSkin(ctx context.Context, uuid string) (*mojangSkinInfo, string, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ss.sessionserverBase+"/session/minecraft/profile/"+uuid, nil)
	if err != nil {
		return nil, SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=bad_request err=%v", err)
	}
	resp, err := ss.httpClient.Do(req)
	if err != nil {
		return nil, SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=sessionserver_err err=%v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=sessionserver_read_failed err=%v", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return nil, SkinSyncResultUpstreamNotFound, fmt.Sprintf(" status=%d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=sessionserver_status status=%d", resp.StatusCode)
	}

	var entry mojangSessionProfile
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, SkinSyncResultUpstreamUnreachable, fmt.Sprintf(" reason=sessionserver_decode_failed err=%v", err)
	}

	for _, prop := range entry.Properties {
		if prop.Name != "textures" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(prop.Value)
		if err != nil {
			return nil, SkinSyncResultUpstreamUnreachable, " reason=textures_property_undecodable"
		}
		var payload models.TexturePayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, SkinSyncResultUpstreamUnreachable, " reason=textures_property_invalid"
		}
		skin, ok := payload.Textures["SKIN"]
		if !ok || skin.URL == "" {
			return nil, "", ""
		}
		info := &mojangSkinInfo{URL: skin.URL, Model: "default", OfficialHash: officialTextureHash(skin.URL)}
		if m, ok := skin.Metadata["model"].(string); ok && m == "slim" {
			info.Model = "slim"
		}
		return info, "", ""
	}
	// No textures property at all: equivalent to no custom skin.
	return nil, "", ""
}

// officialTextureHash extracts the Mojang-side hash from a
// textures.minecraft.net/texture/<64hex> URL; "" when the URL does not
// carry one.
func officialTextureHash(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "texture" {
		return ""
	}
	if len(parts[1]) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return ""
	}
	return strings.ToLower(parts[1])
}

func (ss *SkinSyncService) downloadTexture(ctx context.Context, rawURL string) ([]byte, error) {
	limit := config.AppConfig.Yggdrasil.Security.MaxTextureFileSize
	if limit <= 0 {
		limit = config.DefaultMaxTextureFileSize
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := ss.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("texture server returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// clearLocalSkins removes the active skin binding from every profile so the
// client falls back to the default skin.
func (ss *SkinSyncService) clearLocalSkins(profiles []models.Profile) (bool, error) {
	unlock, err := acquireTextureStorageLock()
	if err != nil {
		return false, err
	}
	defer unlock()
	removedAny := false
	for _, profile := range profiles {
		err := database.DB.Transaction(func(tx *gorm.DB) error {
			removed, err := ss.textureSvc.removeProfileTextureTx(tx, profile.ID, "skin")
			if removed {
				removedAny = true
			}
			return err
		})
		if err != nil {
			return removedAny, err
		}
	}
	return removedAny, nil
}

// applySkin stores the downloaded official skin once and points every
// profile's textures property at it (same storage pipeline as a manual
// upload: original PNG + face avatar + WebP preview + re-signed property).
func (ss *SkinSyncService) applySkin(profiles []models.Profile, skinData []byte, model string) error {
	avatarData, err := generateSkinAvatar(skinData)
	if err != nil {
		return err
	}

	unlock, err := acquireTextureStorageLock()
	if err != nil {
		return err
	}
	defer unlock()

	hash := ss.textureSvc.CalculateHash(skinData)
	if err := ss.textureSvc.SaveTexture(skinData, hash); err != nil {
		return err
	}
	if err := ss.textureSvc.SavePreview(avatarData, avatarFileName(hash)); err != nil {
		return fmt.Errorf("failed to save skin avatar: %w", err)
	}
	if previewData, err := ss.textureSvc.GeneratePreviewImage(skinData, "skin", model); err == nil {
		if err := ss.textureSvc.SavePreview(previewData, hash+"_skin.webp"); err != nil {
			log.Printf("[YGG-SKINSYNC] warning: failed to save texture preview: %v", err)
		}
	} else {
		log.Printf("[YGG-SKINSYNC] warning: failed to generate texture preview: %v", err)
	}

	textureURL := ss.textureSvc.GetEffectiveCallbackURL() + "/textures/" + hash
	for _, profile := range profiles {
		if err := ss.textureSvc.UpdateProfileTexture(profile.ID, "skin", textureURL, model); err != nil {
			return err
		}
	}
	return nil
}

func (ss *SkinSyncService) writeMark(accountID int, mark string) {
	if err := database.DB.Model(&models.Account{}).
		Where("id = ?", accountID).
		Update("skin_sync_mark", mark).Error; err != nil {
		log.Printf("[YGG-SKINSYNC] account=%d result=mark_write_failed err=%v", accountID, err)
	}
}
