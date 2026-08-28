package swu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/engine/swu/ikev2"
)

var ErrInvalidIKETunnelManager = errors.New("invalid swu ike tunnel manager")

type IKEInitRunner func(context.Context, ikev2.InitConfig) (ikev2.InitResult, error)

type IKEAuthRunner func(context.Context, ikev2.FullAuthConfig) (ikev2.FullAuthResult, error)

type IKEPacketSessionFactory func(PacketSessionConfig) (TunnelSession, error)

type IKETransportFactory func(TunnelConfig, IKETransportConfig) (ikev2.InitTransport, error)

type IKEESPTransportFactory func(TunnelConfig, ESPTransportConfig) (ESPPacketTransport, error)

type IKETransportConfig struct {
	EPDGAddress     string
	RemoteAddr      string
	LocalAddr       string
	LocalIP         net.IP
	RemoteIP        net.IP
	LocalPort       uint16
	RemotePort      uint16
	Timeout         time.Duration
	UseNonESPMarker bool
}

type ESPTransportConfig struct {
	EPDGAddress string
	RemoteAddr  string
	LocalAddr   string
	Timeout     time.Duration
}

type IKEPacketTunnelManagerConfig struct {
	Transport                ikev2.InitTransport
	ESPTransport             ESPPacketTransport
	SIM                      sim.AKAProvider
	Random                   io.Reader
	Timeout                  time.Duration
	EPDGCandidateResolver    func(context.Context, string) ([]string, error)
	LocalIP                  net.IP
	RemoteIP                 net.IP
	LocalPort                uint16
	RemotePort               uint16
	UseNonESPMarker          bool
	EAPIdentity              string
	Reauthentication         EAPReauthenticationState
	OnReauthenticationState  func(EAPReauthenticationState)
	InitiatorID              ikev2.Identity
	IKETransportFactory      IKETransportFactory
	ESPTransportFactory      IKEESPTransportFactory
	InitRunner               IKEInitRunner
	AuthRunner               IKEAuthRunner
	PacketSessionFactory     IKEPacketSessionFactory
	SA                       ikev2.SecurityAssociation
	ChildSA                  ikev2.SecurityAssociation
	ChildSPI                 []byte
	TSi                      ikev2.TrafficSelectors
	TSr                      ikev2.TrafficSelectors
	Configuration            ikev2.Configuration
	AdditionalAddresses      []net.IP
	NoAdditionalAddresses    bool
	DisableControlPlaneHooks bool
}

type IKEPacketTunnelManager struct {
	Config IKEPacketTunnelManagerConfig
}

type IKETunnelManagerConfig = IKEPacketTunnelManagerConfig

type IKETunnelManager = IKEPacketTunnelManager

var _ TunnelManager = (*IKEPacketTunnelManager)(nil)

func NewIKEPacketTunnelManager(cfg IKEPacketTunnelManagerConfig) *IKEPacketTunnelManager {
	return &IKEPacketTunnelManager{Config: cfg}
}

func NewIKETunnelManager(cfg IKETunnelManagerConfig) *IKETunnelManager {
	return NewIKEPacketTunnelManager(cfg)
}

