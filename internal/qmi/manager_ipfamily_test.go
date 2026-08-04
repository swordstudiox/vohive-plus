package qmicore

import (
	"testing"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestBuildQMIManagerConfigIPFamily(t *testing.T) {
	cases := []struct {
		ver    string
		wantV4 bool
		wantV6 bool
	}{
		{"", true, false},
		{"v4", true, false},
		{"v6", false, true},
		{"v4v6", true, true},
	}

	for _, c := range cases {
		cfg := config.DeviceConfig{ID: "d1", APN: "internet", IPVersion: c.ver}
		out := buildQMIManagerConfig(cfg, qmimanager.ModemDevice{})
		if out.EnableIPv4 != c.wantV4 || out.EnableIPv6 != c.wantV6 {
			t.Errorf("ver=%q got (v4=%v v6=%v) want (v4=%v v6=%v)",
				c.ver, out.EnableIPv4, out.EnableIPv6, c.wantV4, c.wantV6)
		}
	}
}
