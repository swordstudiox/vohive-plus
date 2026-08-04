package device

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func (p *Pool) suppressQMIUnhealthyEviction(worker *Worker) (bool, string) {
	if worker == nil {
		return true, "worker_nil"
	}
	if p.IsESIMSwitching(worker.ID) {
		return true, "esim_switching"
	}
	if remain := worker.healthRecoveryRemaining(time.Now()); remain > 0 {
		return true, fmt.Sprintf("recovery_window(%s)", remain.Round(time.Second))
	}
	worker.qmiRegistrationMu.Lock()
	registrationInFlight := worker.qmiRegistrationInFlight
	worker.qmiRegistrationMu.Unlock()
	if registrationInFlight {
		return true, "registration_reconcile_in_flight"
	}
	if p != nil && p.lifecycle != nil {
		if canEvict, reason := p.lifecycle.CanEvict(worker.ID, time.Now()); !canEvict {
			return true, reason
		}
	}
	if worker.Backend == nil {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	mode, err := worker.Backend.GetOperatingMode(ctx)
	if err != nil {
		return false, ""
	}
	if mode == backend.ModeRFOff || mode == backend.ModeLowPower {
		return true, fmt.Sprintf("operating_mode=%d", int(mode))
	}
	return false, ""
}

func shouldFastStartMissingQMIWorker(cfg config.DeviceConfig, live QMIDevice, discoveryAvailable bool) bool {
	if !discoveryAvailable {
		// 发现失败时，检查配置的设备文件是否真的存在，避免反复尝试打开不存在的路径。
		// 模块 AT 重启后 USB 重新枚举前设备文件会消失，此时不应快速拉起。
		if ctrl := strings.TrimSpace(cfg.ControlDevice); ctrl != "" {
			if _, err := os.Stat(ctrl); err != nil {
				return false
			}
		}
		return true
	}
	if strings.TrimSpace(live.ControlPath) == "" && strings.TrimSpace(live.NetInterface) == "" && strings.TrimSpace(live.USBPath) == "" {
		return false
	}
	return !qmiManagedAttachmentChanged(cfg, live)
}

func (p *Pool) healthCheckWorkerSnapshot() []*Worker {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	workers := make([]*Worker, 0, len(p.workers))
	for _, w := range p.workers {
		if w != nil {
			workers = append(workers, w)
		}
	}
	return workers
}

func (p *Pool) runHealthCheckTick() bool {
	workers := p.healthCheckWorkerSnapshot()
	needRescan := false
	for _, w := range workers {
		p.refreshIPs(w, false)
		w.cleanupFragmentCache(30 * time.Minute)
		healthy, healthErr := w.ProbeDeviceHealth()
		w.setCachedHealthy(healthy)
		if healthy {
			layer := HealthLayerAT
			if w.Backend != nil && w.Backend.Mode() != backend.BackendAT {
				layer = HealthLayerQMI
			}
			w.RecordWatchdogEvent(WatchdogEvent{
				Layer:     layer,
				State:     HealthStateHealthy,
				EventType: "control_health_check_ok",
				Reason:    "control_health_check_ok",
			})
			w.resetHealthFailureStreak()
			continue
		}

		actBackend := ""
		if w.Backend != nil {
			actBackend = w.Backend.Mode()
		}
		isQMI := actBackend == "qmi" || strings.ToLower(strings.TrimSpace(w.Config.DeviceBackend)) == "qmi"

		if isQMI {
			if suppressed, reason := p.suppressQMIUnhealthyEviction(w); suppressed {
				logger.Debug("QMI 节点当前处于恢复窗口，跳过本轮剥离判定", "device", w.ID, "reason", reason)
				continue
			}
		}

		failures := 1
		if isQMI {
			failures = w.recordHealthFailure()
		}
		// 传输确认已断开（broken pipe/EOF/connection closed 等）时，重连前不可能探活成功，
		// 没有必要再等满 3 次观察窗口——跳过等待，第一次失败就直接触发恢复。
		transportDown := isQMI && healthErr != nil && qmiErrorIndicatesTransportDown(healthErr.Error())
		if isQMI && strings.TrimSpace(w.Config.ControlDevice) != "" {
			if failures < qmiHealthFailureThreshold && !transportDown {
				logger.Warn("QMI 节点探活失败，进入连续失败观察窗口",
					"device", w.ID,
					"failures", failures,
					"threshold", qmiHealthFailureThreshold)
				continue
			}
			reason := "qmi_health_threshold"
			if transportDown && failures < qmiHealthFailureThreshold {
				reason = "qmi_transport_down"
			}
			w.RecordWatchdogEvent(WatchdogEvent{
				Layer:               HealthLayerQMI,
				State:               HealthStateInvalid,
				EventType:           reason,
				Reason:              reason,
				ConsecutiveFailures: failures,
				Threshold:           qmiHealthFailureThreshold,
			})
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(w.ID, LifecyclePhaseRecovering, reason, qmiLifecycleRecoveryTTL)
			}
			logger.Info("检测到免扫节点(QMI)探活超限，进入统一模组恢复流程", "device", w.ID, "reason", reason)
			p.scheduleWorkerRecoveryWithTransportEvent(w.ID, reason, &TransportRecoveryEvent{
				DeviceID:         w.ID,
				WorkerGeneration: w.generation,
				Kind:             TransportRecoveryEventHealthSuspect,
				Source:           reason,
				Err:              healthErr,
			})
			continue
		}

		logger.Info("定时检查发现设备不健康，将触发重连扫描", "device", w.ID, "backend", func() string {
			if w.Backend != nil {
				return w.Backend.Mode()
			}
			return "none"
		}())
		needRescan = true
	}
	workerCount := len(workers)

	if !needRescan {
		if managed := config.ListDevices(); true {
			for _, md := range managed {
				p.mu.RLock()
				isRebuilding := p.rebuilding[md.ID]
				isRebootRecovering := p.modemRebootRecovering[md.ID]
				hasWorker := p.workers[md.ID] != nil
				p.mu.RUnlock()

				if md.ModemIMEI != "" && !hasWorker && !isRebuilding && !isRebootRecovering {
					isQMIConf := strings.ToLower(strings.TrimSpace(md.DeviceBackend)) == "qmi" ||
						(strings.TrimSpace(md.DeviceBackend) == "" && strings.TrimSpace(md.ControlDevice) != "")

					if isQMIConf && strings.TrimSpace(md.ControlDevice) != "" && strings.TrimSpace(md.Interface) != "" {
						live := QMIDevice{}
						discoveryAvailable := false
						if qmiList, err := discoverQMIDevicesFn(); err == nil {
							discoveryAvailable = true
							for _, candidate := range qmiList {
								if strings.TrimSpace(candidate.ControlPath) == strings.TrimSpace(md.ControlDevice) ||
									strings.TrimSpace(candidate.NetInterface) == strings.TrimSpace(md.Interface) ||
									strings.TrimSpace(candidate.USBPath) == strings.TrimSpace(md.USBPath) {
									live = candidate
									break
								}
							}
						}
						if !shouldFastStartMissingQMIWorker(md, live, discoveryAvailable) {
							logger.Info("定时检查发现 QMI 静态路径已变化，改为全量重扫", "device", md.ID)
							needRescan = true
							break
						}

						logger.Info("定时检查发现免扫类型节点缺少 Worker，直接尝试初始化拉起", "device", md.ID)
						go func(c config.DeviceConfig) {
							if _, err := p.AddWorkerFromConfig(c); err != nil {
								logger.Warn("快速拉起节点失败，可能为底层掉线或冲突，下个周期重试", "device", c.ID, "err", err)
							}
						}(md)
						continue
					}

					logger.Info("定时检查发现已配置设备缺少 Worker，将触发重连扫描",
						"device", md.ID, "imei", md.ModemIMEI,
						"active_workers", workerCount)
					needRescan = true
					break
				}
			}
		}
	}

	return needRescan
}

