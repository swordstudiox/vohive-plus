package device

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

type qmiRegistrationTestController struct {
	simStatuses []qmi.SIMStatus
	servingSeq  []*backend.ServingSystem
	opMode      backend.OperatingMode

	registerErr                   error
	forceNetworkSearchErr         error
	allowManualRegister           bool
	registerReqs                  []backend.NASRegisterRequest
	registerCalls                 int
	forceNetworkSearchCalls       int
	systemSelectionAutomaticCalls int
	attachCalls                   []bool
	setModeCalls                  []backend.OperatingMode
	servingCallTimes              []time.Time
}

func (s *qmiRegistrationTestController) GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error) {
	if len(s.simStatuses) == 0 {
		return qmi.SIMReady, nil
	}
	status := s.simStatuses[0]
	if len(s.simStatuses) > 1 {
		s.simStatuses = s.simStatuses[1:]
	}
	return status, nil
}

func (s *qmiRegistrationTestController) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	s.servingCallTimes = append(s.servingCallTimes, time.Now())
	if len(s.servingSeq) == 0 {
		return &backend.ServingSystem{RegStatus: 1, RegStatusText: "已注册(本地)", PSAttached: true}, nil
	}
	current := s.servingSeq[0]
	if len(s.servingSeq) > 1 {
		s.servingSeq = s.servingSeq[1:]
	}
	return current, nil
}

func (s *qmiRegistrationTestController) NASInitiateNetworkRegister(ctx context.Context, req backend.NASRegisterRequest) error {
	if req.Mode != "automatic" && !s.allowManualRegister {
		return errors.New("expected automatic registration")
	}
	s.registerReqs = append(s.registerReqs, req)
	s.registerCalls++
	if s.registerErr != nil {
		return s.registerErr
	}
	return nil
}

func (s *qmiRegistrationTestController) NASSetSystemSelectionAutomatic(ctx context.Context) error {
	s.systemSelectionAutomaticCalls++
	return nil
}

func (s *qmiRegistrationTestController) NASForceNetworkSearch(ctx context.Context) error {
	s.forceNetworkSearchCalls++
	if s.forceNetworkSearchErr != nil {
		return s.forceNetworkSearchErr
	}
	return nil
}

func (s *qmiRegistrationTestController) NASAttachDetach(ctx context.Context, attached bool) error {
	s.attachCalls = append(s.attachCalls, attached)
	return nil
}

func (s *qmiRegistrationTestController) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	if s.opMode == 0 {
		return backend.ModeOnline, nil
	}
	return s.opMode, nil
}

func (s *qmiRegistrationTestController) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	s.setModeCalls = append(s.setModeCalls, mode)
	s.opMode = mode
	return nil
}

func qmiSearchingServingSeq(count int, final *backend.ServingSystem) []*backend.ServingSystem {
	seq := make([]*backend.ServingSystem, 0, count+1)
	for i := 0; i < count; i++ {
		seq = append(seq, &backend.ServingSystem{RegStatus: 2, RegStatusText: "搜索中"})
	}
	if final != nil {
		seq = append(seq, final)
	}
	return seq
}

func TestEnsureQMIRegistrationRegistersAndAttaches(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 0, RegStatusText: "未注册"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 1, RegStatusText: "已注册(本地)", PSAttached: false},
			{RegStatus: 1, RegStatusText: "已注册(本地)", PSAttached: true},
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  6,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.registerCalls != 1 {
		t.Fatalf("registerCalls=%d want 1", ctrl.registerCalls)
	}
	if len(ctrl.attachCalls) != 1 || !ctrl.attachCalls[0] {
		t.Fatalf("attachCalls=%v want [true]", ctrl.attachCalls)
	}
}

func TestEnsureQMIRegistrationWaitsForSIMReady(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMNotReady, qmi.SIMReady},
		servingSeq:  []*backend.ServingSystem{{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true}},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  4,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
}

func TestEnsureQMIRegistrationRegistersWhenSearching(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  4,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.registerCalls != 1 {
		t.Fatalf("registerCalls=%d want 1", ctrl.registerCalls)
	}
}

func TestEnsureQMIRegistrationSuppressesRadioCycleWhenOperatorScanActive(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq:  qmiSearchingServingSeq(qmiRegistrationRadioCycleAfterTries+1, nil),
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  qmiRegistrationRadioCycleAfterTries + 1,
		SuppressRadioCycle: func() bool {
			return true
		},
	})
	if err == nil {
		t.Fatal("ensureQMIRegistration() error=nil want timeout due to suppressed radio cycle")
	}
	if len(ctrl.setModeCalls) != 0 {
		t.Fatalf("setModeCalls=%v want no radio cycle", ctrl.setModeCalls)
	}
}

