package device

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/cardpolicy"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func newVoWiFiLifecycleControllerForTest(p *Pool) *vowifihost.LifecycleController {
	return vowifihost.NewLifecycleController(vowifihost.LifecycleControllerOptions{
		IsActive: p.IsVoWiFiActive,
	})
}

func TestVoWiFiControllerSerializesCommandsPerDevice(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)

	release := make(chan struct{})
	started := make(chan string, 2)
	var mu sync.Mutex
	var order []string

	c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		mu.Lock()
		order = append(order, cmd.Kind.String())
		mu.Unlock()
		started <- cmd.Kind.String()
		<-release
		return nil
	}

	done := make(chan error, 2)
	go func() {
		done <- c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: "dev-a", Kind: vowifihost.LifecycleCommandEnable})
	}()
	select {
	case got := <-started:
		if got != "enable" {
			t.Fatalf("first command = %q, want enable", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first command to start")
	}

	go func() {
		done <- c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: " dev-a ", Kind: vowifihost.LifecycleCommandDisable})
	}()

	select {
	case got := <-started:
		t.Fatalf("second command started too early: %q", got)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	select {
	case got := <-started:
		if got != "disable" {
			t.Fatalf("second command = %q, want disable", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second command to start")
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("submit returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for submit to finish")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"enable", "disable"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestVoWiFiControllerAllowsDifferentDevicesInParallel(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)

	release := make(chan struct{})
	started := make(chan string, 2)

	c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		started <- cmd.DeviceID
		<-release
		return nil
	}

	done := make(chan error, 2)
	go func() {
		done <- c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: "dev-a", Kind: vowifihost.LifecycleCommandEnable})
	}()
	go func() {
		done <- c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: "dev-b", Kind: vowifihost.LifecycleCommandEnable})
	}()

	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case deviceID := <-started:
			seen[deviceID] = true
		case <-deadline:
			t.Fatalf("timed out waiting for both commands to start, saw %v", seen)
		}
	}

	close(release)

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("submit returned error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for submit to finish")
		}
	}
}

func TestDuplicateEnableDoesNotInvalidateActiveVoWiFiLifecycleSink(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-active-enable"
	generation := p.voWiFiHost().NextLifecycleGeneration(deviceID)

	p.voWiFiRuntimeStore().SetInstance(deviceID, &runtimehost.Instance{})

	if err := p.EnableVoWiFi(deviceID); err != nil {
		t.Fatalf("EnableVoWiFi() duplicate active error = %v", err)
	}
	if current := p.voWiFiHost().CurrentLifecycleGeneration(deviceID); current != generation {
		t.Fatalf("current generation = %d, want unchanged active generation %d", current, generation)
	}
}

func TestEnableVoWiFiWhenReadySubmitsControllerCommand(t *testing.T) {
	p := NewPool(&config.Config{})
	p.workers["dev-1"] = &Worker{ID: "dev-1", Backend: &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline}}

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	if err := p.enableVoWiFiWhenReady("dev-1", -time.Nanosecond, "test"); err != nil {
		t.Fatalf("enableVoWiFiWhenReady() error = %v", err)
	}

	want := []string{"enable:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
}

func TestHandleVoWiFiStartupErrorAPDUBusyStaysBelowWarn(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	p := NewPool(&config.Config{})
	defer p.cancel()
	errBusy := fmt.Errorf("wrapped: %w", apduarbiter.ErrAPDUBusy)
	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	err := p.handleVoWiFiStartupError("trace-apdu-busy", "dev-1", "", 0, time.Now(), &Worker{ID: "dev-1"}, runtimehost.State{LastErrorClass: "aka"}, errBusy)
	if !errors.Is(err, apduarbiter.ErrAPDUBusy) {
		t.Fatalf("handleVoWiFiStartupError() error = %v, want ErrAPDUBusy", err)
	}

	entries := drainLoggerEntries(ch)
	if countLogSuffix(entries, "warn", "VoWiFi 失败汇总") != 0 {
		t.Fatalf("VoWiFi failure summary warn emitted for APDU busy")
	}
	if countLogSuffix(entries, "error", "VoWiFi 启动失败") != 0 {
		t.Fatalf("VoWiFi startup error emitted for APDU busy")
	}
	if countLogSuffix(entries, "debug", "VoWiFi 启动遇到 APDU busy，等待短退避恢复") != 1 {
		t.Fatalf("APDU busy debug count = %d, want 1", countLogSuffix(entries, "debug", "VoWiFi 启动遇到 APDU busy，等待短退避恢复"))
	}
}