func (p *Pool) healthCheckLoop() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("healthCheckLoop panic recovered", "err", r)
		}
	}()

	for _, w := range p.healthCheckWorkerSnapshot() {
		p.refreshIPs(w, false)
	}

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	syncTicker := time.NewTicker(1 * time.Minute)
	defer syncTicker.Stop()

	sem := make(chan struct{}, 6)

	for {
		select {
		case <-p.ctx.Done():
			return

		case <-ticker.C:
			if p.runHealthCheckTick() {
				go func() {
					if err := p.RescanAndReconnect(); err != nil {
						logger.Warn("定时重连扫描失败", "err", err)
					}
				}()
			}

		case <-syncTicker.C:
			p.mu.RLock()
			workers := make([]*Worker, 0, len(p.workers))
			for _, w := range p.workers {
				workers = append(workers, w)
			}
			p.mu.RUnlock()

			for _, w := range workers {
				worker := w
				if worker == nil {
					continue
				}
				sem <- struct{}{}
				go func() {
					defer func() { <-sem }()

					done := make(chan struct{})
					go func() {
						isATMode := worker.Backend == nil || worker.Backend.Mode() == "at"
						if isATMode && worker.Modem != nil {
							worker.Modem.RefreshStatus(
								func(msg string) {
									if notifier := p.getNotifier(); notifier != nil {
										notifier.NotifyRaw(msg)
									}
								},
								func(msg string) {
									if notifier := p.getNotifier(); notifier != nil {
										notifier.NotifyRaw(msg)
									}
								},
							)
						}
						_ = worker.RefreshRuntime(nil, "health_sync")
						p.PersistRuntimeState(worker)
						close(done)
					}()

					select {
					case <-done:
					case <-time.After(20 * time.Second):
						logger.Warn("设备状态同步超时", "device", worker.ID)
					}
				}()
			}
		}
	}
}

