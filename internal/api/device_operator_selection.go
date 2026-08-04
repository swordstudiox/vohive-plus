package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type operatorScanResponse struct {
	ScanID     string                      `json:"scan_id"`
	Status     string                      `json:"status"`
	StartedAt  time.Time                   `json:"started_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
	Complete   bool                        `json:"complete"`
	Retryable  bool                        `json:"retryable"`
	Message    string                      `json:"message"`
	Error      string                      `json:"error,omitempty"`
	Candidates []backend.OperatorCandidate `json:"candidates"`
}

func operatorSelectionErrorStatus(err error) int {
	switch {
	case errors.Is(err, device.ErrVoWiFiActive), errors.Is(err, device.ErrESIMSwitching):
		return http.StatusConflict
	case errors.Is(err, device.ErrOperatorSelectionNotSupported):
		return http.StatusBadRequest
	case errors.Is(err, device.ErrBackendNotAvailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) handleDeviceMgmtOperatorScan(c *gin.Context) {
	deviceID := deviceIDParam(c)
	w := s.pool.GetWorker(deviceID)
	if w == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备未找到或已离线"})
		return
	}

	if err := validateOperatorScanWorker(w); err != nil {
		logger.Error("扫描运营商失败", "device", deviceID, "err", err)
		c.JSON(operatorSelectionErrorStatus(err), gin.H{"error": "扫描失败: " + err.Error()})
		return
	}

	status, body := operatorScanHTTPStatusAndBody(w.StartOrGetOperatorScan(c.Request.Context()))
	c.JSON(status, body)
}

func (s *Server) handleDeviceMgmtOperatorScanStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	deviceID := deviceIDParam(c)
	w := s.pool.GetWorker(deviceID)
	if w == nil {
		c.SSEvent("operator_scan", operatorScanResponse{
			Status:    string(device.OperatorScanStatusFailed),
			UpdatedAt: time.Now(),
			Retryable: false,
			Message:   "设备未找到或已离线",
			Error:     "device_not_found",
		})
		c.Writer.Flush()
		return
	}

	if err := validateOperatorScanWorker(w); err != nil {
		c.SSEvent("operator_scan", operatorScanResponse{
			Status:    string(device.OperatorScanStatusFailed),
			UpdatedAt: time.Now(),
			Retryable: false,
			Message:   "扫描失败: " + err.Error(),
			Error:     err.Error(),
		})
		c.Writer.Flush()
		return
	}

	notify := c.Writer.CloseNotify()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	send := func(result device.OperatorScanResult) bool {
		_, body := operatorScanHTTPStatusAndBody(result)
		c.SSEvent("operator_scan", body)
		c.Writer.Flush()
		return operatorScanSSEShouldContinue(result)
	}

	if !send(w.StartOrGetOperatorScan(c.Request.Context())) {
		return
	}

	for {
		select {
		case <-notify:
			return
		case <-c.Request.Context().Done():
			return
		case <-s.shutdownCh:
			return
		case <-ticker.C:
			if !send(w.GetOperatorScanSnapshot()) {
				return
			}
		}
	}
}

func validateOperatorScanWorker(w *device.Worker) error {
	if w == nil {
		return device.ErrWorkerNil
	}
	if w.Backend == nil {
		return device.ErrBackendNotAvailable
	}
	if _, ok := w.Backend.(backend.OperatorSelectionProvider); !ok {
		return device.ErrOperatorSelectionNotSupported
	}
	return nil
}

func operatorScanSSEShouldContinue(result device.OperatorScanResult) bool {
	return result.Status == device.OperatorScanStatusRunning
}

func operatorScanHTTPStatusAndBody(result device.OperatorScanResult) (int, operatorScanResponse) {
	status := http.StatusOK
	if result.Status == device.OperatorScanStatusRunning && len(result.Candidates) == 0 {
		status = http.StatusAccepted
	}
	return status, operatorScanResponse{
		ScanID:     result.ScanID,
		Status:     string(result.Status),
		StartedAt:  result.StartedAt,
		UpdatedAt:  result.UpdatedAt,
		Complete:   result.Complete,
		Retryable:  result.Retryable,
		Message:    result.Message,
		Error:      result.Err,
		Candidates: result.Candidates,
	}
}

func (s *Server) handleDeviceMgmtGetOperatorSelection(c *gin.Context) {
	deviceID := deviceIDParam(c)
	w := s.pool.GetWorker(deviceID)
	if w == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备未找到或已离线"})
		return
	}

	sel, err := w.GetOperatorSelection(c.Request.Context())
	if err != nil {
		logger.Error("读取运营商选择配置失败", "device", deviceID, "err", err)
		c.JSON(operatorSelectionErrorStatus(err), gin.H{"error": "读取失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, sel)
}

func (s *Server) handleDeviceMgmtSetOperatorSelection(c *gin.Context) {
	deviceID := deviceIDParam(c)
	var req backend.SetOperatorSelectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数无效"})
		return
	}

	w := s.pool.GetWorker(deviceID)
	if w == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备未找到或已离线"})
		return
	}

	sel, err := w.SetOperatorSelection(c.Request.Context(), req)
	if err != nil {
		logger.Error("设置运营商失败", "device", deviceID, "err", err)
		c.JSON(operatorSelectionErrorStatus(err), gin.H{"error": "设置失败: " + err.Error()})
		return
	}

	// Persist to file
	if err := config.UpdateDeviceInFile(s.configPath, deviceID, w.Config); err != nil {
		logger.Error("写入设备配置失败 (operator selection)", "device", deviceID, "err", err)
	}

	c.JSON(http.StatusOK, sel)
}
