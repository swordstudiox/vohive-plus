package device

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func newPoolWithWorkerForSync(id string, cfg config.DeviceConfig) (*Pool, *Worker) {
	p := &Pool{workers: map[string]*Worker{}}
	w := &Worker{ID: id, Config: cfg}
	p.workers[id] = w
	return p, w
}

// 开 VoWiFi：同步 w.Config 为 vowifi=T、airplane=T、network=F（否则概览仍显示蜂窝面板）。
// 关 VoWiFi：仅清 vowifi，不在此清 airplane——airplane 是用户飞行意图，交由
// resolveAndApplyPolicy 按卡策略重投影回退。
func TestSetWorkerVoWiFiPolicySyncsConfig(t *testing.T) {
	p, w := newPoolWithWorkerForSync("wwan0", config.DeviceConfig{NetworkEnabled: true})

	p.SetWorkerVoWiFiPolicy("wwan0", true)
	if !w.Config.VoWiFiEnabled || !w.Config.AirplaneEnabled || w.Config.NetworkEnabled {
		t.Fatalf("开 vowifi 应 vowifi=T airplane=T network=F: %+v", w.Config)
	}

	p.SetWorkerVoWiFiPolicy("wwan0", false)
	if w.Config.VoWiFiEnabled {
		t.Fatalf("关 vowifi 应清 vowifi=F: %+v", w.Config)
	}
}

func TestSetWorkerVoWiFiPolicyPreservesNetworkRestoreIntent(t *testing.T) {
	p, w := newPoolWithWorkerForSync("wwan0", config.DeviceConfig{NetworkEnabled: true})

	p.SetWorkerVoWiFiPolicy("wwan0", true)

	if !w.restoreNetworkAfterVoWiFi {
		t.Fatal("开 VoWiFi 前原本允许蜂窝数据，启动失败后应恢复网络")
	}
}

func TestSetWorkerVoWiFiPolicyPreservesConnectedDataRestoreIntent(t *testing.T) {
	p, w := newPoolWithWorkerForSync("wwan0", config.DeviceConfig{NetworkEnabled: false})
	w.netOverride = &fakeController{connected: true}

	p.SetWorkerVoWiFiPolicy("wwan0", true)

	if !w.restoreNetworkAfterVoWiFi {
		t.Fatal("开 VoWiFi 前数据面实际已连接，启动失败后应恢复网络")
	}
}

// 开飞行：同步 airplane=T、vowifi=F、network=F；关飞行仅清 airplane。
func TestSetWorkerAirplanePolicySyncsConfig(t *testing.T) {
	p, w := newPoolWithWorkerForSync("wwan0", config.DeviceConfig{VoWiFiEnabled: true, NetworkEnabled: true})

	p.SetWorkerAirplanePolicy("wwan0", true)
	if !w.Config.AirplaneEnabled || w.Config.VoWiFiEnabled || w.Config.NetworkEnabled {
		t.Fatalf("开飞行应 airplane=T vowifi=F network=F: %+v", w.Config)
	}

	p.SetWorkerAirplanePolicy("wwan0", false)
	if w.Config.AirplaneEnabled {
		t.Fatalf("关飞行应 airplane=F: %+v", w.Config)
	}
}

// 开网络：互斥关 vowifi/airplane，并同步 ip/apn。
func TestSetWorkerNetworkPolicyMutualExclusion(t *testing.T) {
	p, w := newPoolWithWorkerForSync("wwan0", config.DeviceConfig{VoWiFiEnabled: true, AirplaneEnabled: true})

	p.SetWorkerNetworkPolicy("wwan0", true, "v4v6", "ims")
	if !w.Config.NetworkEnabled || w.Config.VoWiFiEnabled || w.Config.AirplaneEnabled {
		t.Fatalf("开网络应互斥关 vowifi/airplane: %+v", w.Config)
	}
	if w.Config.IPVersion != "v4v6" || w.Config.APN != "ims" {
		t.Fatalf("ip/apn 应同步: %+v", w.Config)
	}
}
