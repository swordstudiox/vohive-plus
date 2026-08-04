package api

import (
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/db"
)

func TestBuildTrafficOverviewFieldsDistinguishesWaitingZeroAndStale(t *testing.T) {
	now := time.Date(2026, time.May, 26, 10, 35, 0, 0, time.UTC)

	t.Run("waiting sample", func(t *testing.T) {
		traffic, raw, meta := buildTrafficOverviewFields("wwan0", db.LatestMinuteDeltas{}, now)
		if traffic != nil || raw != nil {
			t.Fatalf("traffic=%v raw=%v want nil while waiting for first sample", traffic, raw)
		}
		if meta == nil || meta.Status != "waiting_sample" || meta.Interface != "wwan0" {
			t.Fatalf("meta=%+v want waiting_sample for wwan0", meta)
		}
	})

	t.Run("zero traffic sample", func(t *testing.T) {
		periodStart := now.Add(-time.Minute)
		traffic, raw, meta := buildTrafficOverviewFields("wwan0", db.LatestMinuteDeltas{
			PeriodStart: periodStart,
			RxBytes:     0,
			TxBytes:     0,
		}, now)
		if traffic["rx"] != "0 B" || traffic["tx"] != "0 B" || traffic["rate"] != "0 B/s" {
			t.Fatalf("traffic=%v want formatted zero values", traffic)
		}
		if raw["bytes_received"] != 0 || raw["bytes_sent"] != 0 {
			t.Fatalf("raw=%v want zero byte counters", raw)
		}
		if meta == nil || meta.Status != "ok" || meta.PeriodStart == nil || !meta.PeriodStart.Equal(periodStart) || meta.AgeSeconds != 60 {
			t.Fatalf("meta=%+v want ok zero sample metadata", meta)
		}
	})

	t.Run("stale sample", func(t *testing.T) {
		traffic, raw, meta := buildTrafficOverviewFields("wwan0", db.LatestMinuteDeltas{
			PeriodStart: now.Add(-5 * time.Minute),
			RxBytes:     100,
			TxBytes:     50,
		}, now)
		if traffic["rx"] != "100 B" || raw["bytes_sent"] != 50 {
			t.Fatalf("traffic=%v raw=%v want stale sample to keep byte values", traffic, raw)
		}
		if meta == nil || meta.Status != "stale" || meta.AgeSeconds != 300 {
			t.Fatalf("meta=%+v want stale metadata", meta)
		}
	})
}
