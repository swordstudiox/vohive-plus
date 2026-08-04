package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
)

func loadAPICountryTableFixture(t *testing.T) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "mcc-mnc-table.json")
	rows := `[
		{"mcc":"310","mnc":"260","iso":"us","country":"United States","country_code":"US","network":"T-Mobile"},
		{"mcc":"262","mnc":"01","iso":"de","country":"Germany","country_code":"DE","network":"Telekom"}
	]`
	if err := os.WriteFile(cachePath, []byte(rows), 0o644); err != nil {
		t.Fatalf("WriteFile() error=%v", err)
	}
	result := upstreamproxy.InitCountryTable(context.Background(), upstreamproxy.CountryTableOptions{CachePath: cachePath})
	if result.Err != nil {
		t.Fatalf("InitCountryTable() error=%v", result.Err)
	}
}

func newUpstreamProxyCountryRulesTestServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if err := db.Init(filepath.Join(t.TempDir(), "api.db")); err != nil {
		t.Fatalf("db.Init() error=%v", err)
	}
	t.Cleanup(func() { db.DB = nil })
	return &Server{}
}

func TestUpstreamProxyCountriesAPIListsLoadedCountries(t *testing.T) {
	loadAPICountryTableFixture(t)
	server := newUpstreamProxyCountryRulesTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/upstream-proxy-countries", nil)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	server.handleListUpstreamProxyCountries(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	var countries []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &countries); err != nil {
		t.Fatalf("json.Unmarshal() error=%v", err)
	}
	if len(countries) != 2 || countries[0]["country_code"] != "DE" || countries[1]["country_code"] != "US" {
		t.Fatalf("countries=%+v, want DE then US", countries)
	}
}

func TestUpstreamProxyCountryRulesAPIUpsertsAndListsUS(t *testing.T) {
	loadAPICountryTableFixture(t)
	server := newUpstreamProxyCountryRulesTestServer(t)
	if err := db.UpsertUpstreamProxy(db.UpstreamProxy{ID: "proxy-us", Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/upstream-proxy-country-rules/us", strings.NewReader(`{"upstream_proxy_id":"proxy-us","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	c.Params = gin.Params{{Key: "country_code", Value: "us"}}
	server.handleUpsertUpstreamProxyCountryRule(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/upstream-proxy-country-rules", nil)
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = req
	server.handleListUpstreamProxyCountryRules(c)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rules []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rules); err != nil {
		t.Fatalf("json.Unmarshal() error=%v body=%s", err, rr.Body.String())
	}
	if len(rules) != 1 || rules[0]["country_code"] != "US" || rules[0]["country_name"] != "United States" {
		t.Fatalf("rules=%+v", rules)
	}
}

func TestUpstreamProxyCountryRulesAPIRejectsUnknownCountryAndMissingProxy(t *testing.T) {
	loadAPICountryTableFixture(t)
	server := newUpstreamProxyCountryRulesTestServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/upstream-proxy-country-rules/ZZ", strings.NewReader(`{"upstream_proxy_id":"proxy-us","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = req
	c.Params = gin.Params{{Key: "country_code", Value: "ZZ"}}
	server.handleUpsertUpstreamProxyCountryRule(c)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown country status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/upstream-proxy-country-rules/US", strings.NewReader(`{"upstream_proxy_id":"missing","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rr)
	c.Request = req
	c.Params = gin.Params{{Key: "country_code", Value: "US"}}
	server.handleUpsertUpstreamProxyCountryRule(c)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing proxy status=%d body=%s", rr.Code, rr.Body.String())
	}
}