func TestEnsureQMIRegistrationSleepsWhenRadioCycleSuppressed(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq:  qmiSearchingServingSeq(qmiRegistrationRadioCycleAfterTries+2, nil),
	}
	pollInterval := 5 * time.Millisecond

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: pollInterval,
		MaxAttempts:  qmiRegistrationRadioCycleAfterTries + 2,
		SuppressRadioCycle: func() bool {
			return true
		},
	})
	if err == nil {
		t.Fatal("ensureQMIRegistration() error=nil want timeout due to suppressed radio cycle")
	}
	if len(ctrl.servingCallTimes) < qmiRegistrationRadioCycleAfterTries+1 {
		t.Fatalf("serving calls=%d want at least %d", len(ctrl.servingCallTimes), qmiRegistrationRadioCycleAfterTries+1)
	}
	suppressedAttemptIndex := qmiRegistrationRadioCycleAfterTries - 1
	nextAttemptIndex := qmiRegistrationRadioCycleAfterTries
	if elapsed := ctrl.servingCallTimes[nextAttemptIndex].Sub(ctrl.servingCallTimes[suppressedAttemptIndex]); elapsed < pollInterval {
		t.Fatalf("elapsed after suppressed radio-cycle attempt=%s want at least %s; path should not hot-loop", elapsed, pollInterval)
	}
	if len(ctrl.setModeCalls) != 0 {
		t.Fatalf("setModeCalls=%v want no radio cycle", ctrl.setModeCalls)
	}
}

func TestEnsureQMIRegistrationReassertsAutomaticSystemSelectionWithPermanentRegisterWake(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  3,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.systemSelectionAutomaticCalls != 1 {
		t.Fatalf("systemSelectionAutomaticCalls=%d want 1", ctrl.systemSelectionAutomaticCalls)
	}
	if len(ctrl.registerReqs) != 1 {
		t.Fatalf("register requests=%d want 1", len(ctrl.registerReqs))
	}
	req := ctrl.registerReqs[0]
	if !req.HasChangeDuration || req.ChangeDuration != qmi.NASChangeDurationPermanent {
		t.Fatalf("register change duration has=%v value=%d want permanent", req.HasChangeDuration, req.ChangeDuration)
	}
}

func TestEnsureQMIRegistrationUsesManualOperatorSelectionRAT(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		allowManualRegister: true,
		simStatuses:         []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
	}
	cfg := config.DeviceConfig{
		OperatorSelectionMode: "manual",
		OperatorSelectionPLMN: "310260",
		OperatorSelectionRAT:  string(backend.OperatorRATLTE),
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", cfg, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  3,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if len(ctrl.registerReqs) != 1 {
		t.Fatalf("register requests=%d want 1", len(ctrl.registerReqs))
	}
	req := ctrl.registerReqs[0]
	if req.Mode != "manual" || req.MCC != 310 || req.MNC != 260 || !req.IncludesPCSDigit || req.RadioAccessTech != 0x08 {
		t.Fatalf("manual register request=%+v", req)
	}
}

func TestEnsureQMIRegistrationFallsBackWhenAutomaticRegisterMalformed(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
		registerErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASInitiateNetworkRegister,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrMalformedMsg,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  4,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.registerCalls != 1 {
		t.Fatalf("registerCalls=%d want 1", ctrl.registerCalls)
	}
	if ctrl.systemSelectionAutomaticCalls != 1 {
		t.Fatalf("systemSelectionAutomaticCalls=%d want 1", ctrl.systemSelectionAutomaticCalls)
	}
}

func TestEnsureQMIRegistrationFallsBackWhenAutomaticRegisterInvalidAction(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
		registerErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASInitiateNetworkRegister,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrInvalidRegisterAction,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  4,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.registerCalls != 1 {
		t.Fatalf("registerCalls=%d want 1", ctrl.registerCalls)
	}
	if ctrl.systemSelectionAutomaticCalls != 1 {
		t.Fatalf("systemSelectionAutomaticCalls=%d want 1", ctrl.systemSelectionAutomaticCalls)
	}
}

