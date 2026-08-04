package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	proxytraffic "github.com/swordstudiox/vohive-plus/internal/proxy/traffic"
)

func TestOverviewStreamEmitVersionIgnoresRuntimeUpdatedAt(t *testing.T) {
	last := newOverviewStreamEmitVersion(deviceMgmtOverviewLiteItem{
		VoWiFiActive: true,
		VoWiFiRuntime: &voWiFiRuntimeDTO{
			Phase:          "registering",
			TunnelReady:    true,
			IMSReady:       false,
			SMSReady:       false,
			LastErrorClass: "",
			UpdatedAt:      time.Unix(1, 0),
		},
	})
	curr := newOverviewStreamEmitVersion(deviceMgmtOverviewLiteItem{
		VoWiFiActive: true,
		VoWiFiRuntime: &voWiFiRuntimeDTO{
			Phase:          "registering",
			TunnelReady:    true,
			IMSReady:       false,
			SMSReady:       false,
			LastErrorClass: "",
			UpdatedAt:      time.Unix(2, 0),
		},
	})

	if !shouldSkipOverviewStatePush(&last, curr) {
		t.Fatal("state push was not skipped when only UpdatedAt changed")
	}
}

func TestOverviewStreamEmitVersionTracksRuntimeBusinessState(t *testing.T) {
	baseItem := deviceMgmtOverviewLiteItem{
		VoWiFiActive: true,
		VoWiFiRuntime: &voWiFiRuntimeDTO{
			Phase:       "registering",
			TunnelReady: true,
		},
	}
	base := newOverviewStreamEmitVersion(baseItem)

	tests := []struct {
		name string
		item deviceMgmtOverviewLiteItem
	}{
		{
			name: "phase changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "ready", TunnelReady: true}},
		},
		{
			name: "tunnel changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "registering", TunnelReady: false}},
		},
		{
			name: "ims changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "registering", TunnelReady: true, IMSReady: true}},
		},
		{
			name: "sms changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "registering", TunnelReady: true, SMSReady: true}},
		},
		{
			name: "last error class changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "registering", TunnelReady: true, LastErrorClass: "ims_register_failed"}},
		},
		{
			name: "active changed",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: false, VoWiFiRuntime: &voWiFiRuntimeDTO{Phase: "registering", TunnelReady: true}},
		},
		{
			name: "runtime disappeared",
			item: deviceMgmtOverviewLiteItem{VoWiFiActive: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			curr := newOverviewStreamEmitVersion(tc.item)
			if shouldSkipOverviewStatePush(&base, curr) {
				t.Fatal("state push was skipped despite business state change")
			}
		})
	}

	empty := newOverviewStreamEmitVersion(deviceMgmtOverviewLiteItem{VoWiFiActive: true})
	appeared := newOverviewStreamEmitVersion(baseItem)
	if shouldSkipOverviewStatePush(&empty, appeared) {
		t.Fatal("state push was skipped when runtime state appeared")
	}
}

func TestOverviewDetailLiveRefreshRequestedDefaultsToCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "absent", url: "/devices/dev1/overview", want: false},
		{name: "refresh false", url: "/devices/dev1/overview?refresh=false", want: false},
		{name: "refresh true", url: "/devices/dev1/overview?refresh=true", want: true},
		{name: "refresh one", url: "/devices/dev1/overview?refresh=1", want: true},
		{name: "live true", url: "/devices/dev1/overview?live=true", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, tt.url, nil)

			if got := overviewDetailLiveRefreshRequested(ctx); got != tt.want {
				t.Fatalf("overviewDetailLiveRefreshRequested()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestOverviewTrafficStreamStateFollowsNetworkConnectivity(t *testing.T) {
	sub := &fakeRealtimeTrafficSubscriber{}
	state := overviewTrafficStreamState{
		subscriber: sub,
		deviceID:   "dev-a",
		ctx:        context.Background(),
	}

	if ch := state.sync(deviceMgmtOverviewLiteItem{NetworkEnabled: false, NetworkConnected: false}); ch != nil {
		t.Fatalf("channel=%v want nil when network disabled", ch)
	}
	if sub.subscribeCalls != 0 {
		t.Fatalf("subscribeCalls=%d want 0 before network is active", sub.subscribeCalls)
	}

	ch := state.sync(deviceMgmtOverviewLiteItem{NetworkEnabled: true, NetworkConnected: true})
	if ch == nil {
		t.Fatal("expected traffic channel when network is connected")
	}
	again := state.sync(deviceMgmtOverviewLiteItem{NetworkEnabled: true, NetworkConnected: true})
	if again != ch {
		t.Fatal("expected connected sync to reuse existing traffic subscription")
	}
	if sub.subscribeCalls != 1 {
		t.Fatalf("subscribeCalls=%d want 1 after repeated connected sync", sub.subscribeCalls)
	}

	if ch := state.sync(deviceMgmtOverviewLiteItem{NetworkEnabled: true, NetworkConnected: false}); ch != nil {
		t.Fatalf("channel=%v want nil after network disconnects", ch)
	}
	if sub.unsubscribeCalls != 1 {
		t.Fatalf("unsubscribeCalls=%d want 1 after network disconnects", sub.unsubscribeCalls)
	}

	if ch := state.sync(deviceMgmtOverviewLiteItem{NetworkEnabled: true, NetworkConnected: true}); ch == nil {
		t.Fatal("expected traffic channel after network reconnects")
	}
	if sub.subscribeCalls != 2 {
		t.Fatalf("subscribeCalls=%d want 2 after reconnect", sub.subscribeCalls)
	}
}

type fakeRealtimeTrafficSubscriber struct {
	subscribeCalls   int
	unsubscribeCalls int
}

func (f *fakeRealtimeTrafficSubscriber) Subscribe(ctx context.Context, deviceID string) (<-chan proxytraffic.RealtimeSnapshot, func()) {
	f.subscribeCalls++
	ch := make(chan proxytraffic.RealtimeSnapshot)
	return ch, func() {
		f.unsubscribeCalls++
	}
}
