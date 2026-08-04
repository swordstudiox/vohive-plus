package device

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
)

func loadDeviceCountryTableFixture(t *testing.T) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "mcc-mnc-table.json")
	rows := `[{"mcc":"310","mnc":"260","iso":"us","country":"United States","country_code":"US","network":"T-Mobile"}]`
	if err := os.WriteFile(cachePath, []byte(rows), 0o644); err != nil {
		t.Fatalf("WriteFile() error=%v", err)
	}
	result := upstreamproxy.InitCountryTable(context.Background(), upstreamproxy.CountryTableOptions{CachePath: cachePath})
	if result.Err != nil {
		t.Fatalf("InitCountryTable() error=%v", result.Err)
	}
}

func openDeviceTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("db.Init() error=%v", err)
	}
}

func TestResolveVoWiFiCountryProxySelectsUSProxy(t *testing.T) {
	openDeviceTestDB(t)
	loadDeviceCountryTableFixture(t)
	if err := db.UpsertUpstreamProxy(db.UpstreamProxy{ID: "proxy-us", Addr: "127.0.0.1:1080", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}
	if err := db.UpsertUpstreamProxyCountryRule(db.UpstreamProxyCountryRule{CountryCode: "US", UpstreamProxyID: "proxy-us", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	got := resolveVoWiFiCountryProxy("310", "trace-1", "dev-1")
	if got == nil || got.ID != "proxy-us" || got.Addr != "127.0.0.1:1080" || !got.Enabled {
		t.Fatalf("resolveVoWiFiCountryProxy()=%+v, want proxy-us", got)
	}
}

func TestResolveVoWiFiCountryProxyDirectWhenNoCountryRule(t *testing.T) {
	openDeviceTestDB(t)
	loadDeviceCountryTableFixture(t)
	if got := resolveVoWiFiCountryProxy("404", "trace-1", "dev-1"); got != nil {
		t.Fatalf("resolveVoWiFiCountryProxy(404)=%+v, want nil direct", got)
	}
}