func (m *IKEPacketTunnelManager) EstablishTunnel(ctx context.Context, cfg TunnelConfig) (TunnelSession, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidIKETunnelManager)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mode := cfg.NormalizedMode()
	if mode == DataplaneModeDisabled {
		return nil, fmt.Errorf("%w: dataplane mode is disabled", ErrInvalidTunnelConfig)
	}
	if mode != DataplaneModeUserspace {
		return nil, fmt.Errorf("%w: unsupported dataplane mode %q", ErrInvalidTunnelConfig, mode)
	}
	provider := m.Config.SIM
	if provider == nil {
		return nil, fmt.Errorf("%w: SIM AKA provider is nil", ErrInvalidIKETunnelManager)
	}
	epdg := epdgAddressForTunnel(cfg)
	if epdg == "" {
		return nil, fmt.Errorf("%w: ePDG address is empty", ErrInvalidTunnelConfig)
	}
	identity, err := eapIdentityForTunnel(cfg, m.Config.EAPIdentity)
	if err != nil {
		return nil, err
	}
	initiatorID := m.Config.InitiatorID
	if initiatorID.Type == 0 {
		initiatorID = ikev2.Identity{Type: ikev2.IDRFC822Addr, Data: []byte(identity)}
	}
	random := m.Config.Random
	if random == nil {
		random = rand.Reader
	}
	childSPI, err := m.childSPI(random)
	if err != nil {
		return nil, err
	}
	initRunner := m.Config.InitRunner
	if initRunner == nil {
		initRunner = ikev2.RunIKE_SA_INIT
	}
	authRunner := m.Config.AuthRunner
	if authRunner == nil {
		authRunner = ikev2.RunIKE_AUTH_Full
	}
	candidates, err := m.resolveEPDGCandidates(ctx, epdg)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, candidate := range candidates {
		transportCfg, espCfg := m.transportConfigs(cfg, candidate)
		transport, err := m.ikeTransport(cfg, transportCfg)
		if err != nil {
			lastErr = err
			continue
		}
		init, err := initRunner(ctx, ikev2.InitConfig{
			Transport:  transport,
			Random:     random,
			SA:         m.Config.SA,
			LocalIP:    transportCfg.LocalIP,
			LocalPort:  transportCfg.LocalPort,
			RemoteIP:   transportCfg.RemoteIP,
			RemotePort: transportCfg.RemotePort,
		})
		if err != nil {
			lastErr = err
			continue
		}
		reauth := m.Config.Reauthentication.clone()
		if !reauth.Usable() {
			reauth = EAPReauthenticationState{}
		}
		auth, err := authRunner(ctx, ikev2.FullAuthConfig{
			Transport:          transport,
			Init:               init,
			Keys:               init.Keys,
			SIM:                provider,
			EAPKeys:            reauth.Keys,
			InitiatorID:        initiatorID,
			EAPIdentity:        identity,
			EAPReauthIdentity:  reauth.Identity,
			EAPReauthCounter:   reauth.Counter,
			EAPReauthCounterOK: reauth.CounterOK,
			ChildSA:            m.Config.ChildSA,
			ChildSPI:           childSPI,
			TSi:                m.Config.TSi,
			TSr:                m.Config.TSr,
			Configuration:      m.Config.Configuration,
			Random:             random,
		})
		if err != nil {
			lastErr = err
			continue
		}
		if auth.ChildSA == nil {
			lastErr = fmt.Errorf("%w: IKE_AUTH completed without CHILD_SA", ErrTunnelNotReady)
			continue
		}
		child := *auth.ChildSA
		m.updateReauthenticationState(auth)
		espTransport, err := m.espTransport(cfg, espCfg)
		if err != nil {
			lastErr = err
			continue
		}
		result := tunnelResultFromIKE(cfg, epdg, init, child)
		closeHandler, mobikeHandler := m.controlHandlers(transport, init, auth, child, result, transportCfg)
		sessionFactory := m.Config.PacketSessionFactory
		if sessionFactory == nil {
			sessionFactory = func(pc PacketSessionConfig) (TunnelSession, error) {
				return NewPacketSession(pc)
			}
		}
		session, err := sessionFactory(PacketSessionConfig{
			Result:        result,
			ChildSA:       child,
			Transport:     espTransport,
			Random:        random,
			MOBIKEHandler: mobikeHandler,
			CloseHandler:  closeHandler,
		})
		if err != nil {
			lastErr = err
			if closer, ok := espTransport.(ESPPacketTransportCloser); ok {
				_ = closer.Close(ctx)
			}
			continue
		}
		if session == nil {
			lastErr = fmt.Errorf("%w: packet session factory returned nil", ErrInvalidIKETunnelManager)
			if closer, ok := espTransport.(ESPPacketTransportCloser); ok {
				_ = closer.Close(ctx)
			}
			continue
		}
		return session, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no usable ePDG candidate found", ErrInvalidIKETunnelManager)
	}
	return nil, lastErr
}