func TestEnsureQMIRegistrationForcesNetworkSearchWhenSearchingPersistsAfterWake(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  4,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.registerCalls != 1 {
		t.Fatalf("registerCalls=%d want 1", ctrl.registerCalls)
	}
	if ctrl.forceNetworkSearchCalls != 1 {
		t.Fatalf("forceNetworkSearchCalls=%d want 1", ctrl.forceNetworkSearchCalls)
	}
}

func TestEnsureQMIRegistrationDoesNotRadioCycleEarlyAfterAcceptedForceSearch(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: qmiSearchingServingSeq(5, &backend.ServingSystem{
			RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true,
		}),
		registerErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASInitiateNetworkRegister,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrInvalidRegisterAction,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  7,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.forceNetworkSearchCalls != 1 {
		t.Fatalf("forceNetworkSearchCalls=%d want 1", ctrl.forceNetworkSearchCalls)
	}
	if len(ctrl.setModeCalls) != 0 {
		t.Fatalf("setModeCalls=%v want no early radio cycle", ctrl.setModeCalls)
	}
}

func TestEnsureQMIRegistrationRadioCyclesAfterDelayedSearchWindow(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: qmiSearchingServingSeq(qmiRegistrationRadioCycleAfterTries, &backend.ServingSystem{
			RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true,
		}),
		registerErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASInitiateNetworkRegister,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrInvalidRegisterAction,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  qmiRegistrationRadioCycleAfterTries + 2,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.forceNetworkSearchCalls != 1 {
		t.Fatalf("forceNetworkSearchCalls=%d want 1", ctrl.forceNetworkSearchCalls)
	}
	if got, want := ctrl.setModeCalls, []backend.OperatingMode{backend.ModeRFOff, backend.ModeOnline}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("setModeCalls=%v want %v", got, want)
	}
}

func TestEnsureQMIRegistrationRadioCyclesSoonerWhenForceNetworkSearchUnsupported(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: []*backend.ServingSystem{
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 2, RegStatusText: "搜索中"},
			{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true},
		},
		forceNetworkSearchErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASForceNetworkSearch,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrOpDeviceUnsupported,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  6,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if ctrl.forceNetworkSearchCalls != 1 {
		t.Fatalf("forceNetworkSearchCalls=%d want 1", ctrl.forceNetworkSearchCalls)
	}
	if got, want := ctrl.setModeCalls, []backend.OperatingMode{backend.ModeRFOff, backend.ModeOnline}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("setModeCalls=%v want %v", got, want)
	}
}

func TestEnsureQMIRegistrationDoesNotRadioCycleEarlyAfterStartupRadioRestore(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		opMode:      backend.ModeRFOff,
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq: qmiSearchingServingSeq(5, &backend.ServingSystem{
			RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true,
		}),
		forceNetworkSearchErr: &qmi.QMIError{
			Service:   0x03,
			MessageID: qmi.NASForceNetworkSearch,
			Result:    0x0001,
			ErrorCode: qmi.QMIErrOpDeviceUnsupported,
		},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  7,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if got, want := ctrl.setModeCalls, []backend.OperatingMode{backend.ModeOnline}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("setModeCalls=%v want startup Online restore only", got)
	}
}

func TestEnsureQMIRegistrationReturnsDenied(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq:  []*backend.ServingSystem{{RegStatus: 3, RegStatusText: "注册被拒"}},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  2,
	})
	if !errors.Is(err, errQMIRegistrationDenied) {
		t.Fatalf("error=%v want errQMIRegistrationDenied", err)
	}
}

func TestEnsureQMIRegistrationRestoresOnlineWhenRadioStartsInFlightMode(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		opMode:      backend.ModeRFOff,
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq:  []*backend.ServingSystem{{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true}},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  3,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
	if got, want := ctrl.setModeCalls, []backend.OperatingMode{backend.ModeOnline}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("setModeCalls=%v want %v", got, want)
	}
}

func TestEnsureQMIRegistrationContinuesForUnknownQMIOperatingMode(t *testing.T) {
	ctrl := &qmiRegistrationTestController{
		opMode:      backend.OperatingMode(5),
		simStatuses: []qmi.SIMStatus{qmi.SIMReady},
		servingSeq:  []*backend.ServingSystem{{RegStatus: 5, RegStatusText: "已注册(漫游)", PSAttached: true}},
	}

	err := ensureQMIRegistration(context.Background(), "dev-qmi", config.DeviceConfig{}, ctrl, ctrl, qmiRegistrationOptions{
		PollInterval: time.Nanosecond,
		MaxAttempts:  2,
	})
	if err != nil {
		t.Fatalf("ensureQMIRegistration() error = %v", err)
	}
}

