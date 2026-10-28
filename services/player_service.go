package services

import (
	"errors"
	"log"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
	"github.com/lnb/HRPAuth-Yggdrasil-API/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PlayerService 负责读取 / 修改当前调用者对应的 Game Account (Mojang bind) 字段。
// 这些字段 (mbe / mojang_uuid / cbh) 在解耦后已迁到本仓库的 accounts 表，
// 旧 HRPAuth Core 的 /user 不再下发，由本服务对外提供。
type PlayerService struct {
	coreClient *clients.CoreClient
	authSvc    *AuthService
}

func NewPlayerService() *PlayerService {
	return &PlayerService{
		coreClient: clients.NewCoreClient(),
		authSvc:    NewAuthService(),
	}
}

// PlayerInfo 是 SDK 期望的 Game Account 摘要。
type PlayerInfo struct {
	UID        string `json:"uid"`         // Core 用户的 UUID (字符串)
	AccountID  int    `json:"account_id"`  // 本仓库 accounts.id
	MBE        bool   `json:"mbe"`         // 是否已开启 Mojang bind 流程
	MojangUUID string `json:"mojang_uuid"` // 已绑定时为 32 位 hex；否则空串
	CBH        bool   `json:"cbh"`         // 1 = 已被人认领 (非 bot)
}

// ErrAccountNotBound 当 Bearer token 无法解析到本地 Account 时返回。
var ErrAccountNotBound = errors.New("player account not linked")

var (
	ErrProfileAlreadyExists   = errors.New("profile already exists")
	ErrProfileUUIDInvalid     = errors.New("invalid profile uuid")
	ErrProfileUUIDConflict    = errors.New("profile uuid already exists")
	ErrProfileNameConflict    = errors.New("profile name already exists")
	ErrProfileNameUnavailable = errors.New("profile name unavailable")
)

type GameProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// ResolveAccount 通过 Bearer token 解析到本地 accounts 行。
// token 可以是本仓库的 Yggdrasil AccessToken，也可以是 HRPAuth Core 的 OAuth2 AccessToken。
func (ps *PlayerService) ResolveAccount(bearer string) (*models.Account, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, ErrAccountNotBound
	}
	tok := ps.authSvc.ValidateToken(bearer, "")
	if tok == nil || tok.AccountID == 0 {
		return nil, ErrAccountNotBound
	}
	var account models.Account
	if err := database.DB.First(&account, tok.AccountID).Error; err != nil {
		return nil, ErrAccountNotBound
	}
	return &account, nil
}

