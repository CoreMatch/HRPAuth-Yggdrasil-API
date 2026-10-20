package services

import (
	"errors"
	"log"
	"strings"

	"github.com/lnb/HRPAuth-Yggdrasil-API/clients"
	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
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