func (m *IKEPacketTunnelManager) updateReauthenticationState(auth ikev2.FullAuthResult) {
	if m == nil || len(auth.EAPKeys.KAut) == 0 || len(auth.EAPKeys.KEncr) == 0 {
		return
	}
	current := m.Config.Reauthentication.clone()
	next := current
	if strings.TrimSpace(auth.EAPNextReauthID) != "" {
		next.Identity = strings.TrimSpace(auth.EAPNextReauthID)
	}
	if strings.TrimSpace(auth.EAPNextPseudonym) != "" {
		next.NextPseudonym = strings.TrimSpace(auth.EAPNextPseudonym)
	}
	if strings.TrimSpace(next.Identity) == "" {
		return
	}
	next.Keys = cloneEAPAKAKeys(auth.EAPKeys)
	next.Reauthenticated = auth.EAPReauthenticated
	next.CounterTooSmall = auth.EAPReauthCounterTooSmall
	switch {
	case auth.EAPReauthenticated:
		next.Counter = auth.EAPReauthCounter
		next.CounterOK = true
		next.LastAcceptedCounter = auth.EAPReauthCounter
	case auth.EAPReauthCounterTooSmall:
		next.CounterOK = current.CounterOK
		next.LastRejectedCounter = auth.EAPReauthCounter
	default:
		next.Counter = 0
		next.CounterOK = true
		next.LastAcceptedCounter = 0
		next.LastRejectedCounter = 0
	}
	next = next.clone()
	m.Config.Reauthentication = next
	if m.Config.OnReauthenticationState != nil {
		m.Config.OnReauthenticationState(next.clone())
	}
}

func (m *IKEPacketTunnelManager) transportConfigs(cfg TunnelConfig, epdg string) (IKETransportConfig, ESPTransportConfig) {
	remotePort := tunnelAddressPort(epdg)
	if remotePort == 0 {
		remotePort = m.Config.RemotePort
	}
	if remotePort == 0 {
		remotePort = 4500
	}
	localPort := m.Config.LocalPort
	localIP := normalizedMOBIKEIP(m.Config.LocalIP, cfg.OuterLocalIP)
	remoteIP := normalizedMOBIKEIP(m.Config.RemoteIP, tunnelAddressHost(epdg))
	remoteAddr := tunnelUDPAddr(epdg, remotePort)
	localAddr := ""
	if local := firstPacketNonEmpty(cfg.OuterLocalIP); local != "" {
		localAddr = tunnelUDPAddr(local, localPort)
	}
	timeout := m.Config.Timeout
	if timeout == 0 {
		timeout = 8 * time.Second
	}
	useMarker := m.Config.UseNonESPMarker
	if !useMarker {
		useMarker = remotePort == 4500
	}
	ikeCfg := IKETransportConfig{
		EPDGAddress:     epdg,
		RemoteAddr:      remoteAddr,
		LocalAddr:       localAddr,
		LocalIP:         localIP,
		RemoteIP:        remoteIP,
		LocalPort:       localPort,
		RemotePort:      remotePort,
		Timeout:         timeout,
		UseNonESPMarker: useMarker,
	}
	espCfg := ESPTransportConfig{
		EPDGAddress: epdg,
		RemoteAddr:  remoteAddr,
		LocalAddr:   localAddr,
		Timeout:     timeout,
	}
	return ikeCfg, espCfg
}

func (m *IKEPacketTunnelManager) ikeTransport(cfg TunnelConfig, transportCfg IKETransportConfig) (ikev2.InitTransport, error) {
	if m.Config.Transport != nil {
		return m.Config.Transport, nil
	}
	if m.Config.IKETransportFactory != nil {
		return m.Config.IKETransportFactory(cfg, transportCfg)
	}
	return ikev2.UDPTransport{
		RemoteAddr:      transportCfg.RemoteAddr,
		LocalAddr:       transportCfg.LocalAddr,
		Timeout:         transportCfg.Timeout,
		UseNonESPMarker: transportCfg.UseNonESPMarker,
	}, nil
}

func (m *IKEPacketTunnelManager) espTransport(cfg TunnelConfig, transportCfg ESPTransportConfig) (ESPPacketTransport, error) {
	if m.Config.ESPTransport != nil {
		return m.Config.ESPTransport, nil
	}
	if m.Config.ESPTransportFactory != nil {
		return m.Config.ESPTransportFactory(cfg, transportCfg)
	}
	return &UDPESPPacketTransport{
		RemoteAddr: transportCfg.RemoteAddr,
		LocalAddr:  transportCfg.LocalAddr,
		Timeout:    transportCfg.Timeout,
	}, nil
}