// GetProfile returns the caller's profile, or nil when it has not been created.
func (ps *PlayerService) GetProfile(bearer string) (*GameProfile, error) {
	account, err := ps.ResolveAccount(bearer)
	if err != nil {
		return nil, err
	}

	var profile models.Profile
	err = database.DB.Where("account_id = ?", account.ID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &GameProfile{ID: profile.ID, Name: profile.Name, Model: profile.Model}, nil
}

// CreateProfile creates the account's single profile. premiumName is only set
// after the controller has looked up the supplied Mojang username.
func (ps *PlayerService) CreateProfile(bearer, profileID, premiumName string) (*GameProfile, error) {
	account, err := ps.ResolveAccount(bearer)
	if err != nil {
		return nil, err
	}

	profileID = normalizeProfileUUID(profileID)
	if profileID == "" {
		return nil, ErrProfileUUIDInvalid
	}

	profileName := premiumName
	if profileName == "" && account.CoreUserID != nil {
		profileName = ps.authSvc.getCachedUsername(*account.CoreUserID)
	}
	if profileName == "" || len(profileName) > 30 {
		return nil, ErrProfileNameUnavailable
	}

	profile := models.Profile{
		ID:        profileID,
		AccountID: account.ID,
		Name:      profileName,
		Model:     "default",
	}

	err = database.DB.Transaction(func(tx *gorm.DB) error {
		var lockedAccount models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedAccount, account.ID).Error; err != nil {
			return err
		}

		var existing models.Profile
		if err := tx.Where("account_id = ?", account.ID).First(&existing).Error; err == nil {
			return ErrProfileAlreadyExists
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if premiumName != "" {
			var uuidOwner models.Account
			err := tx.Where("mojang_uuid = ? AND id <> ?", profileID, account.ID).First(&uuidOwner).Error
			if err == nil {
				return ErrProfileUUIDConflict
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		if err := tx.Where("id = ?", profileID).First(&existing).Error; err == nil {
			return ErrProfileUUIDConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("name = ?", profileName).First(&existing).Error; err == nil {
			return ErrProfileNameConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		return tx.Create(&profile).Error
	})
	if err != nil {
		var mysqlErr *mysqlDriver.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			if strings.Contains(strings.ToLower(mysqlErr.Message), "name") {
				return nil, ErrProfileNameConflict
			}
			return nil, ErrProfileUUIDConflict
		}
		return nil, err
	}

	return &GameProfile{ID: profile.ID, Name: profile.Name, Model: profile.Model}, nil
}

func normalizeProfileUUID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 36 {
		for _, position := range []int{8, 13, 18, 23} {
			if value[position] != '-' {
				return ""
			}
		}
	} else if len(value) != 32 || strings.Contains(value, "-") {
		return ""
	}

	normalized := strings.ToLower(strings.ReplaceAll(value, "-", ""))
	if !utils.IsValidMojangUUID(normalized) {
		return ""
	}
	return normalized
}

// GetPlayerInfo 根据 Bearer token 返回 Game Account 摘要。
func (ps *PlayerService) GetPlayerInfo(bearer string) (*PlayerInfo, error) {
	account, err := ps.ResolveAccount(bearer)
	if err != nil {
		return nil, err
	}
	uid := ""
	if account.CoreUserID != nil {
		uid = *account.CoreUserID
	}
	mUUID := ""
	if account.MojangUUID != nil {
		mUUID = *account.MojangUUID
	}
	return &PlayerInfo{
		UID:        uid,
		AccountID:  account.ID,
		MBE:        account.MBE,
		MojangUUID: mUUID,
		CBH:        account.CBH,
	}, nil
}

// EnableMojangBind 开启 Mojang bind 流程 (写 mbe=1) 并挂一个 15 分钟自动回收，
// 防止用户开启后忘记绑定。
func (ps *PlayerService) EnableMojangBind(bearer string) (*PlayerInfo, error) {
	account, err := ps.ResolveAccount(bearer)
	if err != nil {
		return nil, err
	}
	if account.MojangUUID != nil && *account.MojangUUID != "" {
		// 已经绑定过；不允许重复开启。
		return ps.GetPlayerInfo(bearer)
	}
	if err := database.DB.Model(&models.Account{}).
		Where("id = ?", account.ID).
		Update("mbe", true).Error; err != nil {
		return nil, err
	}
	RegisterMBETimeout(account.ID)
	account.MBE = true
	log.Printf("[YGG-PLAYER] action=mojang_bind_enable account=%d mbe=true auto_reset_at=%s", account.ID, MBETimeoutFor(account.ID).Format("2006-01-02 15:04:05"))
	return ps.GetPlayerInfo(bearer)
}

// DisableMojangBind 关闭 Mojang bind 流程 (写 mbe=0) 并取消自动回收。
func (ps *PlayerService) DisableMojangBind(bearer string) (*PlayerInfo, error) {
	account, err := ps.ResolveAccount(bearer)
	if err != nil {
		return nil, err
	}
	if err := database.DB.Model(&models.Account{}).
		Where("id = ?", account.ID).
		Update("mbe", false).Error; err != nil {
		return nil, err
	}
	CancelMBETimeout(account.ID)
	account.MBE = false
	log.Printf("[YGG-PLAYER] action=mojang_bind_disable account=%d mbe=false", account.ID)
	return ps.GetPlayerInfo(bearer)
}
