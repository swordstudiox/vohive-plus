package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/db"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleTrafficAnalysis(c *gin.Context) {
	rng := c.Query("range")
	if rng == "" {
		rng = "day"
	}
	deviceID := strings.TrimSpace(c.Query("device_id"))
	now := time.Now()

	buckets, chartData, err := db.GetTrafficAnalysisWithChart(rng, deviceID, now)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"range":   rng,
		"buckets": buckets,
		"chart":   chartData,
	})
}
