package device

import (
	"fmt"
	"strings"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const qmiTransportFailureRecoveryReason = "qmi_transport_failed"

// qmiErrorIndicatesTransportDown 判断错误是否表示 QMI 控制面传输已断开
// （设备节点消失 / qmi-proxy socket 断裂 / 连接关闭等），即重连前不可能成功的状态。
func qmiErrorIndicatesTransportDown(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, fragment := range []string{
		"broken pipe",
		"read failed: eof",
		"connection closed",
		"no such device",
		"no such file or directory",
		"write failed",
		"failed to open qmi device",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

// handleTransportRecoveryExhausted 是唯一的传输层 exhausted 事件驱动重建入口：
// 仅当底层核心恢复彻底失败/设备节点消失时调用。
func (p *Pool) handleTransportRecoveryExhausted(worker *Worker, generation uint64, layer HealthLayer, reason string, err error) bool {
	if p == nil || worker == nil {
		return false
	}
	if current := p.GetWorker(worker.ID); current != worker {
		return false
	}
	if generation != 0 && worker.generation != 0 && generation != worker.generation {
		return false
	}
	if err == nil {
		err = fmt.Errorf("qmi recovery exhausted: %s", reason)
	}
	logger.Warn("传输核心恢复已彻底失败，调度 worker 重建",
		"device", worker.ID, "layer", layer, "reason", reason, "err", err)
	p.clearDesiredVoWiFiRecoverState(worker.ID)
	return p.scheduleWorkerRecoveryWithTransportEvent(worker.ID, qmiTransportFailureRecoveryReason, &TransportRecoveryEvent{
		DeviceID:         worker.ID,
		WorkerGeneration: worker.generation,
		Kind:             TransportRecoveryEventRecoveryExhausted,
		Source:           string(layer) + ":recovery_exhausted:" + reason,
		Err:              err,
	})
}

// maybeScheduleTransportRebuild applies the sliding-window guard before
// scheduling a worker rebuild. Over-cap devices are marked Failed instead of
// looping rebuilds.
func (p *Pool) maybeScheduleTransportRebuild(worker *Worker, layer HealthLayer, reason string, err error) bool {
	if p == nil || worker == nil {
		return false
	}
	if p.transportRecovery != nil && !p.transportRecovery.AllowRebuild(worker.ID) {
		logger.Warn("传输恢复重建超过滑窗上限，置 Failed 等待人工/重枚举",
			"device", worker.ID, "layer", layer, "reason", reason, "err", err)
		worker.RecordWatchdogEvent(WatchdogEvent{
			Layer:     layer,
			State:     HealthStateFailed,
			EventType: "transport_recovery_giveup",
			Reason:    reason,
			Err:       err,
		})
		return false
	}
	return p.handleTransportRecoveryExhausted(worker, worker.generation, layer, reason, err)
}
