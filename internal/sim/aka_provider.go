package sim

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	swusim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// ATModem 定义 simauth 所需的 Modem 能力接口。
type ATModem interface {
	DeviceID() string
	ExecuteATSilent(cmd string, timeout time.Duration) (string, error)
	OpenLogicalChannel(aid string) (int, error)
	CloseLogicalChannel(channel int) error
	TransmitAPDU(channel int, hexAPDU string) (string, error)
}

const (
	usimAIDPrefix = "A0000000871002"
	isimAIDPrefix = "A0000000871004"
)

type ATAKAProvider struct {
	m ATModem

	mu              sync.RWMutex
	lastSelectedApp string
	lastPreference  string
	lastFallback    bool
}

type AKAPathProfile struct {
	SelectedApp string
	Preference  string
	Fallback    bool
}

func (d *ATAKAProvider) deviceID() string {
	if d == nil || d.m == nil {
		return ""
	}
	return d.m.DeviceID()
}

func (d *ATAKAProvider) withDevice(kv ...interface{}) []interface{} {
	deviceID := d.deviceID()
	if deviceID == "" {
		return kv
	}
	out := make([]interface{}, 0, len(kv)+2)
	out = append(out, "device", deviceID)
	out = append(out, kv...)
	return out
}

// NewATAKAProvider 创建通用 AT AKA 驱动封装，用于读取 SIM 信息与执行 AKA 计算。
func NewATAKAProvider(m ATModem) *ATAKAProvider {
	return &ATAKAProvider{m: m}
}

func (d *ATAKAProvider) LastAKAProfile() AKAPathProfile {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return AKAPathProfile{
		SelectedApp: d.lastSelectedApp,
		Preference:  d.lastPreference,
		Fallback:    d.lastFallback,
	}
}

// CalculateAKA 执行 AKA 计算，默认使用 USIM。
// 如需 ISIM 优先或自动回退，请显式调用 CalculateAKAWithPreference。
func (d *ATAKAProvider) CalculateAKA(rand16, autn16 []byte) (swusim.AKAResult, error) {
	return d.CalculateAKAWithPreference(rand16, autn16, AKAAppPreferenceUSIM)
}

// CalculateISIMAKA 执行严格 ISIM AKA 计算，不回退到 USIM。
func (d *ATAKAProvider) CalculateISIMAKA(rand16, autn16 []byte) (swusim.AKAResult, error) {
	return d.CalculateAKAWithPreference(rand16, autn16, AKAAppPreferenceISIMStrict)
}

