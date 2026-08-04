package device

import (
	"context"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/cardpolicy"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
)

func TestApplyPolicyProjectsFields(t *testing.T) {
	w := &Worker{ID: "wwan0"}
	applyPolicyToWorker(w, cardpolicy.Policy{
		ICCID: "x", NetworkEnabled: true, VoWiFiEnabled: true,
		AirplaneEnabled: true, IPVersion: "v4v6", APN: "ims",
	})
	if !w.Config.NetworkEnabled || !w.Config.VoWiFiEnabled || !w.Config.AirplaneEnabled {
		t.Fatalf("开关未投影: %+v", w.Config)
	}
	if w.Config.IPVersion != "v4v6" || w.Config.APN != "ims" {
		t.Fatalf("ip/apn 未投影: %+v", w.Config)
	}
	if !w.Config.SMSEnabled {
		t.Fatal("SMS 应恒为 true")
	}
}

// 投影时按策略真正进入飞行模式：当前在线 ⇒ 切 RFOff。
func TestProjectionEntersAirplaneMode(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	stub := &workerStatusBackendStub{opMode: backend.ModeOnline}
	w := &Worker{ID: "wwan0", Backend: stub}

	p.enterAirplaneModeFromPolicy(w, "test")

	if len(stub.setOpModeCalls) != 1 || stub.setOpModeCalls[0] != backend.ModeRFOff {
		t.Fatalf("应切到 RFOff: %+v", stub.setOpModeCalls)
	}
}

// 幂等：已在飞行模式时不重复下发 SetOperatingMode。
func TestProjectionEnterAirplaneIdempotent(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	stub := &workerStatusBackendStub{opMode: backend.ModeRFOff}
	w := &Worker{ID: "wwan0", Backend: stub}

	p.enterAirplaneModeFromPolicy(w, "test")

	if len(stub.setOpModeCalls) != 0 {
		t.Fatalf("已在飞行不应重复切: %+v", stub.setOpModeCalls)
	}
}

// 投影时按策略退出飞行：当前 RFOff 且策略不要求飞行 ⇒ 切回 Online。
func TestProjectionExitsAirplaneMode(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	stub := &workerStatusBackendStub{opMode: backend.ModeRFOff}
	w := &Worker{ID: "wwan0", Backend: stub}

	p.exitAirplaneModeIfNeeded(w, "test")

	if len(stub.setOpModeCalls) != 1 || stub.setOpModeCalls[0] != backend.ModeOnline {
		t.Fatalf("应切回 Online: %+v", stub.setOpModeCalls)
	}
}

// 已在线时退出飞行是 no-op。
func TestProjectionExitAirplaneSkipsWhenOnline(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	stub := &workerStatusBackendStub{opMode: backend.ModeOnline}
	w := &Worker{ID: "wwan0", Backend: stub}

	p.exitAirplaneModeIfNeeded(w, "test")

	if len(stub.setOpModeCalls) != 0 {
		t.Fatalf("已在线不应切: %+v", stub.setOpModeCalls)
	}
}

type stubPolicyResolver struct {
	pol cardpolicy.Policy
	err error
}

func (s *stubPolicyResolver) Resolve(iccid string) (cardpolicy.Policy, error) {
	return s.pol, s.err
}

func TestResolveAndApplyPolicy_EmptyICCID(t *testing.T) {
	p := &Pool{}
	w := &Worker{ID: "wwan0"}
	p.SetPolicyResolver(&stubPolicyResolver{})

	res := p.resolveAndApplyPolicy(w, "test")
	if res.Applied || res.Reason != "iccid_empty" {
		t.Fatalf("空 ICCID 应返回 iccid_empty: %+v", res)
	}
}

func TestResolveAndApplyPolicy_ResolvesAndProjects(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "123", AirplaneEnabled: true},
	})
	stub := &workerStatusBackendStub{opMode: backend.ModeOnline}
	w := &Worker{ID: "wwan0", Backend: stub}
	w.state.Identity.ICCID = "123"

	res := p.resolveAndApplyPolicy(w, "test")
	if !res.Applied {
		t.Fatalf("应成功应用: %+v", res)
	}
	if !w.Config.AirplaneEnabled {
		t.Fatal("策略投影失败")
	}
	if len(stub.setOpModeCalls) != 1 || stub.setOpModeCalls[0] != backend.ModeRFOff {
		t.Fatalf("应切入飞行模式: %+v", stub.setOpModeCalls)
	}
}

func TestResolveAndApplyPolicyDoesNotRecoverVoWiFiWhenCardPolicyDisabled(t *testing.T) {
	p := NewPool(nil)
	defer p.cancel()
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "123", VoWiFiEnabled: false},
	})
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}
	w := &Worker{ID: "wwan0"}
	w.state.Identity.ICCID = "123"
	w.state.Identity.IMSI = "001010000000001"

	res := p.resolveAndApplyPolicy(w, "test")
	if !res.Applied {
		t.Fatalf("应成功应用: %+v", res)
	}

	select {
	case cmd := <-commands:
		t.Fatalf("卡策略未开启 VoWiFi 时不应调度恢复: %+v", cmd)
	case <-time.After(120 * time.Millisecond):
	}
}

func TestRefreshIdentityAndApplyCardPolicyDoesNotFallbackToConfiguredNetworkWithoutResolver(t *testing.T) {
	p := NewPool(nil)
	defer p.cancel()

	ctrl := &fakeController{}
	w := &Worker{
		ID: "wwan0",
		Config: config.DeviceConfig{
			ID:             "wwan0",
			NetworkEnabled: true,
		},
		Backend: &workerStartupIdentityBackendStub{
			liveICCID: "898600000000000001",
			liveIMSI:  "460001234567890",
		},
		netOverride: ctrl,
	}

	result, err := p.refreshIdentityAndApplyCardPolicy(w, "startup_post_apply")
	if err != nil {
		t.Fatalf("refreshIdentityAndApplyCardPolicy() error=%v", err)
	}
	if result.ICCID != "898600000000000001" || result.IMSI != "460001234567890" {
		t.Fatalf("live identity result mismatch: %+v", result)
	}
	if ctrl.connected {
		t.Fatal("无 policy resolver 时不应回退到旧 worker.Config 连接数据网络")
	}
}