func drainLoggerEntries(ch <-chan logger.LogEntry) []logger.LogEntry {
	entries := make([]logger.LogEntry, 0)
	for {
		select {
		case entry := <-ch:
			entries = append(entries, entry)
		default:
			return entries
		}
	}
}

func countLogSuffix(entries []logger.LogEntry, level, suffix string) int {
	count := 0
	for _, entry := range entries {
		if entry.Level == level && strings.HasSuffix(entry.Message, suffix) {
			count++
		}
	}
	return count
}

func TestPoolLifecycleEntrypointsSubmitControllerCommands(t *testing.T) {
	p := NewPool(&config.Config{})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	if err := p.EnableVoWiFi("dev-1"); err != nil {
		t.Fatalf("EnableVoWiFi() error = %v", err)
	}
	if err := p.DisableVoWiFi("dev-1"); err != nil {
		t.Fatalf("DisableVoWiFi() error = %v", err)
	}
	if err := p.RestartVoWiFi("dev-1"); err != nil {
		t.Fatalf("RestartVoWiFi() error = %v", err)
	}

	want := []string{"enable:dev-1", "disable:dev-1", "restart:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
}

func TestEnableVoWiFiStillBlocksDuringESIMSwitchingBeforeSubmitting(t *testing.T) {
	p := NewPool(&config.Config{})
	p.switchMu.Lock()
	p.switchingDevices["dev-1"] = true
	p.switchMu.Unlock()

	submitted := false
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		submitted = true
		return nil
	}

	err := p.EnableVoWiFi("dev-1")
	if err == nil {
		t.Fatal("EnableVoWiFi() expected error when eSIM switching, got nil")
	}
	if !strings.Contains(err.Error(), "正在切卡") {
		t.Fatalf("EnableVoWiFi() error = %v, want contains %q", err, "正在切卡")
	}
	if submitted {
		t.Fatal("EnableVoWiFi() submitted command despite eSIM switching gate")
	}
}

func TestRecoverCommandRunsTeardownThenEnable(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)
	deviceID := "dev-recover"

	var mu sync.Mutex
	var steps []string

	c.TestRun = nil
	c.RecoverRunForTest = func(ctx context.Context, deviceID, reason, overrideEPDG string) error {
		mu.Lock()
		defer mu.Unlock()
		steps = append(steps, "recover:"+deviceID+":"+reason+":"+overrideEPDG)
		return nil
	}

	if err := c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandRecover, Reason: "ims_failure"}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"recover:dev-recover:ims_failure:"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}

func TestVoWiFiLifecycleControllerAssignsGenerationBeforeRunningSessionCommands(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)
	deviceID := "dev-generation"
	seed := c.NextGeneration(deviceID)

	tests := []struct {
		name string
		cmd  vowifihost.LifecycleCommand
		want uint64
	}{
		{
			name: "enable",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandEnable},
			want: seed + 1,
		},
		{
			name: "disable",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandDisable},
			want: seed + 2,
		},
		{
			name: "recover",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandRecover, Reason: "recover"},
			want: seed + 3,
		},
		{
			name: "restart",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandRestart},
			want: seed + 4,
		},
		{
			name: "switch begin",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandSwitchBegin},
			want: seed + 5,
		},
		{
			name: "switch end restore",
			cmd:  vowifihost.LifecycleCommand{DeviceID: deviceID, Kind: vowifihost.LifecycleCommandSwitchEnd, RestoreRadio: true},
			want: seed + 6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
				if cmd.Generation != tc.want {
					t.Fatalf("generation = %d, want %d", cmd.Generation, tc.want)
				}
				return nil
			}

			if err := c.Submit(context.Background(), tc.cmd); err != nil {
				t.Fatalf("submit() error = %v", err)
			}
		})
	}
}

