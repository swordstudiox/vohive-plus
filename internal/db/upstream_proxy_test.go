package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
)

func openTestDB(t *testing.T) {
	t.Helper()
	if err := Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	loadCountryTableFixture(t)
}

func loadCountryTableFixture(t *testing.T) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "mcc-mnc-table.json")
	rows := `[{"mcc":"310","mnc":"260","iso":"us","country":"United States","country_code":"US","network":"T-Mobile"}]`
	if err := os.WriteFile(cachePath, []byte(rows), 0o644); err != nil {
		t.Fatalf("WriteFile() error=%v", err)
	}
	result := upstreamproxy.InitCountryTable(context.Background(), upstreamproxy.CountryTableOptions{
		CachePath: cachePath,
		SourceURL: "http://127.0.0.1:1/missing",
	})
	if result.Err != nil {
		t.Fatalf("InitCountryTable() error=%v", result.Err)
	}
}

func TestUpstreamProxyCountryRuleSelectsEnabledProxyByHomeMCC(t *testing.T) {
	openTestDB(t)
	now := time.Now()
	if err := UpsertUpstreamProxy(UpstreamProxy{ID: "proxy-us", Name: "US", Addr: "127.0.0.1:1080", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: " us ", UpstreamProxyID: "proxy-us", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err := GetHomeMCCUpstreamProxy("310")
	if err != nil {
		t.Fatalf("GetHomeMCCUpstreamProxy() error=%v", err)
	}
	if country != "US" || proxy == nil || proxy.ID != "proxy-us" {
		t.Fatalf("proxy=%+v country=%q, want proxy-us/US", proxy, country)
	}
}

func TestUpstreamProxyCountryRuleDirectWhenNoRuleOrDisabled(t *testing.T) {
	openTestDB(t)
	if err := UpsertUpstreamProxy(UpstreamProxy{ID: "proxy-us", Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}
	proxy, country, err := GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("no rule proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: "US", UpstreamProxyID: "proxy-us", Enabled: false}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err = GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("disabled rule proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
}

func TestUpstreamProxyCountryRuleDirectWhenUnknownMCCOrMissingProxy(t *testing.T) {
	openTestDB(t)
	proxy, country, err := GetHomeMCCUpstreamProxy("404")
	if err != nil || proxy != nil || country != "" {
		t.Fatalf("unknown mcc proxy=%+v country=%q err=%v, want nil/empty/nil", proxy, country, err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: "US", UpstreamProxyID: "missing", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err = GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("missing proxy proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
}
