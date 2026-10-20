package controllers

import (
	"log"
	"time"

	"github.com/lnb/HRPAuth-Yggdrasil-API/services"
)

// MBETimeoutCleanupController 周期性扫描 "开启 Mojang bind 但未绑定" 的账号并自动关闭。
// 行为与 HRPAuth 旧版 mbe_timeout_controller 一致：开启后 15 分钟未绑定即自动 reset mbe=0。
type MBETimeoutCleanupController struct{}

func NewMBETimeoutCleanupController() *MBETimeoutCleanupController {
	return &MBETimeoutCleanupController{}
}

// Start interval<=0 时使用 30 秒默认值 (与旧实现保持一致)。
func (mtc *MBETimeoutCleanupController) Start(interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go mtc.loop(interval)
}

func (mtc *MBETimeoutCleanupController) loop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		services.RunMBETimeoutSweep()
	}
}

// 首次启动时打印一次日志，方便运维确认模块已装载。
func (mtc *MBETimeoutCleanupController) LogStarted() {
	log.Printf("[MBETimeout] cleanup loop running every %s", 30*time.Second)
}