// CalculateAKAWithPreference 按指定优先级执行 AKA:
// - "auto":    先 ISIM，失败后 USIM
// - "isim":    优先 ISIM，失败后 USIM
// - "isim_strict": 仅使用 ISIM，失败不回退
// - "usim":    仅使用 USIM
func (d *ATAKAProvider) CalculateAKAWithPreference(rand16, autn16 []byte, preference string) (swusim.AKAResult, error) {
	logger.Debug("AKA 计算开始",
		d.withDevice(
			"rand", maskHexBytes(rand16),
			"autn", maskHexBytes(autn16),
			"aka_app_preference", preference,
		)...)

	pref := strings.ToLower(strings.TrimSpace(preference))
	if pref == "" {
		pref = AKAAppPreferenceUSIM
	}

	if pref != AKAAppPreferenceAuto && pref != AKAAppPreferenceISIM && pref != AKAAppPreferenceISIMStrict {
		pref = AKAAppPreferenceUSIM
	}

	if pref == AKAAppPreferenceUSIM {
		res, err := d.calculateAKAOnUSIM(rand16, autn16)
		if err != nil {
			logger.Error("AKA 计算失败：USIM 模式失败", d.withDevice("err", err)...)
			return AKAResult{}, err
		}
		d.recordAKAProfile("USIM", pref, false)
		logger.Debug("AKA 路径画像", d.withDevice("selected_app", "USIM", "preference", pref, "fallback", false)...)
		return res, nil
	}

	if res, err := d.calculateAKAOnISIMLogicalChannel(rand16, autn16); err == nil {
		logger.Debug("AKA 使用的 SIM 应用", d.withDevice("app", "ISIM")...)
		d.recordAKAProfile("ISIM", pref, false)
		logger.Debug("AKA 路径画像", d.withDevice("selected_app", "ISIM", "preference", pref, "fallback", false)...)
		logger.Debug("AKA 计算成功（ISIM 逻辑通道）",
			d.withDevice(
				"res_len", len(res.RES), "ck_len", len(res.CK), "ik_len", len(res.IK), "auts_len", len(res.AUTS),
				"res", maskHexBytes(res.RES), "ck", maskHexBytes(res.CK), "ik", maskHexBytes(res.IK), "auts", maskHexBytes(res.AUTS),
			)...)
		return res, nil
	} else {
		if pref == AKAAppPreferenceISIMStrict {
			return AKAResult{}, err
		}
		logger.Warn("ISIM 逻辑通道 AKA 失败，回退 USIM 逻辑通道", d.withDevice("err", err)...)
	}

	res, err := d.calculateAKAOnUSIM(rand16, autn16)
	if err != nil {
		return AKAResult{}, err
	}
	d.recordAKAProfile("USIM", pref, true)
	logger.Debug("AKA 路径画像", d.withDevice("selected_app", "USIM", "preference", pref, "fallback", true)...)
	return res, nil
}

func (d *ATAKAProvider) recordAKAProfile(selectedApp, preference string, fallback bool) {
	d.mu.Lock()
	d.lastSelectedApp = selectedApp
	d.lastPreference = preference
	d.lastFallback = fallback
	d.mu.Unlock()
}

func (d *ATAKAProvider) resolveLogicalChannelAID(app string, fallbackAID string, expectedPrefix string) (string, string, error) {
	fallback := strings.ToUpper(strings.TrimSpace(fallbackAID))

	// 1) 原生解析:各 backend 最佳高层能力(QMI card-status / MBIM APPLICATION_LIST 等)。
	if resolver, ok := d.m.(LogicalChannelAIDResolver); ok {
		aid, source, err := resolver.ResolveLogicalChannelAID(app, fallback)
		if err == nil {
			if full, verr := validateFullAID(aid, expectedPrefix); verr == nil {
				source = strings.TrimSpace(source)
				if source == "" {
					source = "resolver"
				}
				return full, source, nil
			} else {
				logger.Warn("SIMAuth 原生 AID 不可用，尝试 EF_DIR 兜底",
					d.withDevice("app", app, "resolved_aid", aid, "expected_prefix", expectedPrefix, "err", verr)...)
			}
		} else {
			logger.Warn("SIMAuth 原生 AID 解析失败，尝试 EF_DIR 兜底",
				d.withDevice("app", app, "fallback_aid", fallback, "err", err)...)
		}
	}

	return "", "sim_auth_aid_not_ready", fmt.Errorf("sim_auth_aid_not_ready: %s AID 解析失败(原生+EF_DIR 均失败)", app)
}

func validateFullAID(aid, expectedPrefix string) (string, error) {
	aid = strings.ToUpper(strings.TrimSpace(aid))
	if aid == "" {
		return "", fmt.Errorf("empty AID")
	}
	if !strings.HasPrefix(aid, expectedPrefix) {
		return "", fmt.Errorf("prefix mismatch: %s", aid)
	}
	if len(aid) <= len(expectedPrefix) {
		return "", fmt.Errorf("not full AID: %s", aid)
	}
	return aid, nil
}