func (m *IKEPacketTunnelManager) childSPI(random io.Reader) ([]byte, error) {
	if len(m.Config.ChildSPI) > 0 {
		if len(m.Config.ChildSPI) != 4 {
			return nil, fmt.Errorf("%w: child SPI length %d", ErrInvalidIKETunnelManager, len(m.Config.ChildSPI))
		}
		return append([]byte(nil), m.Config.ChildSPI...), nil
	}
	spi := make([]byte, 4)
	if _, err := io.ReadFull(random, spi); err != nil {
		return nil, err
	}
	if spi[0] == 0 && spi[1] == 0 && spi[2] == 0 && spi[3] == 0 {
		spi[3] = 1
	}
	return spi, nil
}

func (m *IKEPacketTunnelManager) controlHandlers(transport ikev2.InitTransport, init ikev2.InitResult, auth ikev2.FullAuthResult, child ikev2.ChildSAResult, result TunnelResult, transportCfg IKETransportConfig) (func(context.Context) error, func(context.Context, MOBIKERequest) (MOBIKEResult, error)) {
	if m.Config.DisableControlPlaneHooks || auth.NextMessageID == 0 || !ikeKeysUsable(init.Keys) {
		return nil, nil
	}
	control := &ikePacketTunnelControl{
		transport:             transport,
		init:                  init,
		keys:                  init.Keys,
		child:                 child,
		nextMessageID:         auth.NextMessageID,
		result:                result,
		localIP:               transportCfg.LocalIP,
		remoteIP:              transportCfg.RemoteIP,
		localPort:             transportCfg.LocalPort,
		remotePort:            transportCfg.RemotePort,
		additionalAddresses:   cloneIPs(m.Config.AdditionalAddresses),
		noAdditionalAddresses: m.Config.NoAdditionalAddresses,
		random:                m.Config.Random,
	}
	closeHandler := control.close
	var mobikeHandler func(context.Context, MOBIKERequest) (MOBIKEResult, error)
	if init.MOBIKESupported {
		mobikeHandler = control.mobike
	}
	return closeHandler, mobikeHandler
}

type ikePacketTunnelControl struct {
	mu                    sync.Mutex
	transport             ikev2.InitTransport
	init                  ikev2.InitResult
	keys                  ikev2.IKEKeys
	child                 ikev2.ChildSAResult
	nextMessageID         uint32
	result                TunnelResult
	localIP               net.IP
	remoteIP              net.IP
	localPort             uint16
	remotePort            uint16
	additionalAddresses   []net.IP
	noAdditionalAddresses bool
	random                io.Reader
}

func (c *ikePacketTunnelControl) close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	messageID := c.nextMessageID
	c.nextMessageID++
	c.mu.Unlock()
	payloads, err := ikev2.TeardownDeletePayloads(c.child, true)
	if err != nil {
		return err
	}
	_, err = ikev2.RunInformationalExchange(ctx, ikev2.InformationalConfig{
		Transport: c.transport,
		Init:      c.init,
		Keys:      c.keys,
		MessageID: messageID,
		Payloads:  payloads,
		Random:    c.random,
	})
	return err
}

func (c *ikePacketTunnelControl) mobike(ctx context.Context, req MOBIKERequest) (MOBIKEResult, error) {
	if c == nil {
		return MOBIKEResult{}, ErrInvalidIKEControl
	}
	c.mu.Lock()
	messageID := c.nextMessageID
	c.nextMessageID++
	c.mu.Unlock()
	payloads, err := mobikeUpdatePayloads(IKEMOBIKEConfig{
		Init:                  c.init,
		Result:                c.result,
		LocalIP:               c.localIP,
		RemoteIP:              c.remoteIP,
		LocalPort:             c.localPort,
		RemotePort:            c.remotePort,
		AdditionalAddresses:   c.additionalAddresses,
		NoAdditionalAddresses: c.noAdditionalAddresses,
	}, c.additionalAddresses, req)
	if err != nil {
		return MOBIKEResult{}, err
	}
	res, err := ikev2.RunInformationalExchange(ctx, ikev2.InformationalConfig{
		Transport: c.transport,
		Init:      c.init,
		Keys:      c.keys,
		MessageID: messageID,
		Payloads:  payloads,
		Random:    c.random,
	})
	if err != nil {
		return MOBIKEResult{}, err
	}
	if err := rejectMOBIKEResponse(res.ResponseInner); err != nil {
		return MOBIKEResult{}, err
	}
	return MOBIKEResult{
		Rekeyed:          false,
		OuterLocalIP:     firstPacketNonEmpty(req.NewIP, req.OldIP, c.result.EPDGAddress),
		LocalInnerIP:     c.result.LocalInnerIP,
		RemoteInnerIP:    c.result.RemoteInnerIP,
		IKEEstablished:   true,
		IPsecEstablished: true,
		Reason:           "mobike update sa addresses sent",
		UpdatedAt:        time.Now(),
	}, nil
}

