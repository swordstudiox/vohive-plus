package api

import (
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/proxy/server"
)

const (
	deviceTrafficStatusOK            = "ok"
	deviceTrafficStatusWaitingSample = "waiting_sample"
	deviceTrafficStatusStale         = "stale"
)

const deviceTrafficStaleAfter = 150 * time.Second

type deviceTrafficMeta struct {
	Interface   string     `json:"interface"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	AgeSeconds  int64      `json:"age_seconds,omitempty"`
	Status      string     `json:"status"`
}

func buildTrafficOverviewFields(iface string, delta db.LatestMinuteDeltas, now time.Time) (map[string]string, map[string]int64, *deviceTrafficMeta) {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		return nil, nil, nil
	}

	meta := &deviceTrafficMeta{
		Interface: iface,
		Status:    deviceTrafficStatusWaitingSample,
	}
	if delta.PeriodStart.IsZero() {
		return nil, nil, meta
	}

	age := now.Sub(delta.PeriodStart)
	if age < 0 {
		age = 0
	}
	meta.PeriodStart = &delta.PeriodStart
	meta.AgeSeconds = int64(age.Seconds())
	meta.Status = deviceTrafficStatusOK
	if age > deviceTrafficStaleAfter {
		meta.Status = deviceTrafficStatusStale
	}

	rx := delta.RxBytes
	tx := delta.TxBytes
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	rate := float64(rx+tx) / 60.0
	return map[string]string{
			"rx":   server.FormatBytes(rx),
			"tx":   server.FormatBytes(tx),
			"rate": server.FormatBytes(int64(rate)) + "/s",
		}, map[string]int64{
			"bytes_received": rx,
			"bytes_sent":     tx,
		}, meta
}
