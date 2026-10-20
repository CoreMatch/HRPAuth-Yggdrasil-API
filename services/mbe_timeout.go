package services

import (
	"log"
	"sync"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/database"
	"github.com/lnb/HRPAuth-Yggdrasil-API/models"
)

// MBETimeout 是开启 "Mojang bind 流程" 后到自动关闭 mbe 的最长等待时间。
// 沿用 HRPAuth 旧实现，行为保持兼容。
const MBETimeout = 15 * time.Minute

var (
	mbePending   = make(map[int]time.Time) // account.ID → 截止时间
	mbePendingMu sync.Mutex
)

// RegisterMBETimeout 为指定 account 记录一个 15 分钟截止时间。
// 重复调用会覆盖旧的截止时间。
func RegisterMBETimeout(accountID int) {
	mbePendingMu.Lock()
	defer mbePendingMu.Unlock()
	mbePending[accountID] = time.Now().Add(MBETimeout)
}

// CancelMBETimeout 取消指定 account 的待执行回收。
// 用于：成功绑定后；显式 disable 后；用户已绑定 (mbe 永久有效)。
func CancelMBETimeout(accountID int) {
	mbePendingMu.Lock()
	defer mbePendingMu.Unlock()
	delete(mbePending, accountID)
}

// MBETimeoutFor 返回指定 account 的截止时间 (用于日志)。
// 未注册时返回零值。
func MBETimeoutFor(accountID int) time.Time {
	mbePendingMu.Lock()
	defer mbePendingMu.Unlock()
	return mbePending[accountID]
}

// RunMBETimeoutSweep 立刻执行一次扫描：清掉过期的 "已开启但未绑定" 用户。
// 由 controllers/MBETimeoutCleanupController.Start 的 ticker 调用。
func RunMBETimeoutSweep() {
	now := time.Now()

	mbePendingMu.Lock()
	expired := make([]int, 0)
	for id, expiry := range mbePending {
		if !now.Before(expiry) {
			expired = append(expired, id)
			delete(mbePending, id)
		}
	}
	mbePendingMu.Unlock()

	for _, id := range expired {
		var account models.Account
		if err := database.DB.First(&account, id).Error; err != nil {
			continue
		}
		if account.MojangUUID != nil && *account.MojangUUID != "" {
			// 已经绑定完成；不要再写回。
			continue
		}
		if err := database.DB.Model(&account).Update("mbe", false).Error; err != nil {
			log.Printf("[MBETimeout] failed to disable mbe for account=%d: %v", id, err)
			continue
		}
		log.Printf("[MBETimeout] account=%d: mbe auto-disabled after %s (no mojang_uuid bound)", id, MBETimeout)
	}
}