func (w *Worker) GetCachedIP() string {
	if nc := w.NetworkController(); nc != nil && !nc.IsConnected() {
		return ""
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return w.cachedIP
}

func (w *Worker) GetCachedIPv6() string {
	if nc := w.NetworkController(); nc != nil && !nc.IsConnected() {
		return ""
	}
	w.cacheMu.RLock()
	defer w.cacheMu.RUnlock()
	return w.cachedPublicIPv6
}

func (w *Worker) GetCachedDeviceStatus() modem.DeviceStatus {
	if w == nil {
		return modem.DeviceStatus{}
	}

	w.cacheMu.RLock()
	if w.state.Runtime.Ready || w.state.Identity.Ready {
		status := w.projectDeviceStatusLocked()
		w.cacheMu.RUnlock()
		return status
	}
	w.cacheMu.RUnlock()

	if w.Backend == nil || w.Backend.Mode() == "at" {
		if w.Modem != nil {
			return w.Modem.GetFullStatus()
		}
	}
	return modem.DeviceStatus{}
}

func (w *Worker) GetCachedHealthy() bool {
	if w == nil {
		return false
	}

	w.cacheMu.RLock()
	if w.state.Runtime.Ready || w.state.Identity.Ready {
		healthy := w.state.Meta.Healthy
		w.cacheMu.RUnlock()
		return healthy
	}
	w.cacheMu.RUnlock()

	if w.Backend == nil || w.Backend.Mode() == "at" {
		if w.Modem != nil {
			return w.Modem.IsHealthy()
		}
	}
	return false
}

func (w *Worker) GetCachedIMSI() string {
	if w == nil {
		return ""
	}
	w.cacheMu.RLock()
	imsi := strings.TrimSpace(w.state.Identity.IMSI)
	w.cacheMu.RUnlock()
	return imsi
}

func (w *Worker) setCachedHealthy(healthy bool) {
	if w == nil {
		return
	}
	w.cacheMu.Lock()
	w.state.Meta.Healthy = healthy
	w.cacheMu.Unlock()
}

func (w *Worker) markHealthRecoveryWindow(duration time.Duration) {
	if w == nil || duration <= 0 {
		return
	}
	deadline := time.Now().Add(duration)
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer:         HealthLayerQMI,
		State:         HealthStateRecovering,
		EventType:     "recovery_window",
		Reason:        "recovery_window",
		RecoveryUntil: deadline,
	})
}

