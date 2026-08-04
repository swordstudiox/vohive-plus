package e911

import (
	"os"
	"strings"
	"testing"

	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestCoordinatorDoesNotRunEntitlementProbes(t *testing.T) {
	body, err := os.ReadFile("coordinator.go")
	if err != nil {
		t.Fatalf("read coordinator.go: %v", err)
	}
	source := string(body)
	for _, forbidden := range []string{
		"runATTBootstrapProbe",
		"runTS43Probe",
		"VOHIVE_E911_ATT_CACHED_TOKEN",
		"debugATTCachedToken",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("coordinator must not contain experimental entitlement hook %q", forbidden)
		}
	}
}

func TestHTTPClientAdapterUsesConfiguredEntitlementClient(t *testing.T) {
	client := &fakeEntitlementHTTPClient{
		body: []byte(`[{"status":6004,"response-id":3}]`),
	}
	adapter := httpClientAdapter{
		deviceID: "dev-1",
		client:   client,
	}

	resp, err := adapter.Do(&runtimee911.HTTPRequest{
		Method: "POST",
		URL:    "https://sentitlement2.mobile.att.net/",
		Headers: []runtimee911.HeaderPair{
			{Key: "x-protocol-version", Value: "2"},
		},
		Body: []byte("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests=%d want 1", len(client.requests))
	}
	gotReq := client.requests[0]
	if gotReq.URL != "https://sentitlement2.mobile.att.net/" {
		t.Fatalf("url=%q", gotReq.URL)
	}
	if len(gotReq.Headers) != 1 || gotReq.Headers[0].Key != "x-protocol-version" || gotReq.Headers[0].Value != "2" {
		t.Fatalf("headers=%+v", gotReq.Headers)
	}
	if string(gotReq.Body) != "payload" {
		t.Fatalf("body=%q", gotReq.Body)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(resp.Body) != `[{"status":6004,"response-id":3}]` {
		t.Fatalf("response body=%q", resp.Body)
	}
}

func TestBuildRuntimeE911IdentityIgnoresDebugCachedTokenEnv(t *testing.T) {
	t.Setenv("VOHIVE_E911_ATT_CACHED_TOKEN", " cached-token-value ")

	got := buildRuntimeE911Identity(modem.DeviceStatus{
		IMSI: "310280233641503",
		IMEI: "356306952701762",
	}, "310", "280", "VoHive")

	if got.CachedToken != "" {
		t.Fatalf("cached token=%q", got.CachedToken)
	}
	if got.SIPUsername != "310280233641503@private.att.net" {
		t.Fatalf("sip username=%q", got.SIPUsername)
	}
}

type fakeEntitlementHTTPClient struct {
	body     []byte
	requests []*runtimee911.HTTPRequest
}

func (f *fakeEntitlementHTTPClient) Do(req *runtimee911.HTTPRequest) (*runtimee911.HTTPResponse, error) {
	f.requests = append(f.requests, req)
	return &runtimee911.HTTPResponse{StatusCode: 200, Body: append([]byte(nil), f.body...)}, nil
}