func TestVoWiFiLifecycleControllerPreservesPresetGeneration(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)
	preset := uint64(42)

	c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		if cmd.Generation != preset {
			t.Fatalf("generation = %d, want %d", cmd.Generation, preset)
		}
		return nil
	}

	if err := c.Submit(context.Background(), vowifihost.LifecycleCommand{
		DeviceID:   "dev-preset",
		Kind:       vowifihost.LifecycleCommandEnable,
		Generation: preset,
	}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
}

func TestVoWiFiLifecycleControllerIgnoresStalePresetGeneration(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)
	deviceID := "dev-stale-preset"
	stale := c.NextGeneration(deviceID)
	current := c.NextGeneration(deviceID)
	if current <= stale {
		t.Fatalf("current generation = %d, want > %d", current, stale)
	}

	var ran atomic.Int32
	c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		ran.Add(1)
		return nil
	}

	if err := c.Submit(context.Background(), vowifihost.LifecycleCommand{
		DeviceID:   deviceID,
		Kind:       vowifihost.LifecycleCommandRecover,
		Reason:     "apdu_busy",
		Generation: stale,
	}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
	if got := ran.Load(); got != 0 {
		t.Fatalf("stale command ran %d times, want 0", got)
	}
	if got := c.CurrentGeneration(deviceID); got != current {
		t.Fatalf("current generation = %d, want unchanged %d", got, current)
	}
}

// TestClaimStartedVoWiFiAppRejectsStaleRuntimeEpoch 测试当启动的 Epoch 过期时，claimStartedVoWiFiApp 应拒绝接管该 App
func TestClaimStartedVoWiFiAppRejectsStaleRuntimeEpoch(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-stale-start"
	stale := p.currentVoWiFiRuntimeEpoch(deviceID)
	current := p.invalidateVoWiFiRuntime(deviceID, "test")

	app := &runtimehost.Instance{}
	// 传入过期的 stale epoch 应该返回 false，表明接管失败
	if claimed := p.claimStartedVoWiFiApp(deviceID, app, stale); claimed {
		t.Fatal("stale runtime epoch should not claim active VoWiFi app")
	}
	if p.IsVoWiFiActive(deviceID) {
		t.Fatal("stale runtime epoch should not activate VoWiFi")
	}
	if current <= stale {
		t.Fatalf("current epoch = %d, want > %d", current, stale)
	}
}

// TestClaimStartedVoWiFiAppAcceptsCurrentRuntimeEpoch 测试当 Epoch 匹配时，claimStartedVoWiFiApp 应成功接管该 App
func TestClaimStartedVoWiFiAppAcceptsCurrentRuntimeEpoch(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-current-start"
	current := p.currentVoWiFiRuntimeEpoch(deviceID)

	app := &runtimehost.Instance{}
	// 传入当前的 current epoch 应该接管成功
	if claimed := p.claimStartedVoWiFiApp(deviceID, app, current); !claimed {
		t.Fatal("current runtime epoch should claim active VoWiFi app")
	}
	if !p.IsVoWiFiActive(deviceID) {
		t.Fatal("current runtime epoch should activate VoWiFi")
	}
}

// TestDisableVoWiFiInvalidatesRuntimeEpochBeforeLifecycleRun 测试在执行停用逻辑前，DisableVoWiFi 是否已使 Epoch 失效
func TestDisableVoWiFiInvalidatesRuntimeEpochBeforeLifecycleRun(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-disable-invalidates"
	before := p.currentVoWiFiRuntimeEpoch(deviceID)
	var epochDuringRun uint64

	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		if cmd.Kind != vowifihost.LifecycleCommandDisable {
			t.Fatalf("kind = %q, want disable", cmd.Kind.String())
		}
		epochDuringRun = p.currentVoWiFiRuntimeEpoch(deviceID)
		return nil
	}

	if err := p.DisableVoWiFi(deviceID); err != nil {
		t.Fatalf("DisableVoWiFi() error = %v", err)
	}
	after := p.currentVoWiFiRuntimeEpoch(deviceID)
	// 停用后 Epoch 应该递增
	if after <= before {
		t.Fatalf("runtime epoch after disable = %d, want > %d", after, before)
	}
	if epochDuringRun != after {
		t.Fatalf("epoch during lifecycle run = %d, want %d", epochDuringRun, after)
	}
}