func (d *ATAKAProvider) openLogicalChannelWithCandidates(label, app, primaryAID, primarySource string) (int, string, string, error) {
	aid := strings.ToUpper(strings.TrimSpace(primaryAID))
	source := strings.TrimSpace(primarySource)
	if source == "" {
		source = "resolver"
	}
	ch, err := d.m.OpenLogicalChannel(aid)
	if err == nil {
		return ch, aid, source, nil
	}
	logger.Warn(label+" 逻辑通道打开失败",
		d.withDevice("app", app, "aid", aid, "aid_source", source, "err", err)...)
	return 0, "", "", err
}

func (d *ATAKAProvider) calculateAKAOnUSIM(rand16, autn16 []byte) (AKAResult, error) {
	usimAID, aidSource, err := d.resolveLogicalChannelAID("usim", usimAIDPrefix, usimAIDPrefix)
	if err != nil {
		return AKAResult{}, err
	}

	ch, openedAID, openedSource, err := d.openLogicalChannelWithCandidates("USIM", "usim", usimAID, aidSource)
	if err != nil {
		return AKAResult{}, fmt.Errorf("打开 USIM 逻辑通道失败: %w", err)
	}
	logger.Debug("USIM 逻辑通道已打开", d.withDevice("channel", ch, "aid", openedAID, "aid_source", openedSource)...)
	defer func() {
		if err := d.m.CloseLogicalChannel(ch); err != nil {
			logger.Warn("关闭 USIM 逻辑通道失败", d.withDevice("channel", ch, "err", err)...)
		}
	}()

	apdu, err := BuildUSIMAuthAPDU(rand16, autn16, false)
	if err != nil {
		return AKAResult{}, err
	}
	res, err := d.sendLogicalAuth(ch, apdu)
	if err == nil {
		logger.Debug("AKA 使用的 SIM 应用", d.withDevice("app", "USIM", "channel", ch)...)
		return res, nil
	}
	logger.Warn("USIM 逻辑通道首选 APDU 失败，尝试带 Le 变体", d.withDevice("err", err)...)

	apdu2, err2 := BuildUSIMAuthAPDU(rand16, autn16, true)
	if err2 != nil {
		return AKAResult{}, err2
	}
	res2, err2 := d.sendLogicalAuth(ch, apdu2)
	if err2 == nil {
		logger.Debug("AKA 使用的 SIM 应用", d.withDevice("app", "USIM", "channel", ch)...)
		return res2, nil
	}
	return AKAResult{}, fmt.Errorf("USIM 逻辑通道 AKA 失败: first=%v second=%v", err, err2)
}

func (d *ATAKAProvider) calculateAKAOnISIMLogicalChannel(rand16, autn16 []byte) (AKAResult, error) {
	isimAID, aidSource, err := d.resolveLogicalChannelAID("isim", isimAIDPrefix, isimAIDPrefix)
	if err != nil {
		return AKAResult{}, err
	}

	ch, err := d.m.OpenLogicalChannel(isimAID)
	if err != nil {
		return AKAResult{}, fmt.Errorf("打开 ISIM 逻辑通道失败: %w", err)
	}
	logger.Debug("ISIM 逻辑通道已打开", d.withDevice("channel", ch, "aid", isimAID, "aid_source", aidSource)...)
	defer func() {
		if err := d.m.CloseLogicalChannel(ch); err != nil {
			logger.Warn("关闭 ISIM 逻辑通道失败", d.withDevice("channel", ch, "err", err)...)
		}
	}()

	apdu, err := BuildUSIMAuthAPDU(rand16, autn16, false)
	if err != nil {
		return AKAResult{}, err
	}
	res, err := d.sendLogicalAuth(ch, apdu)
	if err == nil {
		return res, nil
	}
	logger.Warn("ISIM 逻辑通道首选 APDU 失败，尝试带 Le 变体", d.withDevice("err", err)...)

	apdu2, err2 := BuildUSIMAuthAPDU(rand16, autn16, true)
	if err2 != nil {
		return AKAResult{}, err2
	}
	res2, err2 := d.sendLogicalAuth(ch, apdu2)
	if err2 == nil {
		return res2, nil
	}
	return AKAResult{}, fmt.Errorf("ISIM 逻辑通道 AKA 失败: first=%v second=%v", err, err2)
}

