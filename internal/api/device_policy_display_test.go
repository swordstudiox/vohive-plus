package api

import (
	"path/filepath"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/db"
)

func initPolicyTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "policy.db")); err != nil {
		t.Fatalf("db.Init() error=%v", err)
	}
	t.Cleanup(func() {
		if db.DB != nil {
			if sqlDB, err := db.DB.DB(); err == nil && sqlDB != nil {
				_ = sqlDB.Close()
			}
			db.DB = nil
		}
	})
}

func TestResolveOfflineDevicePolicyFromCard(t *testing.T) {
	initPolicyTestDB(t)
	iccid := "8986007777777777777"
	if err := db.DB.Create(&db.Device{IMEI: "imei-1", Alias: "wwan0", CurrentICCID: &iccid}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertCardPolicy(db.CardPolicy{ICCID: iccid, NetworkEnabled: true, VoWiFiEnabled: true, IPVersion: "v4v6", Source: "user"}); err != nil {
		t.Fatal(err)
	}

	got := resolveOfflineDevicePolicy("wwan0")
	if !got.NetworkEnabled || !got.VoWiFiEnabled {
		t.Fatalf("应取卡策略: %+v", got)
	}
	if got.IPVersion != "v4v6" || !got.SMSEnabled {
		t.Fatalf("ip/sms 错: %+v", got)
	}
}

func TestResolveOfflineDevicePolicyNoCardSafeDefault(t *testing.T) {
	initPolicyTestDB(t)
	got := resolveOfflineDevicePolicy("unknown-device")
	if got.NetworkEnabled || got.VoWiFiEnabled {
		t.Fatalf("无卡应全关: %+v", got)
	}
	if !got.SMSEnabled || got.IPVersion != "v4" {
		t.Fatalf("默认应 sms=on/ip=v4: %+v", got)
	}
}
