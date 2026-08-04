package mbimcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/netprobe"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

const (
	dataSessionID                    = 0
	defaultDataConnectCommandTimeout = 120 * time.Second
)

var (
	ErrNetworkNotRegistered = errors.New("mbimcore: network not registered")
	defaultPublicIPURLs     = []string{
		"https://api.ipify.org",
		"https://ident.me",
		"https://ifconfig.me/ip",
		"https://httpbin.org/ip",
	}
)

const (
	registerStateHome    uint32 = 3
	registerStateRoaming uint32 = 4
)

const (
	mbimStatusBusy                 uint32 = 0x01
	mbimStatusMaxActivatedContexts uint32 = 0x0d
)

type DataConfig struct {
	APN       string
	Interface string
	IPVersion string
	Username  string
	Password  string
}

func ipTypeFromVersion(v string) uint32 {
	enableV4, enableV6, err := config.ResolveIPFamily(v)
	switch {
	case err != nil, enableV4 && enableV6:
		return mbim.ContextIPTypeIPv4v6
	case enableV6:
		return mbim.ContextIPTypeIPv6
	default:
		return mbim.ContextIPTypeIPv4
	}
}

func (m *Manager) Connect() error {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	return m.connectLocked()
}

// connectLocked performs the CONNECT/IP_CONFIGURATION exchange. Callers must
// hold m.dataMu.
func (m *Manager) connectLocked() error {
	d, err := m.device()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.connectTimeoutOrDefault())
	defer cancel()
	if err := m.ensureRegistered(ctx, d); err != nil {
		return err
	}

	m.mu.Lock()
	cfg := m.dataCfg
	nc := m.netcfg
	m.desiredConnection = true
	m.mu.Unlock()
	if nc == nil {
		nc = realNetConfigurator{}
	}

	st, err := m.activateDataSessionWithRetry(ctx, d, cfg)
	if errors.Is(err, context.DeadlineExceeded) {
		recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), m.connectTimeoutOrDefault())
		defer recoveryCancel()
		if reopenErr := m.reopenControlPlaneForDataConnect(recoveryCtx); reopenErr != nil {
			return fmt.Errorf("mbimcore: CONNECT activate timeout recovery: %w", reopenErr)
		}
		ctx = recoveryCtx
		d, err = m.device()
		if err != nil {
			return err
		}
		if err := m.ensureRegistered(ctx, d); err != nil {
			return err
		}
		st, err = m.activateDataSessionWithRetry(ctx, d, cfg)
	}
	if err != nil {
		recovered, recoverErr := recoverStaleDataSession(ctx, d, err)
		if recoverErr != nil {
			return recoverErr
		}
		if recovered {
			st, err = m.activateDataSessionWithRetry(ctx, d, cfg)
		}
	}
	if err != nil {
		return fmt.Errorf("mbimcore: CONNECT activate: %w", err)
	}
	if st.ActivationState != mbim.ActivationStateActivated {
		return fmt.Errorf("mbimcore: CONNECT not activated (state=%d nwerror=%d)", st.ActivationState, st.NwError)
	}

	ipc, err := mbim.QueryIPConfiguration(ctx, d, dataSessionID)
	if err != nil {
		return fmt.Errorf("mbimcore: IP_CONFIGURATION: %w", err)
	}
	if ipc.IPv4Address == "" && ipc.IPv6Address == "" {
		return fmt.Errorf("mbimcore: no IP assigned")
	}
	if err := m.applyIPConfig(nc, cfg.Interface, ipc); err != nil {
		_, _ = mbim.Connect(ctx, d, dataSessionID, mbim.ActivationCommandDeactivate, "", "", "", mbim.AuthProtocolNone, mbim.ContextIPTypeDefault)
		_ = nc.Flush(cfg.Interface)
		return fmt.Errorf("mbimcore: apply IP config: %w", err)
	}

	m.mu.Lock()
	m.privateIPv4 = ipc.IPv4Address
	m.privateIPv6 = ipc.IPv6Address
	m.connected = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) connectTimeoutOrDefault() time.Duration {
	if m.connectTimeout > 0 {
		return m.connectTimeout
	}
	return defaultDataConnectCommandTimeout
}

func activateDataSession(ctx context.Context, d *mbim.Device, cfg DataConfig) (mbim.ConnectState, error) {
	return mbim.Connect(ctx, d, dataSessionID, mbim.ActivationCommandActivate, cfg.APN, cfg.Username, cfg.Password, mbim.AuthProtocolNone, ipTypeFromVersion(cfg.IPVersion))
}

func (m *Manager) activateDataSessionWithRetry(ctx context.Context, d *mbim.Device, cfg DataConfig) (mbim.ConnectState, error) {
	attempts := m.activateMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	delay := m.activateRetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	var st mbim.ConnectState
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		st, err = activateDataSession(ctx, d, cfg)
		if err == nil || !isConnectStatus(err, mbimStatusBusy) || attempt == attempts {
			return st, err
		}
		if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
			return st, sleepErr
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
	return st, err
}

func recoverStaleDataSession(ctx context.Context, d *mbim.Device, activateErr error) (bool, error) {
	if !isConnectStatus(activateErr, mbimStatusMaxActivatedContexts) {
		return false, nil
	}
	if _, err := mbim.Connect(ctx, d, dataSessionID, mbim.ActivationCommandDeactivate, "", "", "", mbim.AuthProtocolNone, mbim.ContextIPTypeDefault); err != nil {
		return true, fmt.Errorf("mbimcore: CONNECT recover stale session: deactivate after max activated contexts: %w", err)
	}
	return true, nil
}