func (d *ATAKAProvider) sendLogicalAuth(channel int, apdu []byte) (AKAResult, error) {
	body, sw1, sw2, err := d.sendAPDUOnLogicalChannel(channel, apdu)
	if err != nil {
		return AKAResult{}, err
	}
	resp := append(append([]byte(nil), body...), sw1, sw2)
	return ParseUSIMAuthResponse(d.deviceID(), resp)
}

func (d *ATAKAProvider) sendAPDUOnLogicalChannel(channel int, apdu []byte) (body []byte, sw1 byte, sw2 byte, err error) {
	send := func(cmd []byte) ([]byte, error) {
		hexAPDU := hex.EncodeToString(cmd)
		start := time.Now()
		respHex, err := d.m.TransmitAPDU(channel, hexAPDU)
		if err != nil {
			logger.Warn("APDU 执行失败",
				d.withDevice("channel", channel, "apdu", maskHexString(hexAPDU), "err", err)...)
			return nil, err
		}
		resp, err := hex.DecodeString(respHex)
		if err != nil {
			return nil, fmt.Errorf("HEX 解码失败: %w", err)
		}
		sw := ""
		if len(resp) >= 2 {
			sw = fmt.Sprintf("%02X%02X", resp[len(resp)-2], resp[len(resp)-1])
		}
		logger.Debug("CGLA 往返",
			d.withDevice(
				"channel", channel,
				"apdu", maskHexString(hexAPDU),
				"resp", maskHexBytes(resp),
				"resp_len", len(resp),
				"sw", sw,
				"cost", time.Since(start).String(),
			)...)
		return resp, nil
	}

	resp, err := send(apdu)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(resp) < 2 {
		return nil, 0, 0, fmt.Errorf("响应过短: %d", len(resp))
	}
	sw1 = resp[len(resp)-2]
	sw2 = resp[len(resp)-1]
	body = resp[:len(resp)-2]

	if sw1 == 0x61 && sw2 != 0x00 {
		getResp := []byte{0x00, 0xC0, 0x00, 0x00, sw2}
		resp2, err := send(getResp)
		if err != nil {
			return body, sw1, sw2, err
		}
		if len(resp2) < 2 {
			return nil, 0, 0, fmt.Errorf("GET RESPONSE 过短: %d", len(resp2))
		}
		sw1 = resp2[len(resp2)-2]
		sw2 = resp2[len(resp2)-1]
		body = resp2[:len(resp2)-2]
	}
	return body, sw1, sw2, nil
}

// AKAWithPreferenceProvider 定义了允许指定卡应用偏好（如 USIM/ISIM）的 AKA 计算扩展接口。
type AKAWithPreferenceProvider interface {
	CalculateAKAWithPreference(rand16, autn16 []byte, preference string) (swusim.AKAResult, error)
}

// preferredAKAAdapter 将 AKAWithPreferenceProvider 与特定偏好绑定，包装为 AKAProvider。
type preferredAKAAdapter struct {
	p          AKAWithPreferenceProvider
	preference string
}

// WrapPreferredAKAProvider 将包含偏好参数的 AKA 提供者包装为统一的 AKAProvider 接口。
func WrapPreferredAKAProvider(p AKAWithPreferenceProvider, preference string) swusim.AKAProvider {
	if p == nil {
		return nil
	}
	return preferredAKAAdapter{p: p, preference: strings.TrimSpace(preference)}
}

// CalculateAKA 代理调用底层的带偏好设置的 AKA 计算方法。
func (a preferredAKAAdapter) CalculateAKA(rand16, autn16 []byte) (swusim.AKAResult, error) {
	return a.p.CalculateAKAWithPreference(rand16, autn16, a.preference)
}