func (w *Worker) healthRecoveryRemaining(now time.Time) time.Duration {
	if w == nil {
		return 0
	}
	w.healthMu.Lock()
	defer w.healthMu.Unlock()
	if now.IsZero() {
		now = time.Now()
	}
	if now.After(w.healthGraceUntil) {
		return 0
	}
	return w.healthGraceUntil.Sub(now)
}

func (w *Worker) resetHealthFailureStreak() {
	if w == nil {
		return
	}
	w.healthMu.Lock()
	w.healthConsecutiveFailures = 0
	w.healthMu.Unlock()
}

func (w *Worker) recordHealthFailure() int {
	if w == nil {
		return 0
	}
	w.healthMu.Lock()
	w.healthConsecutiveFailures++
	failures := w.healthConsecutiveFailures
	w.healthMu.Unlock()

	state := HealthStateSuspect
	if failures >= qmiHealthFailureThreshold {
		state = HealthStateInvalid
	}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer:               HealthLayerQMI,
		State:               state,
		EventType:           "control_health_check_failed",
		Reason:              "control_health_check_failed",
		ConsecutiveFailures: failures,
		Threshold:           qmiHealthFailureThreshold,
	})
	return failures
}

func (w *Worker) InvalidateDynamicCache() {
	if w == nil {
		return
	}
	w.cacheMu.Lock()
	w.state.Runtime.Ready = false
	w.cacheMu.Unlock()
	logger.Info(fmt.Sprintf("[%s] 动态状态缓存已失效清空", w.ID))
}

func (w *Worker) PreWarmCache() {
	if w == nil {
		return
	}
	_ = w.RefreshRuntime(nil, "prewarm")
	_ = w.RefreshIdentityLive(nil, "prewarm")
	if w.Pool != nil {
		w.Pool.PersistRuntimeState(w)
		w.Pool.PersistIdentityState(w)
	}
	logger.Info(fmt.Sprintf("[%s] 设备冷启动预热完毕", w.ID))
}

func (w *Worker) clearCachedIP() {
	w.cacheMu.Lock()
	w.cachedIP = ""
	w.cachedPublicIPv6 = ""
	w.cacheTime = time.Time{}
	w.cacheMu.Unlock()
}

func (w *Worker) NetworkConnected() bool {
	return w != nil && w.NetworkController() != nil && w.NetworkController().IsConnected()
}

// ipHealthy treats either IP family as a valid data-plane address.
func ipHealthy(v4, v6 string) bool {
	return strings.TrimSpace(v4) != "" || strings.TrimSpace(v6) != ""
}

// representativeIP 在仍需要单个字符串代表"当前公网 IP"的调用点（如 RotateWithNotify 的
// 新旧 IP 对比/返回值）使用：双栈下优先取 v4，仅有 v6 时退化为 v6。
func representativeIP(publicV4, publicV6 string) string {
	if publicV4 != "" {
		return publicV4
	}
	return publicV6
}