func isConnectStatus(err error, status uint32) bool {
	var se *mbim.StatusError
	return errors.As(err, &se) && se.Op == "CONNECT" && se.Status == status
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *Manager) reopenControlPlaneForDataConnect(ctx context.Context) error {
	m.mu.Lock()
	dev, mon := m.dev, m.mon
	waiter := m.ussdWaiter
	healthDone := m.healthDone
	m.dev, m.mon, m.ussdWaiter = nil, nil, nil
	m.connected = false
	m.privateIPv4, m.privateIPv6 = "", ""
	m.mu.Unlock()
	m.recoveryGate.Store(false)

	notifyUSSDWaiter(waiter, errUSSDClosed)
	if mon != nil {
		mon.Stop()
	}
	if healthDone != nil {
		m.healthOnce.Do(func() { close(healthDone) })
	}
	if dev != nil {
		_ = dev.Close()
	}

	return m.Open(ctx)
}

func (m *Manager) Disconnect() error {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	return m.disconnectLocked()
}

// disconnectLocked performs the deactivate/flush sequence. Callers must hold
// m.dataMu.
func (m *Manager) disconnectLocked() error {
	m.mu.Lock()
	m.desiredConnection = false
	connected := m.connected
	iface := m.dataCfg.Interface
	nc := m.netcfg
	m.mu.Unlock()
	if !connected {
		return nil
	}

	d, err := m.device()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = mbim.Connect(ctx, d, dataSessionID, mbim.ActivationCommandDeactivate, "", "", "", mbim.AuthProtocolNone, mbim.ContextIPTypeDefault)
	if nc != nil {
		_ = nc.Flush(iface)
	}
	m.mu.Lock()
	m.connected = false
	m.privateIPv4, m.privateIPv6 = "", ""
	m.mu.Unlock()
	return err
}

func (m *Manager) RotateIP() error {
	if !m.IsConnected() {
		return fmt.Errorf("mbimcore: network_not_connected")
	}
	m.dataMu.Lock()
	defer m.dataMu.Unlock()
	if err := m.disconnectLocked(); err != nil {
		return fmt.Errorf("mbimcore: rotate disconnect: %w", err)
	}
	return m.connectLocked()
}

func (m *Manager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *Manager) GetPrivateIP() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.privateIPv4
}

func (m *Manager) GetPrivateIPv6() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.privateIPv6
}

func (m *Manager) GetPublicIPv4AndV6NoCache() (publicV4 string, publicV6 string) {
	m.mu.Lock()
	cfg := m.dataCfg
	urls := append([]string(nil), m.publicIPURLs...)
	privateIPv6 := m.privateIPv6
	m.mu.Unlock()

	enableV4, enableV6, err := config.ResolveIPFamily(cfg.IPVersion)
	if err != nil {
		enableV4, enableV6 = true, false
	}
	if enableV6 && strings.TrimSpace(privateIPv6) == "" {
		enableV6 = false
	}

	prober := netprobe.New(netprobe.Config{Interface: cfg.Interface, URLs: urls, Timeout: 10 * time.Second})
	if enableV4 {
		publicV4 = prober.Probe(context.Background(), netprobe.FamilyV4)
	}
	if enableV6 {
		publicV6 = prober.Probe(context.Background(), netprobe.FamilyV6)
	}
	return publicV4, publicV6
}

func (m *Manager) applyIPConfig(nc netConfigurator, iface string, ipc mbim.IPConfiguration) error {
	if ipc.IPv4Address != "" {
		if err := nc.SetIPv4(iface, ipc.IPv4Address, int(ipc.IPv4PrefixLength)); err != nil {
			return err
		}
	}
	if ipc.IPv6Address != "" {
		if err := nc.SetIPv6(iface, ipc.IPv6Address, int(ipc.IPv6PrefixLength)); err != nil {
			return err
		}
	}
	if ipc.IPv4MTU > 0 {
		if err := nc.SetMTU(iface, int(ipc.IPv4MTU)); err != nil {
			return err
		}
	}
	if err := nc.BringUp(iface); err != nil {
		return err
	}
	if ipc.IPv4Gateway != "" {
		if err := nc.AddDefaultRoute(iface, ipc.IPv4Gateway); err != nil {
			return err
		}
	}
	dns := append(append([]string{}, ipc.IPv4DNS...), ipc.IPv6DNS...)
	if len(dns) > 0 {
		if err := nc.SetDNS(dns); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) handleConnectIndication(st mbim.ConnectState) {
	if st.ActivationState != mbim.ActivationStateDeactivated && st.ActivationState != mbim.ActivationStateDeactivating {
		return
	}
	m.mu.Lock()
	desired := m.desiredConnection
	m.connected = false
	m.mu.Unlock()
	if !desired {
		return
	}
	go m.reconnectWithBackoff()
}

func (m *Manager) reconnectWithBackoff() {
	if !m.reconnectGate.CompareAndSwap(false, true) {
		return
	}
	defer m.reconnectGate.Store(false)
	backoff := time.Second
	for attempt := 0; attempt < 6; attempt++ {
		m.mu.Lock()
		desired := m.desiredConnection
		m.mu.Unlock()
		if !desired {
			return
		}
		if err := m.Connect(); err == nil {
			return
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *Manager) ensureRegistered(ctx context.Context, d *mbim.Device) error {
	m.mu.Lock()
	timeout := m.registrationTimeout
	m.mu.Unlock()
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		rs, err := mbim.QueryRegisterState(ctx, d)
		if err == nil && (rs.RegisterState == registerStateHome || rs.RegisterState == registerStateRoaming) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNetworkNotRegistered
		}
		time.Sleep(500 * time.Millisecond)
	}
}
