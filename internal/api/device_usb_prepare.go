package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/device"
)

var prepareWSLUSBForAPIFn = func(ctx context.Context) (device.WSLUSBPrepareResult, error) {
	return device.PrepareWSLUSB(ctx, device.WSLUSBPrepareOptions{})
}

func (s *Server) handleDevicePrepareUSB(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := prepareWSLUSBForAPIFn(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "WSL USB 准备失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":                 "ok",
		"message":                result.Message,
		"supported_device_found": result.SupportedDeviceFound,
		"prepared":               result.Prepared,
		"actions":                result.Actions,
		"devices":                result.Devices,
	})
}
