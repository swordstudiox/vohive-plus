package device

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/netlink/nl"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"golang.org/x/sys/unix"
)

// UdevWatcher 监听 USB 设备热插拔事件
type UdevWatcher struct {
	pool     *Pool
	stop     chan struct{}
	stopOnce sync.Once

	// 防抖相关
	debounce  time.Duration
	pending   bool
	pendingMu sync.Mutex
	timer     *time.Timer
}

// NewUdevWatcher 创建 udev 监听器
func NewUdevWatcher(pool *Pool) *UdevWatcher {
	return &UdevWatcher{
		pool:     pool,
		stop:     make(chan struct{}),
		debounce: 3 * time.Second, // 等待设备枚举完成
	}
}

// Start 启动 udev 事件监听
func (w *UdevWatcher) Start() {
	go w.loop()
}

// Stop 停止监听
func (w *UdevWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stop)
		w.pendingMu.Lock()
		if w.timer != nil {
			w.timer.Stop()
		}
		w.pendingMu.Unlock()
	})
}

func (w *UdevWatcher) loop() {
	// 创建 netlink 连接监听内核 uevent
	conn, err := nl.Subscribe(unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		logger.Warn("udev 监听器启动失败，热插拔功能不可用", "err", err)
		return
	}
	defer conn.Close()

	logger.Info("udev 设备热插拔监听器已启动")

	for {
		select {
		case <-w.stop:
			logger.Info("udev 监听器已停止")
			return
		default:
		}

		// 设置读取超时，以便定期检查 stop 信号
		tv := unix.NsecToTimeval((1 * time.Second).Nanoseconds())
		_ = conn.SetReceiveTimeout(&tv)

		msgs, _, err := conn.Receive()
		if err != nil {
			// 超时错误是正常的
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				continue
			}
			// 其他错误记录但继续
			continue
		}

		for _, msg := range msgs {
			if w.isModemEvent(msg.Data) {
				w.scheduleRescan()
				break // 一批事件只触发一次扫描
			}
		}
	}
}

// isModemEvent 检查是否是 USB 调制解调器相关事件
func (w *UdevWatcher) isModemEvent(data []byte) bool {
	s := string(data)

	// 检查 ACTION
	if !strings.Contains(s, "ACTION=add") && !strings.Contains(s, "ACTION=remove") {
		return false
	}

	// 检查 SUBSYSTEM（usb/net/tty/usbmisc/wwan 都可能是调制解调器相关）
	if strings.Contains(s, "SUBSYSTEM=usb") ||
		strings.Contains(s, "SUBSYSTEM=net") ||
		strings.Contains(s, "SUBSYSTEM=tty") ||
		strings.Contains(s, "SUBSYSTEM=usbmisc") ||
		strings.Contains(s, "SUBSYSTEM=wwan") {

		// 进一步过滤：排除无关设备
		// 如果是 net 子系统，只关心 wwan 开头的接口
		if strings.Contains(s, "SUBSYSTEM=net") {
			if !strings.Contains(s, "wwan") {
				return false
			}
		}

		// 如果是 tty 子系统，只关心 ttyUSB
		if strings.Contains(s, "SUBSYSTEM=tty") {
			if !strings.Contains(s, "ttyUSB") {
				return false
			}
		}

		logger.Debug("检测到调制解调器相关 udev 事件", "data_preview", truncateString(s, 200))
		return true
	}

	return false
}

// scheduleRescan 防抖：延迟执行扫描
// 采用"重置计时器"模式：每次事件都重置倒计时，确保最终一次事件（设备完成枚举）生效
func (w *UdevWatcher) scheduleRescan() {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	// 如果已有定时器，重置它（确保最后一次事件生效，不丢弃插入事件）
	if w.timer != nil {
		w.timer.Reset(w.debounce)
		return
	}

	w.pending = true
	w.timer = time.AfterFunc(w.debounce, func() {
		w.pendingMu.Lock()
		w.pending = false
		w.timer = nil
		w.pendingMu.Unlock()

		logger.Info("udev 检测到设备变化，执行重新扫描")
		if w.pool != nil {
			if woken := w.pool.WakeModemRebootRecoveries("udev_modem_event"); woken > 0 {
				logger.Debug("udev 事件已唤醒模组重启恢复流程", "recoveries", woken)
				return
			}
		}
		if err := w.pool.RescanAndReconnect(); err != nil {
			logger.Warn("设备重新扫描失败", "err", err)
		}
	})
}

// truncateString 截断字符串用于日志
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