func TestDisableVoWiFiAppliesCurrentCardPolicyAfterRuntimeStops(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	deviceID := "dev-disable-policy"
	backendStub := &workerStatusBackendStub{opMode: backend.ModeRFOff}
	w := &Worker{
		ID:      deviceID,
		Config:  config.DeviceConfig{ID: deviceID, VoWiFiEnabled: true, AirplaneEnabled: true},
		Backend: backendStub,
	}
	w.state.Identity.ICCID = "iccid-disable-policy"
	w.state.Identity.IMSI = "001010000000001"
	p.workers[deviceID] = w
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "iccid-disable-policy", VoWiFiEnabled: false, AirplaneEnabled: false},
	})
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		if cmd.Kind != vowifihost.LifecycleCommandDisable {
			t.Fatalf("kind = %q, want disable", cmd.Kind.String())
		}
		return nil
	}

	if err := p.DisableVoWiFi(deviceID); err != nil {
		t.Fatalf("DisableVoWiFi() error = %v", err)
	}

	if len(backendStub.setOpModeCalls) != 1 || backendStub.setOpModeCalls[0] != backend.ModeOnline {
		t.Fatalf("DisableVoWiFi 后应按当前卡策略退出飞行模式: %+v", backendStub.setOpModeCalls)
	}
	if w.Config.VoWiFiEnabled || w.Config.AirplaneEnabled {
		t.Fatalf("worker config 未投影当前卡策略: %+v", w.Config)
	}
}

// TestDisableVoWiFiWithAirplaneIntentStaysInAirplane 锁定回退语义：当卡策略保留了
// 用户的飞行意图（airplane=true）时，关闭 VoWiFi 应停留在飞行（RFOff），而非切回 Online。
func TestDisableVoWiFiWithAirplaneIntentStaysInAirplane(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	deviceID := "dev-disable-airplane"
	backendStub := &workerStatusBackendStub{opMode: backend.ModeRFOff}
	w := &Worker{
		ID:      deviceID,
		Config:  config.DeviceConfig{ID: deviceID, VoWiFiEnabled: true, AirplaneEnabled: true},
		Backend: backendStub,
	}
	w.state.Identity.ICCID = "iccid-disable-airplane"
	w.state.Identity.IMSI = "001010000000002"
	p.workers[deviceID] = w
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "iccid-disable-airplane", VoWiFiEnabled: false, AirplaneEnabled: true},
	})
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		if cmd.Kind != vowifihost.LifecycleCommandDisable {
			t.Fatalf("kind = %q, want disable", cmd.Kind.String())
		}
		return nil
	}

	if err := p.DisableVoWiFi(deviceID); err != nil {
		t.Fatalf("DisableVoWiFi() error = %v", err)
	}

	// 已处于 RFOff，纯飞行投影应跳过 SetOperatingMode；关键是绝不能切回 Online。
	for _, m := range backendStub.setOpModeCalls {
		if m == backend.ModeOnline {
			t.Fatalf("保留飞行意图时不应切回 Online: %+v", backendStub.setOpModeCalls)
		}
	}
	if w.Config.VoWiFiEnabled || !w.Config.AirplaneEnabled {
		t.Fatalf("关 VoWiFi 后应回退到飞行: %+v", w.Config)
	}
}

func TestVoWiFiLifecycleControllerAssignsNonZeroGenerationToRestart(t *testing.T) {
	p := NewPool(&config.Config{})
	c := newVoWiFiLifecycleControllerForTest(p)

	c.TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		if cmd.Kind != vowifihost.LifecycleCommandRestart {
			t.Fatalf("kind = %q, want %q", cmd.Kind.String(), vowifihost.LifecycleCommandRestart.String())
		}
		if cmd.Generation == 0 {
			t.Fatal("generation = 0, want non-zero")
		}
		return nil
	}

	if err := c.Submit(context.Background(), vowifihost.LifecycleCommand{DeviceID: "dev-restart", Kind: vowifihost.LifecycleCommandRestart}); err != nil {
		t.Fatalf("submit() error = %v", err)
	}
}