func (p *Pool) refreshIPs(worker *Worker, checkPublic bool) {
	nc := worker.NetworkController()
	if worker == nil || nc == nil || !nc.IsConnected() {
		return
	}

	now := time.Now()
	minInterval := 10 * time.Second
	if checkPublic {
		minInterval = 5 * time.Second
	}

	worker.ipRefreshMu.Lock()
	if worker.ipRefreshInFlight {
		worker.ipRefreshMu.Unlock()
		return
	}
	if !worker.ipRefreshLast.IsZero() && now.Sub(worker.ipRefreshLast) < minInterval {
		worker.ipRefreshMu.Unlock()
		return
	}
	worker.ipRefreshInFlight = true
	worker.ipRefreshLast = now
	worker.ipRefreshMu.Unlock()

	go func() {
		defer func() {
			worker.ipRefreshMu.Lock()
			worker.ipRefreshInFlight = false
			worker.ipRefreshMu.Unlock()
		}()

		privateIP := nc.GetPrivateIP()
		privateIPv6 := nc.GetPrivateIPv6()

		if checkPublic {
			publicV4, publicV6 := nc.GetPublicIPv4AndV6NoCache()

			worker.cacheMu.Lock()
			oldIP := worker.cachedIP
			oldIPv6 := worker.cachedPublicIPv6
			if publicV4 != "" {
				worker.cachedIP = publicV4
			}
			if publicV6 != "" {
				worker.cachedPublicIPv6 = publicV6
			}
			worker.cacheTime = time.Now()
			worker.cacheMu.Unlock()

			if ipHealthy(publicV4, publicV6) {
				worker.publicIPRetryMu.Lock()
				worker.publicIPRetryCount = 0
				if worker.publicIPRetryTimer != nil {
					worker.publicIPRetryTimer.Stop()
					worker.publicIPRetryTimer = nil
				}
				worker.publicIPRetryMu.Unlock()

				if oldIP == "" && oldIPv6 == "" {
					logger.Info(fmt.Sprintf("[%s] 获取到公网 IP", worker.ID), "public_ip", publicV4, "public_ipv6", publicV6, "private_ip", privateIP, "private_ipv6", privateIPv6)
				} else if (publicV4 != "" && publicV4 != oldIP) || (publicV6 != "" && publicV6 != oldIPv6) {
					logger.Info(fmt.Sprintf("[%s] 检测到 IP 变更", worker.ID), "old_ip", oldIP, "new_ip", publicV4, "old_ipv6", oldIPv6, "new_ipv6", publicV6, "private_ip", privateIP, "private_ipv6", privateIPv6)

					if publicV4 != "" && oldIP != "" {
						if app := p.voWiFiHost().Instance(worker.ID); app != nil {
							logger.Info(fmt.Sprintf("[%s] 检测到内网抖动，正平滑触发底层 MOBIKE 漫游", worker.ID), "new_ip", publicV4)
							if err := app.TriggerMOBIKE(oldIP, publicV4); err != nil {
								logger.Warn(fmt.Sprintf("[%s] MOBIKE 漫游触发失败", worker.ID), "err", err)
							}
						}
					} else if publicV6 != "" && oldIPv6 != "" {
						if app := p.voWiFiHost().Instance(worker.ID); app != nil {
							logger.Info(fmt.Sprintf("[%s] 检测到 IPv6 内网抖动，正平滑触发底层 MOBIKE 漫游", worker.ID), "new_ipv6", publicV6)
							if err := app.TriggerMOBIKE(oldIPv6, publicV6); err != nil {
								logger.Warn(fmt.Sprintf("[%s] MOBIKE 漫游触发失败", worker.ID), "err", err)
							}
						}
					}
				}

				if imei := worker.getIMEI(); imei != "" {
					_ = db.UpdateDeviceIPsV6(imei, publicV4, publicV6, privateIP, privateIPv6)
				}
			} else {
				p.schedulePublicIPRetry(worker)
			}
		} else {
			if imei := worker.getIMEI(); imei != "" {
				cachedPublic := worker.GetCachedIP()
				cachedPublicIPv6 := worker.GetCachedIPv6()
				_ = db.UpdateDeviceIPsV6(imei, cachedPublic, cachedPublicIPv6, privateIP, privateIPv6)
			}
		}
	}()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (p *Pool) schedulePublicIPRetry(worker *Worker) {
	worker.publicIPRetryMu.Lock()
	defer worker.publicIPRetryMu.Unlock()

	worker.publicIPRetryCount++
	attempt := worker.publicIPRetryCount

	if attempt > 8 {
		logger.Warn(fmt.Sprintf("[%s] 公网 IP 获取失败次数过多，停止重试", worker.ID))
		return
	}

	delay := time.Duration(1<<minInt(attempt-1, 6)) * 2 * time.Second
	if worker.publicIPRetryTimer != nil {
		worker.publicIPRetryTimer.Stop()
	}
	worker.publicIPRetryTimer = time.AfterFunc(delay, func() {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		p.refreshIPs(worker, true)
	})

	logger.Info(fmt.Sprintf("[%s] 未获取到公网 IP，稍后重试", worker.ID), "attempt", attempt, "next_retry_in", delay.String())
}
