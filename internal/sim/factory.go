package sim

import (
	"context"
	"errors"
	"strings"

	swusim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

// BackendAKAProvider is the backend surface needed to compute AKA without APDU.
type BackendAKAProvider interface {
	CalculateAKA(ctx context.Context, rand16, autn16 []byte) (res, ik, ck, auts []byte, err error)
}

type mbimAKAProvider = BackendAKAProvider

// AKAProviderWorker is the minimal worker/backend surface needed to build a runtime AKA provider.
type AKAProviderWorker interface {
	BackendMode() string
	MBIMAKAProvider() (BackendAKAProvider, bool)
	MBIMCapability() (*mbim.Capabilities, bool)
	RuntimeModem() (ATModem, error)
}

type backendAKAProvider struct {
	backend BackendAKAProvider
}

func (p backendAKAProvider) CalculateAKA(rand16, autn16 []byte) (swusim.AKAResult, error) {
	res, ik, ck, auts, err := p.backend.CalculateAKA(context.Background(), rand16, autn16)
	if err != nil {
		// MBIM AUTH_SYNC_FAILURE: err is set but auts may carry the resync token.
		// Return it as ErrSyncFailure so the EAP-AKA engine can send AT_AUTS.
		var se *mbim.StatusError
		if errors.As(err, &se) && se.Status == 35 { // 35 is MBIM_STATUS_AUTH_SYNC_FAILURE
			return swusim.AKAResult{AUTS: append([]byte(nil), auts...)}, swusim.ErrSyncFailure
		}
		if len(auts) > 0 {
			return swusim.AKAResult{AUTS: append([]byte(nil), auts...)}, swusim.ErrSyncFailure
		}
		return swusim.AKAResult{}, err
	}
	return swusim.AKAResult{
		RES:  append([]byte(nil), res...),
		CK:   append([]byte(nil), ck...),
		IK:   append([]byte(nil), ik...),
		AUTS: append([]byte(nil), auts...),
	}, nil
}

// channelOrAuthAKAProvider tries logical-channel APDU AKA first; when the UICC
// channel cannot be opened (e.g. EM7430 USIM 12-byte AID → SelectFailed), it
// automatically falls back to the MBIM Auth service.
type channelOrAuthAKAProvider struct {
	channel swusim.AKAProvider
	auth    swusim.AKAProvider
}

func (p *channelOrAuthAKAProvider) CalculateAKA(rand16, autn16 []byte) (swusim.AKAResult, error) {
	res, err := p.channel.CalculateAKA(rand16, autn16)
	if err == nil {
		return res, nil
	}
	if isUICCChannelOpenError(err) {
		logger.Warn("[sim] 逻辑通道开通道失败，降级到 MBIM Auth AKA", "err", err)
		return p.auth.CalculateAKA(rand16, autn16)
	}
	return swusim.AKAResult{}, err
}

// isUICCChannelOpenError reports whether err indicates the UICC channel could
// not be opened (as opposed to the channel being open but an APDU failing).
// These MBIM status codes all mean the modem rejected the OPEN_CHANNEL command.
func isUICCChannelOpenError(err error) bool {
	var se *mbim.StatusError
	if !errors.As(err, &se) {
		return false
	}
	switch se.Status {
	case mbim.StatusMSSelectFailed,
		mbim.StatusMSNoLogicalChannels,
		mbim.StatusMSInvalidLogicalChannel:
		return true
	}
	return false
}

// BuildAKAProvider returns the unified runtime AKA provider for the worker/backend.
func BuildAKAProvider(w AKAProviderWorker) swusim.AKAProvider {
	if w == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(w.BackendMode()), backend.BackendMBIM) {
		caps, _ := w.MBIMCapability()

		var mbimAuth swusim.AKAProvider
		if caps != nil && caps.AuthAKAUsable() {
			if provider, ok := w.MBIMAKAProvider(); ok && provider != nil {
				mbimAuth = backendAKAProvider{backend: provider}
			}
		}

		// 无论 MBIM 规范层面上宣告支不支持逻辑通道，只要有 RuntimeModem（比如 QMI over MBIM 适配器），
		// 我们都优先尝试 APDU AKA。如果 APDU AKA 在执行时开通道失败，会自动降级给 MBIM Auth。
		modem, err := w.RuntimeModem()
		if err == nil && modem != nil {
			channelProvider := NewATAKAProvider(modem)
			if mbimAuth != nil {
				return &channelOrAuthAKAProvider{
					channel: channelProvider,
					auth:    mbimAuth,
				}
			}
			return channelProvider
		}

		// 退路: 没有任何逻辑通道/QMI适配器可用时, 直接用模组的 MBIM AUTH 服务。
		return mbimAuth
	}
	modem, err := w.RuntimeModem()
	if err != nil || modem == nil {
		return nil
	}
	return NewATAKAProvider(modem)
}