func TestQMIRegistrationTimeoutShorterWhenNotRequiredForData(t *testing.T) {
	got := qmiRegistrationTimeout(true)
	if got != qmiRegistrationTimeoutDataRequired {
		t.Fatalf("data-required timeout = %v, want %v", got, qmiRegistrationTimeoutDataRequired)
	}

	got = qmiRegistrationTimeout(false)
	if got != qmiRegistrationTimeoutBestEffort {
		t.Fatalf("best-effort timeout = %v, want %v", got, qmiRegistrationTimeoutBestEffort)
	}

	if qmiRegistrationTimeoutBestEffort >= qmiRegistrationTimeoutDataRequired {
		t.Fatalf("best-effort timeout %v should be shorter than data-required timeout %v", qmiRegistrationTimeoutBestEffort, qmiRegistrationTimeoutDataRequired)
	}
}

func TestQMIRegistrationErrorRequiredOnlyWhenNetworkEnabled(t *testing.T) {
	err := qmiRegistrationPreferenceError(errors.New("registration failed"), true)
	if err == nil {
		t.Fatal("network enabled error = nil, want propagated error")
	}

	err = qmiRegistrationPreferenceError(errors.New("registration failed"), false)
	if err != nil {
		t.Fatalf("network disabled error = %v, want nil", err)
	}

	err = qmiRegistrationPreferenceError(errQMIRegistrationSkipped, true)
	if err != nil {
		t.Fatalf("skipped registration error = %v, want nil", err)
	}
}

func TestQMIRegistrationReconcileAsyncAllowsOnlyOneInFlightPerWorker(t *testing.T) {
	w := &Worker{ID: "dev-qmi", stop: make(chan struct{})}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32

	run := func(ctx context.Context) error {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-releaseFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case 2:
			close(secondStarted)
			return nil
		default:
			t.Fatalf("unexpected reconcile call count: %d", calls.Load())
			return nil
		}
	}

	if !w.startQMIRegistrationReconcile(context.Background(), "first", run) {
		t.Fatal("first reconcile start = false, want true")
	}
	select {
	case <-firstStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first reconcile did not start")
	}

	if w.startQMIRegistrationReconcile(context.Background(), "duplicate", run) {
		t.Fatal("duplicate reconcile start = true, want false while first is in-flight")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("reconcile calls = %d, want 1 while duplicate is skipped", got)
	}

	close(releaseFirst)
	deadline := time.After(500 * time.Millisecond)
	for {
		if w.startQMIRegistrationReconcile(context.Background(), "after-finish", run) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("reconcile did not allow retry after first finished")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-secondStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second reconcile did not start")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reconcile calls = %d, want 2 after retry", got)
	}
}

type fakeProvisioningSIM struct {
	statuses []qmi.SIMStatus
	idx      int
	ensured  int
}

func (f *fakeProvisioningSIM) GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error) {
	s := f.statuses[f.idx]
	if f.idx < len(f.statuses)-1 {
		f.idx++
	}
	return s, nil
}

func (f *fakeProvisioningSIM) EnsureSIMProvisioned(ctx context.Context, opts qmimanager.EnsureSIMProvisionedOptions) (qmimanager.UIMReadiness, error) {
	f.ensured++
	return qmimanager.UIMReadiness{UIMReady: true, Reason: qmimanager.UIMReadinessReady}, nil
}

func TestEnsureQMIRegistrationCallsProvisioningBeforeReady(t *testing.T) {
	sim := &fakeProvisioningSIM{statuses: []qmi.SIMStatus{qmi.SIMNotReady, qmi.SIMReady}}
	ctrl := &qmiRegistrationTestController{servingSeq: []*backend.ServingSystem{{RegStatus: 1, PSAttached: true}}}
	err := ensureQMIRegistration(context.Background(), "dev", config.DeviceConfig{}, sim, ctrl,
		qmiRegistrationOptions{PollInterval: time.Millisecond, MaxAttempts: 5})
	if err != nil {
		t.Fatalf("ensureQMIRegistration error: %v", err)
	}
	if sim.ensured != 1 {
		t.Fatalf("expected EnsureSIMProvisioned called once, got %d", sim.ensured)
	}
}
