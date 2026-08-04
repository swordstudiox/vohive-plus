package vowifihost

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type LifecycleCommandKind int

const (
	LifecycleCommandEnable LifecycleCommandKind = iota + 1
	LifecycleCommandDisable
	LifecycleCommandRestart
	LifecycleCommandRecover
	LifecycleCommandSwitchBegin
	LifecycleCommandSwitchEnd
)

func (k LifecycleCommandKind) String() string {
	switch k {
	case LifecycleCommandEnable:
		return "enable"
	case LifecycleCommandDisable:
		return "disable"
	case LifecycleCommandRestart:
		return "restart"
	case LifecycleCommandRecover:
		return "recover"
	case LifecycleCommandSwitchBegin:
		return "switch_begin"
	case LifecycleCommandSwitchEnd:
		return "switch_end"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

type LifecycleCommand struct {
	DeviceID           string
	Kind               LifecycleCommandKind
	Reason             string
	OverrideEPDG       string
	RestoreRadio       bool
	AllowSwitch        bool
	RuntimeInvalidated bool
	Generation         uint64
}

type LifecycleControllerOptions struct {
	IsActive func(deviceID string) bool
	Run      func(context.Context, LifecycleCommand) error
}

type LifecycleController struct {
	mu                sync.Mutex
	devices           map[string]*deviceLifecycle
	isActive          func(deviceID string) bool
	run               func(context.Context, LifecycleCommand) error
	TestRun           func(context.Context, LifecycleCommand) error
	RecoverRunForTest func(context.Context, string, string, string) error
}

type deviceLifecycle struct {
	runMu        sync.Mutex
	generationMu sync.Mutex
	generation   uint64
	runCancel    context.CancelFunc
	runCancelSeq uint64
}

func NewLifecycleController(options ...LifecycleControllerOptions) *LifecycleController {
	c := &LifecycleController{devices: make(map[string]*deviceLifecycle)}
	if len(options) > 0 {
		c.isActive = options[0].IsActive
		c.run = options[0].Run
	}
	return c
}

func (c *LifecycleController) SetRunForTest(fn func(context.Context, LifecycleCommand) error) {
	if c == nil {
		return
	}
	c.TestRun = fn
}

func (c *LifecycleController) SetRecoverRunForTest(fn func(context.Context, string, string, string) error) {
	if c == nil {
		return
	}
	c.RecoverRunForTest = fn
}

func (c *LifecycleController) Submit(ctx context.Context, cmd LifecycleCommand) error {
	if c == nil {
		return fmt.Errorf("vowifi lifecycle controller is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmd.DeviceID = strings.TrimSpace(cmd.DeviceID)
	if cmd.DeviceID == "" {
		return fmt.Errorf("vowifi lifecycle command device_id is empty")
	}
	if cmd.Kind == 0 {
		return fmt.Errorf("vowifi lifecycle command kind is empty")
	}

	lifecycle := c.device(cmd.DeviceID)
	if cmd.Kind == LifecycleCommandSwitchBegin || cmd.Kind == LifecycleCommandRestart {
		return c.submitPreempting(ctx, lifecycle, cmd)
	}

	lifecycle.runMu.Lock()
	defer lifecycle.runMu.Unlock()

	if c.commandGenerationStale(lifecycle, cmd) {
		currentGeneration := c.currentGeneration(lifecycle)
		logger.Debug("忽略过期 VoWiFi lifecycle 命令",
			"device", cmd.DeviceID,
			"kind", cmd.Kind.String(),
			"command_generation", cmd.Generation,
			"current_generation", currentGeneration,
			"reason", strings.TrimSpace(cmd.Reason))
		return nil
	}

	if cmd.Kind == LifecycleCommandEnable && cmd.Generation == 0 && c.isActive != nil && c.isActive(cmd.DeviceID) {
		logger.Debug("忽略重复 VoWiFi enable 命令",
			"device", cmd.DeviceID,
			"reason", strings.TrimSpace(cmd.Reason),
			"current_generation", c.currentGeneration(lifecycle))
		return nil
	}

	if cmd.Generation == 0 {
		switch cmd.Kind {
		case LifecycleCommandEnable,
			LifecycleCommandDisable,
			LifecycleCommandRestart,
			LifecycleCommandRecover:
			cmd.Generation = c.nextGeneration(lifecycle)
		case LifecycleCommandSwitchEnd:
			if cmd.RestoreRadio {
				cmd.Generation = c.nextGeneration(lifecycle)
			}
		}
	}

	runCtx, clearRun := c.bindRunContext(ctx, lifecycle)
	defer clearRun()
	return c.runCommand(runCtx, cmd)
}

func (c *LifecycleController) submitPreempting(ctx context.Context, lifecycle *deviceLifecycle, cmd LifecycleCommand) error {
	if c.commandGenerationStale(lifecycle, cmd) {
		currentGeneration := c.currentGeneration(lifecycle)
		logger.Debug("忽略过期 VoWiFi lifecycle 命令",
			"device", cmd.DeviceID,
			"kind", cmd.Kind.String(),
			"command_generation", cmd.Generation,
			"current_generation", currentGeneration,
			"reason", strings.TrimSpace(cmd.Reason))
		return nil
	}
	if cmd.Generation == 0 {
		cmd.Generation = c.nextGeneration(lifecycle)
	}
	c.cancelActiveRun(lifecycle)
	runCtx, clearRun := c.bindRunContext(ctx, lifecycle)
	defer clearRun()
	return c.runCommand(runCtx, cmd)
}

func (c *LifecycleController) runCommand(ctx context.Context, cmd LifecycleCommand) error {
	if c.TestRun != nil {
		return c.TestRun(ctx, cmd)
	}
	if cmd.Kind == LifecycleCommandRecover && c.RecoverRunForTest != nil {
		return c.RecoverRunForTest(ctx, cmd.DeviceID, cmd.Reason, cmd.OverrideEPDG)
	}
	if c.run == nil {
		return fmt.Errorf("vowifi lifecycle controller run callback is nil")
	}
	return c.run(ctx, cmd)
}

func (c *LifecycleController) NextGeneration(deviceID string) uint64 {
	if c == nil {
		return 0
	}
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" {
		return 0
	}
	lifecycle := c.device(trimmed)
	return c.nextGeneration(lifecycle)
}

func (c *LifecycleController) CurrentGeneration(deviceID string) uint64 {
	if c == nil {
		return 0
	}
	trimmed := strings.TrimSpace(deviceID)
	if trimmed == "" {
		return 0
	}
	lifecycle := c.device(trimmed)
	return c.currentGeneration(lifecycle)
}

func (c *LifecycleController) nextGeneration(lifecycle *deviceLifecycle) uint64 {
	if c == nil || lifecycle == nil {
		return 0
	}
	lifecycle.generationMu.Lock()
	defer lifecycle.generationMu.Unlock()
	lifecycle.generation++
	return lifecycle.generation
}

func (c *LifecycleController) currentGeneration(lifecycle *deviceLifecycle) uint64 {
	if c == nil || lifecycle == nil {
		return 0
	}
	lifecycle.generationMu.Lock()
	defer lifecycle.generationMu.Unlock()
	return lifecycle.generation
}

func (c *LifecycleController) bindRunContext(ctx context.Context, lifecycle *deviceLifecycle) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lifecycle == nil {
		return ctx, func() {}
	}
	runCtx, cancel := context.WithCancel(ctx)
	lifecycle.generationMu.Lock()
	lifecycle.runCancelSeq++
	seq := lifecycle.runCancelSeq
	lifecycle.runCancel = cancel
	lifecycle.generationMu.Unlock()
	return runCtx, func() {
		lifecycle.generationMu.Lock()
		if lifecycle.runCancelSeq == seq {
			lifecycle.runCancel = nil
		}
		lifecycle.generationMu.Unlock()
	}
}

func (c *LifecycleController) cancelActiveRun(lifecycle *deviceLifecycle) {
	if lifecycle == nil {
		return
	}
	lifecycle.generationMu.Lock()
	cancel := lifecycle.runCancel
	lifecycle.generationMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *LifecycleController) commandGenerationStale(lifecycle *deviceLifecycle, cmd LifecycleCommand) bool {
	if c == nil || lifecycle == nil || cmd.Generation == 0 {
		return false
	}
	current := c.currentGeneration(lifecycle)
	return current != 0 && cmd.Generation != current
}

func (c *LifecycleController) device(deviceID string) *deviceLifecycle {
	deviceID = strings.TrimSpace(deviceID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if lifecycle := c.devices[deviceID]; lifecycle != nil {
		return lifecycle
	}
	lifecycle := &deviceLifecycle{}
	c.devices[deviceID] = lifecycle
	return lifecycle
}