func tunnelResultFromIKE(cfg TunnelConfig, epdg string, init ikev2.InitResult, child ikev2.ChildSAResult) TunnelResult {
	return TunnelResult{
		Ready:             true,
		Mode:              DataplaneModeUserspace,
		EPDGAddress:       epdg,
		LocalInnerIP:      firstPacketNonEmpty(cfg.InnerLocalIP, childConfigurationAddress(child, ikev2.ConfigInternalIPv4Address), childConfigurationAddress(child, ikev2.ConfigInternalIPv6Address)),
		RemoteInnerIP:     strings.TrimSpace(cfg.RemoteInnerIP),
		DNSServers:        childConfigurationDNS(child),
		IKEEstablished:    true,
		IPsecEstablished:  true,
		MOBIKESupported:   init.MOBIKESupported,
		ChildSAIdentifier: childSAIdentifier(child),
		Reason:            "ike ipsec tunnel ready",
		EstablishedAt:     time.Now(),
	}
}

func childConfigurationAddress(child ikev2.ChildSAResult, attrType uint16) string {
	values := childConfigurationIPStrings(child, attrType)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func childConfigurationDNS(child ikev2.ChildSAResult) []string {
	return append(childConfigurationIPStrings(child, ikev2.ConfigInternalIPv4DNS), childConfigurationIPStrings(child, ikev2.ConfigInternalIPv6DNS)...)
}

func childConfigurationIPStrings(child ikev2.ChildSAResult, attrType uint16) []string {
	if child.Configuration == nil {
		return nil
	}
	width := 0
	switch attrType {
	case ikev2.ConfigInternalIPv4Address, ikev2.ConfigInternalIPv4DNS:
		width = net.IPv4len
	case ikev2.ConfigInternalIPv6Address, ikev2.ConfigInternalIPv6DNS:
		width = net.IPv6len
	default:
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, attr := range child.Configuration.Attributes {
		if attr.Type != attrType {
			continue
		}
		for value := attr.Value; len(value) >= width; value = value[width:] {
			ip := net.IP(value[:width]).String()
			if ip != "" && !seen[ip] {
				out = append(out, ip)
				seen[ip] = true
			}
		}
	}
	return out
}

func childSAIdentifier(child ikev2.ChildSAResult) string {
	local := hex.EncodeToString(child.LocalSPI)
	remote := hex.EncodeToString(child.RemoteSPI)
	switch {
	case local != "" && remote != "":
		return local + "/" + remote
	case local != "":
		return local
	default:
		return remote
	}
}

func epdgAddressForTunnel(cfg TunnelConfig) string {
	if epdg := strings.TrimSpace(cfg.EPDGAddress); epdg != "" {
		return epdg
	}
	mcc, mnc := tunnelMCCMNC(cfg)
	if mcc == "" || mnc == "" {
		return ""
	}
	return fmt.Sprintf("epdg.epc.mnc%s.mcc%s.pub.3gppnetwork.org", leftPadTunnel(mnc, 3), mcc)
}

func (m *IKEPacketTunnelManager) resolveEPDGCandidates(ctx context.Context, epdg string) ([]string, error) {
	epdg = strings.TrimSpace(epdg)
	if epdg == "" {
		return nil, fmt.Errorf("%w: ePDG address is empty", ErrInvalidTunnelConfig)
	}
	host := tunnelAddressHost(epdg)
	if host == "" {
		return []string{epdg}, nil
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return []string{withTunnelAddressPort(ip.String(), tunnelAddressPort(epdg))}, nil
	}
	if resolver := m.Config.EPDGCandidateResolver; resolver != nil {
		candidates, err := resolver(ctx, host)
		if err != nil {
			return nil, err
		}
		return normalizeEPDGCandidates(candidates, epdg), nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return []string{epdg}, nil
	}
	candidates := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP == nil {
			continue
		}
		candidates = append(candidates, addr.IP.String())
	}
	return normalizeEPDGCandidates(candidates, epdg), nil
}

func normalizeEPDGCandidates(candidates []string, fallback string) []string {
	fallbackPort := tunnelAddressPort(fallback)
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates)+1)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if ip := net.ParseIP(strings.Trim(candidate, "[]")); ip != nil {
			candidate = ip.String()
		}
		candidate = withTunnelAddressPort(candidate, fallbackPort)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" && !seen[fallback] {
		out = append(out, fallback)
	}
	if len(out) == 0 && fallback != "" {
		out = append(out, fallback)
	}
	return out
}

func eapIdentityForTunnel(cfg TunnelConfig, override string) (string, error) {
	raw := firstPacketNonEmpty(override, cfg.Identity.IMPI, cfg.IMSI, cfg.Identity.IMPU)
	if raw == "" {
		return "", fmt.Errorf("%w: EAP identity is empty", ErrInvalidTunnelConfig)
	}
	raw = normalizeTunnelIdentity(raw)
	if strings.Contains(raw, "@") {
		return raw, nil
	}
	mcc, mnc := tunnelMCCMNC(cfg)
	if mcc == "" || mnc == "" {
		return "", fmt.Errorf("%w: MCC/MNC is required for IMSI-derived EAP identity", ErrInvalidTunnelConfig)
	}
	prefix := ""
	if isDecimalString(raw) {
		prefix = "0"
	}
	return fmt.Sprintf("%s%s@nai.epc.mnc%s.mcc%s.3gppnetwork.org", prefix, raw, leftPadTunnel(mnc, 3), mcc), nil
}

func normalizeTunnelIdentity(identity string) string {
	identity = strings.TrimSpace(identity)
	identity = strings.Trim(identity, "<>")
	if strings.HasPrefix(strings.ToLower(identity), "sip:") {
		identity = identity[4:]
	}
	if semi := strings.IndexByte(identity, ';'); semi >= 0 {
		identity = identity[:semi]
	}
	return strings.TrimSpace(identity)
}

func tunnelMCCMNC(cfg TunnelConfig) (string, string) {
	mcc := strings.TrimSpace(cfg.MCC)
	mnc := strings.TrimSpace(cfg.MNC)
	imsi := strings.TrimSpace(cfg.IMSI)
	if mcc == "" && len(imsi) >= 3 {
		mcc = imsi[:3]
	}
	if mnc == "" && len(imsi) >= 6 {
		mnc = imsi[3:6]
	}
	return mcc, mnc
}

func tunnelUDPAddr(addr string, port uint16) string {
	addr = strings.TrimSpace(addr)
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(int(port)))
}

func tunnelAddressPort(addr string) uint16 {
	_, rawPort, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return 0
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return 0
	}
	return uint16(port)
}

func withTunnelAddressPort(addr string, port uint16) string {
	addr = strings.TrimSpace(addr)
	if addr == "" || port == 0 {
		return addr
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), strconv.Itoa(int(port)))
}

func tunnelAddressHost(addr string) string {
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(addr, "[]")
}

func leftPadTunnel(value string, width int) string {
	value = strings.TrimSpace(value)
	for len(value) < width {
		value = "0" + value
	}
	return value
}

func isDecimalString(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func ikeKeysUsable(keys ikev2.IKEKeys) bool {
	p := keys.Profile
	return p.RequiredLength() > 0 &&
		len(keys.SKD) >= p.PRFKeyLength &&
		len(keys.SKAi) >= p.IntegrityKeyLength &&
		len(keys.SKAr) >= p.IntegrityKeyLength &&
		len(keys.SKEi) >= p.EncryptionKeyLength &&
		len(keys.SKEr) >= p.EncryptionKeyLength &&
		len(keys.SKPi) >= p.PRFKeyLength &&
		len(keys.SKPr) >= p.PRFKeyLength
}
