package qmicore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/netprobe"
	"github.com/swordstudiox/vohive-plus/pkg/logger"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/netcfg"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/miekg/dns"
)

// 精选极速探测源
var ipCheckURLs = []string{
	"https://api.ipify.org",
	"https://ident.me",
	"https://ifconfig.me/ip",
	"https://httpbin.org/ip",
}

const (
	publicIPDialTimeout    = 5 * time.Second
	publicIPResolveTimeout = 4 * time.Second
	publicIPRequestTimeout = 10 * time.Second

	existingDataConnectionResetTimeout = 8 * time.Second
)

var fallbackPublicIPDNSServers = []string{
	"1.1.1.1:53",
	"8.8.8.8:53",
	"9.9.9.9:53",
}

type qmiEventLogLevel int

const (
	qmiEventLogDebug qmiEventLogLevel = iota
	qmiEventLogInfo
	qmiEventLogWarn
)

type qmiEventSummary struct {
	Level   qmiEventLogLevel
	Message string
	Fields  []any
}

type qmiManagerLoggerAdapter struct {
	fields []any
}

func newQMIManagerLoggerAdapter(deviceID string) qmimanager.Logger {
	return &qmiManagerLoggerAdapter{
		fields: []any{
			"device", deviceID,
			"component", "qmi_lib",
		},
	}
}

func (l *qmiManagerLoggerAdapter) clone() *qmiManagerLoggerAdapter {
	if l == nil {
		return &qmiManagerLoggerAdapter{}
	}
	next := &qmiManagerLoggerAdapter{
		fields: make([]any, len(l.fields)),
	}
	copy(next.fields, l.fields)
	return next
}

func (l *qmiManagerLoggerAdapter) message(args ...interface{}) string {
	return strings.TrimSpace(fmt.Sprintln(args...))
}

func (l *qmiManagerLoggerAdapter) logDebug(msg string) {
	logger.Debug(msg, l.fields...)
}

func (l *qmiManagerLoggerAdapter) logWarn(msg string) {
	logger.Warn(msg, l.fields...)
}

func (l *qmiManagerLoggerAdapter) logError(msg string) {
	logger.Error(msg, l.fields...)
}

func (l *qmiManagerLoggerAdapter) Debug(args ...interface{}) {
	l.logDebug(l.message(args...))
}

func (l *qmiManagerLoggerAdapter) Debugf(format string, args ...interface{}) {
	l.logDebug(fmt.Sprintf(format, args...))
}

func (l *qmiManagerLoggerAdapter) Info(args ...interface{}) {
	l.logDebug(l.message(args...))
}

func (l *qmiManagerLoggerAdapter) Infof(format string, args ...interface{}) {
	l.logDebug(fmt.Sprintf(format, args...))
}

func (l *qmiManagerLoggerAdapter) Warn(args ...interface{}) {
	msg := l.message(args...)
	l.logWarn(msg)
}

func (l *qmiManagerLoggerAdapter) Warnf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.logWarn(msg)
}

func (l *qmiManagerLoggerAdapter) Error(args ...interface{}) {
	msg := l.message(args...)
	l.logError(msg)
}

func (l *qmiManagerLoggerAdapter) Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.logError(msg)
}

func (l *qmiManagerLoggerAdapter) WithField(key string, value interface{}) qmimanager.Logger {
	next := l.clone()
	next.fields = append(next.fields, key, value)
	return next
}

func (l *qmiManagerLoggerAdapter) WithError(err error) qmimanager.Logger {
	next := l.clone()
	next.fields = append(next.fields, "err", err)
	return next
}

func logQMIEvent(deviceID string, event qmimanager.Event) {
	summary := summarizeQMIEvent(event)
	if summary.Message == "" {
		return
	}

	fields := make([]any, 0, 2+len(summary.Fields))
	fields = append(fields, "device", deviceID)
	fields = append(fields, summary.Fields...)

	switch summary.Level {
	case qmiEventLogWarn:
		logger.Warn(summary.Message, fields...)
	case qmiEventLogInfo:
		logger.Info(summary.Message, fields...)
	default:
		logger.Debug(summary.Message, fields...)
	}
}

func logQMIStatsSnapshot(deviceID string, mgr *qmimanager.Manager) {
	if mgr == nil {
		return
	}
	managerStats := mgr.Stats()
	clientStats := mgr.ClientStats()
	logger.Debug("QMI 运行时统计",
		"device", deviceID,
		"status_checks", managerStats.StatusChecks,
		"debounced_checks", managerStats.DebouncedChecks,
		"reconnect_scheduled", managerStats.ReconnectScheduled,
		"stale_timer_ignored", managerStats.StaleTimerIgnored,
		"reset_events", managerStats.ResetEvents,
		"reset_coalesced", managerStats.ResetCoalesced,
		"recover_attempts", managerStats.RecoverAttempts,
		"recover_success", managerStats.RecoverSuccess,
		"recover_backoff_ms", managerStats.RecoverBackoffMs,
		"unmatched_responses", clientStats.UnmatchedResponses,
		"parse_errors", clientStats.ParseErrors,
		"coalesced_indications", clientStats.CoalescedIndications,
		"dropped_edge_indications", clientStats.DroppedEdgeIndications,
	)
}

func summarizeQMIEvent(event qmimanager.Event) qmiEventSummary {
	switch event.Type {
	case qmimanager.EventConnected:
		fields := make([]any, 0, 2)
		if ip := qmiSettingsIP(event.Settings); ip != "" {
			fields = append(fields, "ip", ip)
		}
		return qmiEventSummary{
			Level:   qmiEventLogInfo,
			Message: "QMI 连接成功",
			Fields:  fields,
		}
	case qmimanager.EventDisconnected:
		return qmiEventSummary{
			Level:   qmiEventLogWarn,
			Message: "QMI 连接断开",
		}
	case qmimanager.EventIPChanged:
		fields := make([]any, 0, 2)
		if ip := qmiSettingsIP(event.Settings); ip != "" {
			fields = append(fields, "ip", ip)
		}
		return qmiEventSummary{
			Level:   qmiEventLogInfo,
			Message: "QMI IP 发生变化",
			Fields:  fields,
		}
	case qmimanager.EventSignalUpdate:
		fields := make([]any, 0, 6)
		if event.Signal != nil {
			fields = append(fields, "rssi", event.Signal.RSSI, "rsrp", event.Signal.RSRP, "rsrq", event.Signal.RSRQ)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 信号更新",
			Fields:  fields,
		}
	case qmimanager.EventDialFailed:
		fields := make([]any, 0, 2)
		if event.Error != nil {
			fields = append(fields, "err", event.Error)
		}
		return qmiEventSummary{
			Level:   qmiEventLogWarn,
			Message: "QMI 拨号失败",
			Fields:  fields,
		}
	case qmimanager.EventReconnecting:
		fields := make([]any, 0, 2)
		if event.Error != nil {
			fields = append(fields, "err", event.Error)
		}
		return qmiEventSummary{
			Level:   qmiEventLogInfo,
			Message: "QMI 准备重连",
			Fields:  fields,
		}
	case qmimanager.EventNewSMS:
		fields := []any{
			"index", qmiSMSIndexValue(event.SMSIndex),
			"storage", event.StorageType,
		}
		return qmiEventSummary{
			Level:   qmiEventLogInfo,
			Message: "QMI 收到新短信指示",
			Fields:  fields,
		}
	case qmimanager.EventNewSMSRaw:
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 收到原始短信指示",
			Fields: []any{
				"pdu_len", len(event.Pdu),
			},
		}
	case qmimanager.EventIMSRegistrationStatus:
		fields := make([]any, 0, 8)
		if info := event.IMSRegistration; info != nil {
			if info.HasStatus {
				fields = append(fields, "status", info.Status)
			}
			if info.HasErrorCode {
				fields = append(fields, "error_code", info.ErrorCode)
			}
			if info.HasTechnology {
				fields = append(fields, "technology", info.Technology)
			}
			if info.HasErrorMessage {
				fields = append(fields, "error_message_len", utf8.RuneCountInString(info.ErrorMessage))
			}
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI IMS 注册状态变化",
			Fields:  fields,
		}
	case qmimanager.EventIMSServicesStatus:
		fields := make([]any, 0, 20)
		if info := event.IMSServices; info != nil {
			if info.HasSMSServiceStatus {
				fields = append(fields, "sms_status", info.SMSServiceStatus)
			}
			if info.HasVoiceServiceStatus {
				fields = append(fields, "voice_status", info.VoiceServiceStatus)
			}
			if info.HasVideoTelephonyServiceStatus {
				fields = append(fields, "video_status", info.VideoTelephonyServiceStatus)
			}
			if info.HasSMSTechnology {
				fields = append(fields, "sms_tech", info.SMSTechnology)
			}
			if info.HasVoiceTechnology {
				fields = append(fields, "voice_tech", info.VoiceTechnology)
			}
			if info.HasVideoTelephonyTechnology {
				fields = append(fields, "video_tech", info.VideoTelephonyTechnology)
			}
			if info.HasUETASServiceStatus {
				fields = append(fields, "uetas_status", info.UETASServiceStatus)
			}
			if info.HasUETASTechnology {
				fields = append(fields, "uetas_tech", info.UETASTechnology)
			}
			if info.HasVideoShareServiceStatus {
				fields = append(fields, "video_share_status", info.VideoShareServiceStatus)
			}
			if info.HasVideoShareTechnology {
				fields = append(fields, "video_share_tech", info.VideoShareTechnology)
			}
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI IMS 业务状态变化",
			Fields:  fields,
		}
	case qmimanager.EventIMSSettingsChanged:
		fields := make([]any, 0, 26)
		if info := event.IMSSettings; info != nil {
			if info.HasVoiceOverLTEEnabled {
				fields = append(fields, "volte", info.VoiceOverLTEEnabled)
			}
			if info.HasVideoTelephonyEnabled {
				fields = append(fields, "vt", info.VideoTelephonyEnabled)
			}
			if info.HasVoiceWiFiEnabled {
				fields = append(fields, "vowifi", info.VoiceWiFiEnabled)
			}
			if info.HasCallModePreference {
				fields = append(fields, "call_mode", info.CallModePreference)
			}
			if info.HasIMSServiceEnabled {
				fields = append(fields, "ims_enabled", info.IMSServiceEnabled)
			}
			if info.HasUTServiceEnabled {
				fields = append(fields, "ut_enabled", info.UTServiceEnabled)
			}
			if info.HasSMSServiceEnabled {
				fields = append(fields, "sms_enabled", info.SMSServiceEnabled)
			}
			if info.HasUSSDServiceEnabled {
				fields = append(fields, "ussd_enabled", info.USSDServiceEnabled)
			}
			if info.HasPresenceEnabled {
				fields = append(fields, "presence_enabled", info.PresenceEnabled)
			}
			if info.HasAutoconfigEnabled {
				fields = append(fields, "autoconfig_enabled", info.AutoconfigEnabled)
			}
			if info.HasXDMClientEnabled {
				fields = append(fields, "xdm_enabled", info.XDMClientEnabled)
			}
			if info.HasRCSEnabled {
				fields = append(fields, "rcs_enabled", info.RCSEnabled)
			}
			if info.HasCarrierConfigEnabled {
				fields = append(fields, "carrier_cfg_enabled", info.CarrierConfigEnabled)
			}
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI IMS 配置变化",
			Fields:  fields,
		}
	case qmimanager.EventVoiceCallStatus:
		fields := make([]any, 0, 4)
		if info := event.VoiceCalls; info != nil {
			fields = append(fields,
				"call_count", len(info.Calls),
				"remote_party_count", len(info.RemotePartyNumbers),
			)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 语音通话状态变化",
			Fields:  fields,
		}
	case qmimanager.EventVoiceUSSD:
		fields := make([]any, 0, 4)
		if info := event.VoiceUSSD; info != nil {
			if info.HasUserAction {
				fields = append(fields, "user_action", info.UserAction)
			}
			fields = append(fields, "ussd_len", qmiUSSDPayloadLen(info.USSData))
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI USSD 指示",
			Fields:  fields,
		}
	case qmimanager.EventVoiceUSSDReleased:
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI USSD 已释放",
		}
	case qmimanager.EventVoiceSupplementaryService:
		fields := make([]any, 0, 4)
		if info := event.VoiceSupplementary; info != nil {
			fields = append(fields, "call_id", info.CallID, "notification_type", info.NotificationType)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 补充业务指示",
			Fields:  fields,
		}
	case qmimanager.EventVoiceSupplementaryServiceRequest:
		fields := make([]any, 0, 12)
		if info := event.VoiceSupplementaryRequest; info != nil {
			if info.HasInfo {
				fields = append(fields,
					"request", info.Request,
					"modified_by_call_control", info.ModifiedByCallControl,
				)
			}
			if info.HasServiceClass {
				fields = append(fields, "service_class", info.ServiceClass)
			}
			if info.HasReason {
				fields = append(fields, "reason", info.Reason)
			}
			if info.HasCallID {
				fields = append(fields, "call_id", info.CallID)
			}
			if info.HasFailureCause {
				fields = append(fields, "failure_cause", info.FailureCause)
			}
			if info.HasDataSource {
				fields = append(fields, "data_source", info.DataSource)
			}
			if info.HasExtendedServiceClass {
				fields = append(fields, "extended_service_class", info.ExtendedServiceClass)
			}
			fields = append(fields,
				"ussd_len", qmiUSSDPayloadLen(info.USSData),
				"alpha_len", qmiUSSDPayloadLen(info.Alpha),
				"encoded_utf16_len", len(info.EncodedDataUTF16),
			)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 补充业务请求指示",
			Fields:  fields,
		}
	case qmimanager.EventVoiceUSSDNoWaitResult:
		fields := make([]any, 0, 8)
		if info := event.VoiceUSSDNoWait; info != nil {
			if info.HasErrorCode {
				fields = append(fields, "error_code", info.ErrorCode)
			}
			if info.HasFailureCause {
				fields = append(fields, "failure_cause", info.FailureCause)
			}
			fields = append(fields,
				"ussd_len", qmiUSSDPayloadLen(info.USSData),
				"alpha_len", qmiUSSDPayloadLen(info.Alpha),
			)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 异步 USSD 结果",
			Fields:  fields,
		}
	case qmimanager.EventPacketServiceStatusChanged:
		fields := make([]any, 0, 8)
		fields = append(fields, "status", event.PacketServiceStatus.String())
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 数据服务状态指示",
			Fields:  fields,
		}
	case qmimanager.EventServingSystemChanged:
		fields := make([]any, 0, 16)
		if info := event.ServingSystem; info != nil {
			fields = append(fields,
				"registration_state", info.RegistrationState.String(),
				"ps_attached", info.PSAttached,
				"radio_interface", qmiRadioInterfaceName(info.RadioInterface),
			)
			if info.MCC != 0 {
				fields = append(fields, "mcc", info.MCC)
			}
			if info.MNC != 0 {
				fields = append(fields, "mnc", info.MNC)
			}
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 服务系统指示",
			Fields:  fields,
		}
	case qmimanager.EventNASEventReport:
		if len(event.TLVMeta) == 0 {
			return qmiEventSummary{}
		}
		fields := []any{
			"tlv_count", len(event.TLVMeta),
		}
		if tlvs := qmiTLVSummary(event.TLVMeta); tlvs != "" {
			fields = append(fields, "tlvs", tlvs)
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI NAS 事件报告",
			Fields:  fields,
		}
	case qmimanager.EventWMSSMSCAddress:
		fields := make([]any, 0, 8)
		if info := event.WMSSMSCAddress; info != nil {
			fields = append(fields,
				"type", info.Type,
				"digits_len", utf8.RuneCountInString(info.Digits),
			)
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI WMS SMSC 地址指示",
			Fields:  fields,
		}
	case qmimanager.EventWMSTransportNetworkRegistrationStatus:
		fields := []any{
			"registration_status", event.WMSTransportRegistration.String(),
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI WMS 传输网络注册状态",
			Fields:  fields,
		}
	case qmimanager.EventModemReset:
		return qmiEventSummary{
			Level:   qmiEventLogWarn,
			Message: "QMI 检测到模组重置",
			Fields:  qmiRawEventFields(event),
		}
	case qmimanager.EventSimStatusChanged:
		return qmiEventSummary{
			Level:   qmiEventLogInfo,
			Message: "QMI SIM 状态变化指示",
			Fields:  qmiRawEventFields(event),
		}
	case qmimanager.EventUIMSessionClosed:
		return qmiEventSummary{}
	case qmimanager.EventUIMRefresh:
		fields := make([]any, 0, 14)
		if info := event.UIMRefresh; info != nil {
			fields = append(fields,
				"stage", info.Stage,
				"mode", info.Mode,
				"session_type", info.SessionType,
				"aid_len", len(info.ApplicationIdentifier),
				"file_count", len(info.Files),
			)
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI UIM refresh 指示",
			Fields:  fields,
		}
	case qmimanager.EventUIMSlotStatus:
		fields := make([]any, 0, 10)
		if info := event.UIMSlotStatus; info != nil {
			fields = append(fields, "slot_count", len(info.Slots))
		}
		fields = append(fields, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI UIM 卡槽状态指示",
			Fields:  fields,
		}
	case qmimanager.EventUnknownIndication:
		fields := []any{
			"raw_type", int(event.RawQMIType),
			"service_id", event.ServiceID,
			"service_name", qmiServiceName(event.ServiceID),
			"message_id", event.MessageID,
			"tlv_count", len(event.TLVMeta),
		}
		if tlvs := qmiTLVSummary(event.TLVMeta); tlvs != "" {
			fields = append(fields, "tlvs", tlvs)
		}
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 收到未知指示",
			Fields:  fields,
		}
	default:
		fields := append([]any{"event", event.Type.String()}, qmiRawEventFields(event)...)
		return qmiEventSummary{
			Level:   qmiEventLogDebug,
			Message: "QMI 事件",
			Fields:  fields,
		}
	}
}

func qmiSettingsIP(settings *qmi.RuntimeSettings) string {
	if settings == nil || settings.IPv4Address == nil {
		return ""
	}
	return settings.IPv4Address.String()
}

func qmiSettingsIPv6(settings *qmi.RuntimeSettings) string {
	if settings == nil || settings.IPv6Address == nil {
		return ""
	}
	return settings.IPv6Address.String()
}

func qmiSMSIndexValue(index uint32) any {
	if index == ^uint32(0) {
		return "unknown"
	}
	return index
}

func qmiUSSDPayloadLen(payload *qmi.VoiceUSSDPayload) int {
	if payload == nil {
		return 0
	}
	if payload.Text != "" {
		return utf8.RuneCountInString(payload.Text)
	}
	return len(payload.Data)
}

func qmiServiceName(serviceID uint8) string {
	switch serviceID {
	case qmi.ServiceControl:
		return "CTL"
	case qmi.ServiceWDS:
		return "WDS"
	case qmi.ServiceDMS:
		return "DMS"
	case qmi.ServiceNAS:
		return "NAS"
	case qmi.ServiceQOS:
		return "QOS"
	case qmi.ServiceWMS:
		return "WMS"
	case qmi.ServicePDS:
		return "PDS"
	case qmi.ServiceAUTH:
		return "AUTH"
	case qmi.ServiceVOICE:
		return "VOICE"
	case qmi.ServiceCAT2:
		return "CAT2"
	case qmi.ServiceUIM:
		return "UIM"
	case qmi.ServicePBM:
		return "PBM"
	case qmi.ServiceIMS:
		return "IMS"
	case qmi.ServiceWDA:
		return "WDA"
	case qmi.ServiceWDSIPv6:
		return "WDS_IPV6"
	case qmi.ServiceIMSP:
		return "IMSP"
	case qmi.ServiceIMSA:
		return "IMSA"
	case qmi.ServiceCOEX:
		return "COEX"
	default:
		return "UNKNOWN"
	}
}

func qmiRadioInterfaceName(iface uint8) string {
	switch iface {
	case 0:
		return "none"
	case 1:
		return "cdma"
	case 2:
		return "umts"
	case 4:
		return "lte"
	case 5:
		return "lte-m"
	case 6:
		return "nr5g"
	case 8:
		return "lte"
	case 10:
		return "nr5g"
	default:
		return fmt.Sprintf("unknown(%d)", iface)
	}
}

func qmiTLVSummary(meta []qmi.TLVMeta) string {
	if len(meta) == 0 {
		return ""
	}
	parts := make([]string, 0, len(meta))
	for _, tlv := range meta {
		parts = append(parts, fmt.Sprintf("0x%02x:%d", tlv.Type, tlv.Length))
	}
	return strings.Join(parts, ",")
}

func qmiRawEventFields(event qmimanager.Event) []any {
	return []any{
		"raw_type", int(event.RawQMIType),
		"service_id", event.ServiceID,
		"message_id", event.MessageID,
	}
}

type Manager struct {
	cfg                 config.DeviceConfig
	qmiMgr              *qmimanager.Manager // QMI 管理器
	mu                  sync.Mutex
	euiccMu             sync.Mutex           // 仅保护 Open/Close 通道操作（低频）
	chanMuMu            sync.RWMutex         // 保护 chanMu map 并发访问
	chanMu              map[byte]*sync.Mutex // per-channel 锁，Transmit 热路径使用
	apduLeaseMu         sync.Mutex
	apduArbiter         *apduarbiter.Arbiter
	apduSessions        map[byte]apduSessionInfo
	onConnect           func()
	smsHandlersMu       sync.Mutex
	onNewSMSStored      []func(storage uint8, index uint32)
	onNewSMSRaw         []func(RawSMSIndication)
	uimHandlersMu       sync.Mutex
	onUIMRefresh        []func(info *qmi.UIMRefreshIndication)
	onUIMSlotStatus     []func(info *qmi.UIMSlotStatus)
	onModemReset        []func()
	healthHandlersMu    sync.Mutex
	onHealthEvent       []func(HealthEvent)
	recoveryExhaustedMu sync.Mutex
	onRecoveryExhausted []func(reason string, err error)
	publicIPLookup      func(ctx context.Context, host string) ([]string, error)
	hasIPv6Bearer       func() bool // 测试替身：是否存在已建立的 IPv6 数据承载，默认见 ipv6BearerUp

	resetExistingDataConnection            func(context.Context) (bool, error)
	resetExistingDataConnectionViaCoreHook func(context.Context) (bool, error)
}

type apduSessionInfo struct {
	Channel  byte
	Owner    string
	Class    apduarbiter.APDUClass
	OpenedAt time.Time
}

type RawSMSIndication struct {
	PDU           []byte
	AckRequired   bool
	TransactionID uint32
	Format        uint8
}

type HealthEventState string

const (
	HealthEventHealthy    HealthEventState = "healthy"
	HealthEventSuspect    HealthEventState = "suspect"
	HealthEventRecovering HealthEventState = "recovering"
)

type HealthEvent struct {
	State  HealthEventState
	Reason string
	Event  qmimanager.Event
	At     time.Time
}

// New 创建 QMI Core 管理器
// modemDev 参数可以为 nil，表示使用配置文件中的设备路径
func New(cfg config.DeviceConfig, modemDev *qmimanager.ModemDevice) *Manager {
	m := &Manager{
		cfg:          cfg,
		chanMu:       make(map[byte]*sync.Mutex),
		apduSessions: make(map[byte]apduSessionInfo),
	}

	// 如果提供了 modemDev，使用它；否则从配置文件构建
	var device qmimanager.ModemDevice
	if modemDev != nil {
		device = *modemDev
	} else {
		// 从配置文件构建设备信息
		device = qmimanager.ModemDevice{
			ControlPath:  cfg.ControlDevice,
			NetInterface: cfg.Interface,
			ATPort:       cfg.ATPort,
		}
		// 向后兼容：如果使用了旧的 QMIDevice 字段
		if device.ControlPath == "" && cfg.QMIDevice != "" {
			device.ControlPath = cfg.QMIDevice
		}
	}

	// 构建 quectel-qmi-go 配置
	qmiCfg := buildQMIManagerConfig(cfg, device)

	// 创建 QMI 管理器
	openFields := clientOpenModeSummary(cfg)
	if qmiCfg.ClientOptions.UseProxy {
		logger.Info("QMI client 将优先通过 qmi-proxy 打开控制口", openFields...)
	} else {
		logger.Debug("QMI client 将直接打开控制口", openFields...)
	}
	m.qmiMgr = qmimanager.New(qmiCfg, newQMIManagerLoggerAdapter(cfg.ID))
	m.qmiMgr.OnEvent(func(event qmimanager.Event) {
		m.handleQMIEvent(event)
	})

	// 设置连接回调
	m.qmiMgr.OnConnect(func(s *qmi.RuntimeSettings) {
		if m.onConnect != nil {
			go m.onConnect()
		}
	})

	return m
}

func buildQMIManagerConfig(cfg config.DeviceConfig, device qmimanager.ModemDevice) qmimanager.Config {
	isQMIBackend := strings.ToLower(strings.TrimSpace(cfg.DeviceBackend)) == "qmi"
	enableV4, enableV6, err := config.ResolveIPFamily(cfg.IPVersion)
	if err != nil {
		enableV4, enableV6 = true, false
	}
	return qmimanager.Config{
		Device:          device,
		APN:             cfg.APN,
		EnableIPv4:      enableV4,
		EnableIPv6:      enableV6,
		AutoReconnect:   true,
		NoRoute:         false,         // 关键: 允许添加默认路由 (但在底层库中设置了高 Metric=512)
		NoDNS:           true,          // 关键: 不修改系统 DNS
		DisableWMSInd:   !isQMIBackend, // 如果是纯 QMI 模式，解禁 QMI WMS 以支持接收短信；否则（AT或Auto）禁用，以防与 AT URC 冲突
		NoDial:          true,          // 数据面统一走显式 Connect()/Disconnect()，禁止底层自动拨号
		DataPlanePolicy: qmimanager.DataPlanePolicyLazy,
		Timeouts: qmimanager.TimeoutConfig{
			Init:               10 * time.Second,
			Dial:               30 * time.Second,
			SIMCheck:           10 * time.Second,
			StatusCheck:        2 * time.Second,
			Stop:               3 * time.Second,
			IndicationRegister: 5 * time.Second,
		},
		RetryPolicy: qmimanager.RetryPolicy{
			ReconnectDelays: []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second},
			ReinitDelays:    []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second},
			RadioResetAfter: 3,
		},
		HealthPolicy: qmimanager.HealthPolicy{
			FullCheckInterval:     15 * time.Second,
			IndicationDebounce:    500 * time.Millisecond,
			IPConsistencyInterval: 60 * time.Second,
		},
		EventPolicy: qmimanager.EventPolicy{
			CallbackQueueSize: 128,
		},
		RecoveryPolicy: qmimanager.RecoveryPolicy{
			MaxRecoverElapsed:       90 * time.Second, // 核心恢复 90s 仍未收敛即放弃并重建 worker
			ServiceTimeoutThreshold: 2,
			ServiceTimeoutWindow:    3 * time.Minute,
		},
		ClientOptions: ClientOptionsFromDeviceConfig(cfg),
	}
}

func (m *Manager) SetOnConnect(handler func()) {
	m.onConnect = handler
}

func (m *Manager) OnHealthEvent(handler func(HealthEvent)) {
	if handler == nil {
		return
	}
	m.healthHandlersMu.Lock()
	m.onHealthEvent = append(m.onHealthEvent, handler)
	m.healthHandlersMu.Unlock()
}

func (m *Manager) OnRecoveryExhausted(handler func(reason string, err error)) {
	if m == nil || handler == nil {
		return
	}
	m.recoveryExhaustedMu.Lock()
	m.onRecoveryExhausted = append(m.onRecoveryExhausted, handler)
	m.recoveryExhaustedMu.Unlock()
}

func (m *Manager) dispatchRecoveryExhausted(reason string, err error) {
	if m == nil {
		return
	}
	m.recoveryExhaustedMu.Lock()
	handlers := append([]func(string, error){}, m.onRecoveryExhausted...)
	m.recoveryExhaustedMu.Unlock()
	for _, handler := range handlers {
		handler(reason, err)
	}
}

func healthEventForQMIEvent(event qmimanager.Event) (HealthEvent, bool) {
	health := HealthEvent{
		Event: event,
		At:    time.Now(),
	}
	switch event.Type {
	case qmimanager.EventConnected:
		health.State = HealthEventHealthy
		health.Reason = "qmi_connected"
	case qmimanager.EventReconnecting:
		health.State = HealthEventSuspect
		health.Reason = "qmi_reconnecting"
	case qmimanager.EventDisconnected:
		health.State = HealthEventSuspect
		health.Reason = "qmi_disconnected"
	case qmimanager.EventDialFailed:
		health.State = HealthEventSuspect
		health.Reason = "qmi_dial_failed"
	case qmimanager.EventModemReset:
		health.State = HealthEventRecovering
		health.Reason = "qmi_modem_reset"
	default:
		return HealthEvent{}, false
	}
	return health, true
}

func (m *Manager) dispatchHealthEvent(event qmimanager.Event) {
	health, ok := healthEventForQMIEvent(event)
	if !ok {
		return
	}
	m.healthHandlersMu.Lock()
	handlers := append([]func(HealthEvent){}, m.onHealthEvent...)
	m.healthHandlersMu.Unlock()
	for _, handler := range handlers {
		if handler != nil {
			handler(health)
		}
	}
}

func (m *Manager) handleQMIEvent(event qmimanager.Event) {
	logQMIEvent(m.cfg.ID, event)
	m.dispatchHealthEvent(event)
	switch event.Type {
	case qmimanager.EventConnected,
		qmimanager.EventDisconnected,
		qmimanager.EventDialFailed,
		qmimanager.EventReconnecting:
		logQMIStatsSnapshot(m.cfg.ID, m.qmiMgr)
	case qmimanager.EventModemReset:
		m.releaseAllAPDULeases("modem_reset")
		logQMIStatsSnapshot(m.cfg.ID, m.qmiMgr)
		m.uimHandlersMu.Lock()
		handlers := append([]func(){}, m.onModemReset...)
		m.uimHandlersMu.Unlock()
		for _, handler := range handlers {
			if handler != nil {
				handler()
			}
		}
	case qmimanager.EventRecoveryExhausted:
		logQMIStatsSnapshot(m.cfg.ID, m.qmiMgr)
		logger.Warn("QMI 核心恢复已彻底失败，转入 worker 重建",
			"device", m.cfg.ID, "reason", event.Reason, "err", event.Error)
		m.dispatchRecoveryExhausted(event.Reason, event.Error)
	case qmimanager.EventNewSMS:
		m.smsHandlersMu.Lock()
		storedHandlers := append([]func(storage uint8, index uint32){}, m.onNewSMSStored...)
		m.smsHandlersMu.Unlock()

		for _, handler := range storedHandlers {
			if handler != nil {
				handler(event.StorageType, event.SMSIndex)
			}
		}
	case qmimanager.EventNewSMSRaw:
		m.smsHandlersMu.Lock()
		handlers := append([]func(RawSMSIndication){}, m.onNewSMSRaw...)
		m.smsHandlersMu.Unlock()

		info := RawSMSIndication{
			PDU:           append([]byte(nil), event.Pdu...),
			AckRequired:   event.SMSAckRequired,
			TransactionID: event.SMSTransactionID,
			Format:        event.SMSFormat,
		}
		for _, handler := range handlers {
			if handler != nil {
				handler(info)
			}
		}
	case qmimanager.EventUIMRefresh:
		m.uimHandlersMu.Lock()
		handlers := append([]func(info *qmi.UIMRefreshIndication){}, m.onUIMRefresh...)
		m.uimHandlersMu.Unlock()
		for _, handler := range handlers {
			if handler != nil {
				handler(event.UIMRefresh)
			}
		}
	case qmimanager.EventUIMSlotStatus:
		m.uimHandlersMu.Lock()
		handlers := append([]func(info *qmi.UIMSlotStatus){}, m.onUIMSlotStatus...)
		m.uimHandlersMu.Unlock()
		for _, handler := range handlers {
			if handler != nil {
				handler(event.UIMSlotStatus)
			}
		}
	}
}

func (m *Manager) lookupPublicIPHost(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		return []string{ip.String()}, nil
	}
	if m.publicIPLookup != nil {
		return m.publicIPLookup(ctx, host)
	}

	lookupCtx, cancel := context.WithTimeout(ctx, publicIPResolveTimeout)
	defer cancel()

	enableV4, enableV6, err := config.ResolveIPFamily(m.cfg.IPVersion)
	if err != nil {
		enableV4, enableV6 = true, false
	}
	dnsServers := m.publicIPDNSServers()
	dialer := m.boundDialer(publicIPDialTimeout)
	var ips []string
	var errs []error
	if enableV6 {
		v6, err := resolveAAAAWithTCPDNS(lookupCtx, host, dnsServers, dialer)
		if err != nil {
			errs = append(errs, err)
		}
		ips = append(ips, v6...)
	}
	if enableV4 {
		v4, err := resolveIPv4WithTCPDNS(lookupCtx, host, dnsServers, dialer)
		if err != nil {
			errs = append(errs, err)
		}
		ips = append(ips, v4...)
	}
	if len(ips) > 0 {
		return dedupeStrings(ips), nil
	}
	err = errors.Join(errs...)
	if err == nil {
		err = fmt.Errorf("host %s 未解析到可用 IP 地址", host)
	}
	logger.Debug(fmt.Sprintf("[%s] 公网 IP 探测域名解析失败", m.cfg.ID), "host", host, "err", err)
	return nil, err
}

func (m *Manager) publicIPDNSServers() []string {
	servers := make([]string, 0, 5)
	if m != nil && m.qmiMgr != nil {
		if settings := m.qmiMgr.Settings(); settings != nil {
			servers = appendDNSServer(servers, settings.IPv4DNS1)
			servers = appendDNSServer(servers, settings.IPv4DNS2)
		}
	}
	servers = append(servers, fallbackPublicIPDNSServers...)
	return dedupeStrings(servers)
}

func (m *Manager) boundDialer(timeout time.Duration) *net.Dialer {
	dialer := &net.Dialer{Timeout: timeout}
	if strings.TrimSpace(m.cfg.Interface) == "" {
		return dialer
	}
	dialer.Control = func(network, address string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, m.cfg.Interface)
		}); err != nil {
			return err
		}
		return sockErr
	}
	return dialer
}

func resolveIPv4WithTCPDNS(ctx context.Context, host string, servers []string, dialer *net.Dialer) ([]string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("dns host 不能为空")
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("dns servers 不能为空")
	}

	client := &dns.Client{
		Net:     "tcp",
		Timeout: publicIPResolveTimeout,
		Dialer:  dialer,
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)

	var errs []error
	for _, server := range servers {
		resp, _, err := client.ExchangeContext(ctx, msg, server)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", server, err))
			continue
		}
		if resp == nil {
			errs = append(errs, fmt.Errorf("%s: 空 DNS 响应", server))
			continue
		}
		if resp.Rcode != dns.RcodeSuccess {
			errs = append(errs, fmt.Errorf("%s: dns rcode=%s", server, dns.RcodeToString[resp.Rcode]))
			continue
		}

		ips := extractARecords(resp.Answer)
		if len(ips) > 0 {
			return ips, nil
		}
		errs = append(errs, fmt.Errorf("%s: 未返回 A 记录", server))
	}

	return nil, errors.Join(errs...)
}

func resolveAAAAWithTCPDNS(ctx context.Context, host string, servers []string, dialer *net.Dialer) ([]string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("dns host 不能为空")
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("dns servers 不能为空")
	}

	client := &dns.Client{
		Net:     "tcp",
		Timeout: publicIPResolveTimeout,
		Dialer:  dialer,
	}
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(host), dns.TypeAAAA)

	var errs []error
	for _, server := range servers {
		resp, _, err := client.ExchangeContext(ctx, msg, server)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", server, err))
			continue
		}
		if resp == nil {
			errs = append(errs, fmt.Errorf("%s: 空 DNS 响应", server))
			continue
		}
		if resp.Rcode != dns.RcodeSuccess {
			errs = append(errs, fmt.Errorf("%s: dns rcode=%s", server, dns.RcodeToString[resp.Rcode]))
			continue
		}

		ips := extractAAAARecords(resp.Answer)
		if len(ips) > 0 {
			return ips, nil
		}
		errs = append(errs, fmt.Errorf("%s: 未返回 AAAA 记录", server))
	}

	return nil, errors.Join(errs...)
}

func extractARecords(records []dns.RR) []string {
	ips := make([]string, 0, len(records))
	for _, record := range records {
		a, ok := record.(*dns.A)
		if !ok || a == nil || a.A == nil {
			continue
		}
		if ipv4 := a.A.To4(); ipv4 != nil {
			ips = append(ips, ipv4.String())
		}
	}
	return dedupeStrings(ips)
}

func extractAAAARecords(records []dns.RR) []string {
	ips := make([]string, 0, len(records))
	for _, record := range records {
		aaaa, ok := record.(*dns.AAAA)
		if !ok || aaaa == nil || aaaa.AAAA == nil {
			continue
		}
		if ipv6 := aaaa.AAAA.To16(); ipv6 != nil && ipv6.To4() == nil {
			ips = append(ips, ipv6.String())
		}
	}
	return dedupeStrings(ips)
}

func appendDNSServer(servers []string, ip net.IP) []string {
	if ip == nil {
		return servers
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return append(servers, net.JoinHostPort(ipv4.String(), "53"))
	}
	return servers
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (m *Manager) ControlDevice() string {
	return strings.TrimSpace(m.cfg.ControlDevice)
}

func (m *Manager) QMIState() qmimanager.State {
	if m == nil || m.qmiMgr == nil {
		return qmimanager.StateDisconnected
	}
	return m.qmiMgr.State()
}

func (m *Manager) SetAPDUArbiter(arbiter *apduarbiter.Arbiter) {
	if m == nil {
		return
	}
	m.apduLeaseMu.Lock()
	defer m.apduLeaseMu.Unlock()
	if m.apduSessions == nil {
		m.apduSessions = make(map[byte]apduSessionInfo)
	}
	if m.apduArbiter == arbiter {
		return
	}
	clear(m.apduSessions)
	m.apduArbiter = arbiter
}

func (m *Manager) acquireAPDUTransportLease(ctx context.Context, timeout time.Duration, owner string, class apduarbiter.APDUClass, channel byte, scope apduarbiter.TransportScope) (*apduarbiter.Lease, error) {
	m.apduLeaseMu.Lock()
	arbiter := m.apduArbiter
	m.apduLeaseMu.Unlock()
	if arbiter == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return arbiter.AcquireTransport(ctx, apduarbiter.Request{
		Owner:   owner,
		Mode:    "QMI",
		Class:   class,
		Channel: int(channel),
		Scope:   scope,
	})
}

func (m *Manager) bindAPDUSession(channel byte, owner string, class ...apduarbiter.APDUClass) {
	m.apduLeaseMu.Lock()
	defer m.apduLeaseMu.Unlock()
	if m.apduSessions == nil {
		m.apduSessions = make(map[byte]apduSessionInfo)
	}
	sessionClass := apduarbiter.APDUClassEUICCWrite
	if len(class) > 0 && class[0] != "" {
		sessionClass = class[0]
	}
	m.apduSessions[channel] = apduSessionInfo{
		Channel:  channel,
		Owner:    strings.TrimSpace(owner),
		Class:    sessionClass,
		OpenedAt: time.Now(),
	}
}

func (m *Manager) getAPDUSession(channel byte) (apduSessionInfo, bool) {
	m.apduLeaseMu.Lock()
	defer m.apduLeaseMu.Unlock()
	session, ok := m.apduSessions[channel]
	return session, ok
}

func (m *Manager) hasAPDUSession(channel byte) bool {
	m.apduLeaseMu.Lock()
	defer m.apduLeaseMu.Unlock()
	_, ok := m.apduSessions[channel]
	return ok
}

func (m *Manager) takeAPDUSession(channel byte) (apduSessionInfo, bool) {
	m.apduLeaseMu.Lock()
	defer m.apduLeaseMu.Unlock()
	session, ok := m.apduSessions[channel]
	delete(m.apduSessions, channel)
	return session, ok
}

func (m *Manager) apduTransportProfile(channel byte) (string, apduarbiter.APDUClass) {
	owner := "esim_apdu"
	class := apduarbiter.APDUClassEUICCWrite
	if channel == 0 {
		return "vowifi_aka", apduarbiter.APDUClassUSIMAKA
	}
	if session, ok := m.getAPDUSession(channel); ok {
		if session.Owner != "" {
			owner = session.Owner
		}
		if session.Class != "" {
			class = session.Class
		}
		return owner, class
	}
	return "unbound_channel_apdu", class
}

func (m *Manager) releaseAllAPDULeases(reason string) {
	if m == nil {
		return
	}
	m.apduLeaseMu.Lock()
	count := len(m.apduSessions)
	clear(m.apduSessions)
	arbiter := m.apduArbiter
	m.apduLeaseMu.Unlock()

	if arbiter != nil {
		arbiter.InvalidateSIMAuthReady(reason)
	}
	if count > 0 {
		logger.Warn(fmt.Sprintf("[%s] APDU logical session registry 已清理", m.cfg.ID), "reason", reason, "session_count", count)
	}
}

func (m *Manager) ReleaseAPDULeasesForSwitchTeardown() {
	m.releaseAllAPDULeases("esim_switch_teardown")
}

func (m *Manager) OpenEUICCLogicalChannel(ctx context.Context, slot byte, aid []byte) (byte, error) {
	return m.openUIMLogicalChannel(ctx, slot, aid, "esim_session_open", "esim", apduarbiter.APDUClassEUICCWrite)
}

func (m *Manager) OpenSIMAuthLogicalChannel(ctx context.Context, slot byte, aid []byte) (byte, error) {
	return m.openUIMLogicalChannel(ctx, slot, aid, "vowifi_aka_open", "vowifi_aka", apduarbiter.APDUClassUSIMAKA)
}

func (m *Manager) openUIMLogicalChannel(ctx context.Context, slot byte, aid []byte, leaseOwner, sessionOwner string, class apduarbiter.APDUClass) (byte, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, fmt.Errorf("qmi_uim_not_available")
	}
	lease, err := m.acquireAPDUTransportLease(ctx, 10*time.Second, leaseOwner, class, 0, apduarbiter.TransportScopeExclusive)
	if err != nil {
		return 0, err
	}
	if lease != nil {
		defer lease.Release()
		lease.Touch()
	}
	m.euiccMu.Lock()
	defer m.euiccMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	channel, err := m.qmiMgr.OpenLogicalChannelContext(ctx, slot, aid)
	if err != nil {
		return 0, err
	}
	if lease != nil {
		lease.Touch()
	}
	m.bindAPDUSession(channel, sessionOwner, class)
	return channel, nil
}

// getOrCreateChanMu 返回指定 channel 对应的互斥锁（懒创建，线程安全）
func (m *Manager) getOrCreateChanMu(channel byte) *sync.Mutex {
	m.chanMuMu.RLock()
	mu := m.chanMu[channel]
	m.chanMuMu.RUnlock()
	if mu != nil {
		return mu
	}
	m.chanMuMu.Lock()
	defer m.chanMuMu.Unlock()
	if mu = m.chanMu[channel]; mu != nil {
		return mu
	}
	mu = &sync.Mutex{}
	m.chanMu[channel] = mu
	return mu
}

func (m *Manager) CloseEUICCLogicalChannel(ctx context.Context, slot byte, channel byte) error {
	return m.closeUIMLogicalChannel(ctx, slot, channel, "esim_session_close", apduarbiter.APDUClassEUICCWrite)
}

func (m *Manager) CloseSIMAuthLogicalChannel(ctx context.Context, slot byte, channel byte) error {
	return m.closeUIMLogicalChannel(ctx, slot, channel, "vowifi_aka_close", apduarbiter.APDUClassRecovery)
}

func (m *Manager) closeUIMLogicalChannel(ctx context.Context, slot byte, channel byte, defaultOwner string, defaultClass apduarbiter.APDUClass) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_uim_not_available")
	}
	session, ok := m.takeAPDUSession(channel)
	owner := defaultOwner
	class := defaultClass
	if ok {
		if session.Owner != "" {
			owner = session.Owner + "_close"
		}
		if session.Class == apduarbiter.APDUClassUSIMAKA {
			class = apduarbiter.APDUClassRecovery
		} else if session.Class != "" {
			class = session.Class
		}
	}
	lease, err := m.acquireAPDUTransportLease(ctx, 10*time.Second, owner, class, channel, apduarbiter.TransportScopeExclusive)
	if err != nil {
		return err
	}
	if lease != nil {
		defer lease.Release()
		lease.Touch()
	}
	m.euiccMu.Lock()
	defer m.euiccMu.Unlock()
	err = m.qmiMgr.CloseLogicalChannelContext(ctx, slot, channel)
	if lease != nil {
		lease.Touch()
	}
	return err
}

func (m *Manager) TransmitEUICCAPDU(ctx context.Context, slot byte, channel byte, command []byte) ([]byte, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	owner, class := m.apduTransportProfile(channel)
	scope := apduarbiter.TransportScopeExclusive
	if channel > 0 {
		scope = apduarbiter.TransportScopeQMIChannel
	}
	lease, err := m.acquireAPDUTransportLease(ctx, 10*time.Second, owner, class, channel, scope)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
		lease.Touch()
	}
	// per-channel 互斥：同一通道内 APDU 顺序执行，不同通道可并发发送
	chanMu := m.getOrCreateChanMu(channel)
	chanMu.Lock()
	defer chanMu.Unlock()
	// 先检查 ctx 状态：如果上层已取消，不发送 APDU。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := m.qmiMgr.SendAPDUContext(ctx, slot, channel, command)
	if lease != nil {
		lease.Touch()
	}
	return resp, err
}

func (m *Manager) GetNativeMCCMNC(ctx context.Context) (mcc, mnc string, err error) {
	if m == nil || m.qmiMgr == nil {
		return "", "", fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.GetNativeMCCMNC(ctx)
}

func (m *Manager) GetNativeSPN(ctx context.Context) (string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.GetNativeSPN(ctx)
}

func (m *Manager) GetSIMMetadata(ctx context.Context) (*qmi.SIMMetadata, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.GetSIMMetadata(ctx)
}

func (m *Manager) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	if m == nil || m.qmiMgr == nil {
		return qmimanager.UIMReadiness{
			TransportReady: false,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
		}, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetUIMReadiness(ctx)
}

func (m *Manager) RequestCoreRecovery(reason string) bool {
	if m == nil || m.qmiMgr == nil {
		return false
	}
	return m.qmiMgr.RequestCoreRecovery(reason)
}

func (m *Manager) GetUSIMAID(ctx context.Context) ([]byte, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.GetUSIMAID(ctx)
}

func (m *Manager) EnsureSIMProvisioned(ctx context.Context, opts qmimanager.EnsureSIMProvisionedOptions) (qmimanager.UIMReadiness, error) {
	if m == nil || m.qmiMgr == nil {
		return qmimanager.UIMReadiness{
			TransportReady: false,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
		}, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.EnsureSIMProvisioned(ctx, opts)
}

func (m *Manager) GetISIMAID(ctx context.Context) ([]byte, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.GetISIMAID(ctx)
}

func (m *Manager) UIMGetFileAttributesWithSession(ctx context.Context, sessionType uint8, fileID uint16, path []uint8) (*qmi.UIMFileAttributes, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.UIMGetFileAttributesWithSession(ctx, sessionType, fileID, path)
}

func (m *Manager) UIMReadTransparentWithSession(ctx context.Context, sessionType uint8, fileID uint16, path []uint8) ([]byte, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.UIMReadTransparentWithSession(ctx, sessionType, fileID, path)
}

func (m *Manager) UIMReadRecordWithSession(ctx context.Context, sessionType uint8, fileID uint16, path []uint8, recordNumber uint16, recordLength uint16) (*qmi.UIMRecordData, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_uim_not_available")
	}
	return m.qmiMgr.UIMReadRecordWithSession(ctx, sessionType, fileID, path, recordNumber, recordLength)
}

func (m *Manager) GetSMSC(ctx context.Context) (string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", fmt.Errorf("qmi_uim_not_available")
	}
	lease, err := m.acquireAPDUTransportLease(ctx, 15*time.Second, "smsc_query", apduarbiter.APDUClassSMSC, 0, apduarbiter.TransportScopeExclusive)
	if err != nil {
		return "", err
	}
	if lease != nil {
		defer lease.Release()
		lease.Touch()
	}
	return m.qmiMgr.GetSMSC(ctx)
}

// Start 启动 QMI 管理器
func (m *Manager) Start() error {
	logger.Info(fmt.Sprintf("[%s] 启动 QMI 管理器", m.cfg.ID))
	return m.qmiMgr.Start()
}

// StartCore 仅启动 QMI 核心控制面，不建立数据连接。
func (m *Manager) StartCore() error {
	logger.Info(fmt.Sprintf("[%s] 启动 QMI Core", m.cfg.ID))
	return m.qmiMgr.StartCore()
}

// StartCoreContext 仅启动 QMI 核心控制面，不建立数据连接，并受调用方 context 约束。
func (m *Manager) StartCoreContext(ctx context.Context) error {
	logger.Info(fmt.Sprintf("[%s] 启动 QMI Core", m.cfg.ID))
	return m.qmiMgr.StartCoreContext(ctx)
}

// Connect 显式建立数据连接。
func (m *Manager) Connect() error {
	logger.Info(fmt.Sprintf("[%s] 建立数据连接", m.cfg.ID))
	return m.qmiMgr.Connect()
}

// Disconnect 断开数据连接，但保留 QMI Core。
func (m *Manager) Disconnect() error {
	logger.Info(fmt.Sprintf("[%s] 断开数据连接", m.cfg.ID))
	return m.qmiMgr.Disconnect()
}

// ResetExistingDataConnection tears down a data call that may have been left
// active before VoHive took ownership of this QMI device.
func (m *Manager) ResetExistingDataConnection(ctx context.Context) (bool, error) {
	if m == nil {
		return false, fmt.Errorf("qmi_manager_not_available")
	}
	if m.resetExistingDataConnection != nil {
		return m.resetExistingDataConnection(ctx)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, existingDataConnectionResetTimeout)
	defer cancel()

	reset, err := m.resetExistingDataConnectionViaCore(ctx)
	if err != nil {
		return false, err
	}
	if reset {
		m.flushQMIInterfaceAfterExistingDataCleanup()
	}
	return reset, nil
}

func (m *Manager) resetExistingDataConnectionViaCore(ctx context.Context) (bool, error) {
	if m == nil {
		return false, fmt.Errorf("qmi_manager_not_available")
	}
	if m.resetExistingDataConnectionViaCoreHook != nil {
		return m.resetExistingDataConnectionViaCoreHook(ctx)
	}
	if m.qmiMgr == nil {
		return false, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.ResetExistingDataConnection(ctx)
}

func (m *Manager) flushQMIInterfaceAfterExistingDataCleanup() {
	if m == nil {
		return
	}
	if iface := strings.TrimSpace(m.cfg.Interface); iface != "" {
		netcfg.FlushAddresses(iface)
		netcfg.FlushRoutes(iface)
		netcfg.BringDown(iface)
	}
}

// Stop 停止 QMI 管理器
func (m *Manager) Stop() error {
	logger.Info(fmt.Sprintf("[%s] 停止 QMI 管理器", m.cfg.ID))
	m.releaseAllAPDULeases("stop")
	return m.qmiMgr.Stop()
}

// RotateIP 切换 IP 地址
func (m *Manager) RotateIP() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.IsConnected() {
		return fmt.Errorf("network_not_connected")
	}

	oldIP := m.GetPrivateIP()
	logger.Info(fmt.Sprintf("[%s] 开始 IP 切换", m.cfg.ID), "old_ip", oldIP)

	// 调用 quectel-qmi-go 的 RotateIP 方法
	err := m.qmiMgr.RotateIP()
	if err != nil {
		logger.Error(fmt.Sprintf("[%s] IP 切换失败", m.cfg.ID), "err", err)
		return err
	}

	newIP := m.GetPrivateIP()
	logger.Info(fmt.Sprintf("[%s] IP 切换完成", m.cfg.ID), "old_ip", oldIP, "new_ip", newIP)

	return nil
}

// ============================================================================
// QMI Property Queries (Forwarded to qmimanager)
// ============================================================================

// GetDeviceSerialNumbers returns IMEI and other serials
func (m *Manager) GetDeviceSerialNumbers(ctx context.Context) (*qmi.DeviceInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetDeviceSerialNumbers(ctx)
}

// GetDeviceRevision returns firmware version
func (m *Manager) GetDeviceRevision(ctx context.Context) (string, string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", "", fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetDeviceRevision(ctx)
}

// GetIMSI returns IMSI from SIM
func (m *Manager) GetIMSI(ctx context.Context) (string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetIMSI(ctx)
}

// GetICCID returns ICCID from SIM
func (m *Manager) GetICCID(ctx context.Context) (string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetICCID(ctx)
}

// GetMSISDN returns MSISDN when available.
func (m *Manager) GetMSISDN(ctx context.Context) (string, error) {
	if m == nil || m.qmiMgr == nil {
		return "", fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetMSISDN(ctx)
}

// GetSIMStatus returns current SIM state
func (m *Manager) GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error) {
	if m == nil || m.qmiMgr == nil {
		return qmi.SIMAbsent, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetSIMStatus(ctx)
}

// GetServingSystem returns registration info
func (m *Manager) GetServingSystem(ctx context.Context) (*qmi.ServingSystem, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetServingSystem(ctx)
}

// GetSignalStrength returns RSSI/RSRP
func (m *Manager) GetSignalStrength(ctx context.Context) (*qmi.SignalStrength, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetSignalStrength(ctx)
}

// GetSignalInfo returns detailed LTE/5G signal info
func (m *Manager) GetSignalInfo(ctx context.Context) (*qmi.SignalInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetSignalInfo(ctx)
}

// GetSysInfo returns LAC/CellID
func (m *Manager) GetSysInfo(ctx context.Context) (*qmi.SysInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetSysInfo(ctx)
}

// --- NAS Low-level Methods ---

func (m *Manager) NASGetRFBandInfo(ctx context.Context) (*qmi.RFBandInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetRFBandInfo(ctx)
}

func (m *Manager) NASGetTechnologyPreference(ctx context.Context) (*qmi.TechnologyPreference, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetTechnologyPreference(ctx)
}

func (m *Manager) NASSetTechnologyPreference(ctx context.Context, pref qmi.TechnologyPreference) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASSetTechnologyPreference(ctx, pref)
}

func (m *Manager) NASGetSystemSelectionPreference(ctx context.Context) (*qmi.SystemSelectionPreference, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetSystemSelectionPreference(ctx)
}

func (m *Manager) NASSetSystemSelectionPreference(ctx context.Context, pref qmi.SystemSelectionPreference) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASSetSystemSelectionPreference(ctx, pref)
}

func (m *Manager) NASGetCellLocationInfo(ctx context.Context) (*qmi.CellLocationInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetCellLocationInfo(ctx)
}

func (m *Manager) NASGetNetworkTime(ctx context.Context) (*qmi.NetworkTimeInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetNetworkTime(ctx)
}

func (m *Manager) NASInitiateNetworkRegister(ctx context.Context, req qmi.NASInitiateNetworkRegisterRequest) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASInitiateNetworkRegister(ctx, req)
}

func (m *Manager) NASForceNetworkSearch(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASForceNetworkSearch(ctx)
}

func (m *Manager) NASAttachDetach(ctx context.Context, attached bool) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASAttachDetach(ctx, attached)
}

func (m *Manager) NASGetOperatorName(ctx context.Context) (*qmi.NASOperatorNameInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetOperatorName(ctx)
}

func (m *Manager) NASGetPLMNName(ctx context.Context, req qmi.NASPLMNNameRequest) (*qmi.NASPLMNNameInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASGetPLMNName(ctx, req)
}

func (m *Manager) NASConfigSignalInfoV2(ctx context.Context, cfg qmi.NASSignalInfoConfigV2) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASConfigSignalInfoV2(ctx, cfg)
}

func (m *Manager) NASRegisterIndications(ctx context.Context, cfg qmi.NASIndicationRegistration) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASRegisterIndications(ctx, cfg)
}

// GetOperatingMode returns CFUN status
func (m *Manager) GetOperatingMode(ctx context.Context) (qmi.OperatingMode, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.GetOperatingMode(ctx)
}

// SetOperatingMode sets CFUN status
func (m *Manager) SetOperatingMode(ctx context.Context, mode qmi.OperatingMode) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.SetOperatingMode(ctx, mode)
}

// UIMReset resets the modem UIM service state.
func (m *Manager) UIMReset(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.UIMReset(ctx)
}

// UIMPowerOffSIM powers off the specified SIM slot.
func (m *Manager) UIMPowerOffSIM(ctx context.Context, slot uint8) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.UIMPowerOffSIM(ctx, slot)
}

// UIMPowerOnSIM powers on the specified SIM slot.
func (m *Manager) UIMPowerOnSIM(ctx context.Context, slot uint8) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.UIMPowerOnSIM(ctx, slot)
}

func (m *Manager) UIMPostSwitchReload(ctx context.Context, readiness qmimanager.UIMReadiness, opts qmimanager.UIMPostSwitchReloadOptions) (uint8, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.UIMPostSwitchReload(ctx, readiness, opts)
}

// --- WDS Low-level Methods ---

func (m *Manager) WDSGetChannelRates(ctx context.Context) (*qmi.ChannelRates, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSGetChannelRates(ctx)
}

func (m *Manager) WDSGetPacketStatistics(ctx context.Context, mask uint32) (*qmi.PacketStatistics, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSGetPacketStatistics(ctx, mask)
}

func (m *Manager) WDSCreateProfile(ctx context.Context, profileType uint8, settings qmi.WDSProfileSettings) (*qmi.ProfileInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSCreateProfile(ctx, profileType, settings)
}

func (m *Manager) WDSModifyProfileSettings(ctx context.Context, profileType, profileIndex uint8, settings qmi.WDSProfileSettings) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSModifyProfileSettings(ctx, profileType, profileIndex, settings)
}

func (m *Manager) WDSDeleteProfile(ctx context.Context, profileType, profileIndex uint8) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSDeleteProfile(ctx, profileType, profileIndex)
}

func (m *Manager) WDSGetAutoconnectSettings(ctx context.Context) (*qmi.AutoconnectSettings, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSGetAutoconnectSettings(ctx)
}

func (m *Manager) WDSSetAutoconnectSettings(ctx context.Context, settings qmi.AutoconnectSettings) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSSetAutoconnectSettings(ctx, settings)
}

func (m *Manager) WDSGetDataBearerTechnology(ctx context.Context) (*qmi.DataBearerTechnologyInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSGetDataBearerTechnology(ctx)
}

func (m *Manager) WDSGetCurrentDataBearerTechnology(ctx context.Context) (*qmi.CurrentBearerTechnologyInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WDSGetCurrentDataBearerTechnology(ctx)
}

// --- WMS Low-level Methods ---

func (m *Manager) WMSSendRawMessage(ctx context.Context, format uint8, pdu []byte) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WMSSendRawMessage(ctx, format, pdu)
}

func (m *Manager) WMSRawReadMessage(ctx context.Context, storageType uint8, index uint32) ([]byte, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WMSRawReadMessage(ctx, storageType, index)
}

func (m *Manager) WMSDeleteMessage(ctx context.Context, storageType uint8, index uint32) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WMSDeleteMessage(ctx, storageType, index)
}

func (m *Manager) WMSListMessagesAuto(ctx context.Context, storageType uint8) ([]struct {
	Index uint32
	Tag   qmi.MessageTagType
}, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WMSListMessagesAuto(ctx, storageType)
}

func (m *Manager) WMSDeleteMessagesByTag(ctx context.Context, storageType uint8, tag qmi.MessageTagType, mode qmi.MessageMode) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WMSDeleteMessagesByTag(ctx, storageType, tag, mode)
}

// --- VOICE Low-level Methods ---

func (m *Manager) VOICEDialCall(ctx context.Context, number string) (uint8, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEDialCall(ctx, number)
}

func (m *Manager) VOICEAnswerCall(ctx context.Context, callID uint8) (uint8, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEAnswerCall(ctx, callID)
}

func (m *Manager) VOICEEndCall(ctx context.Context, callID uint8) (uint8, error) {
	if m == nil || m.qmiMgr == nil {
		return 0, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEEndCall(ctx, callID)
}

func (m *Manager) VOICEGetAllCallInfo(ctx context.Context) (*qmi.VoiceAllCallInfo, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEGetAllCallInfo(ctx)
}

func (m *Manager) OnVoiceCallStatus(handler func(*qmi.VoiceAllCallInfo)) error {
	if m == nil || m.qmiMgr == nil || handler == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	m.qmiMgr.OnVoiceCallStatus(handler)
	return nil
}

func (m *Manager) VOICEOriginateUSSD(ctx context.Context, req qmi.VoiceUSSDRequest) (*qmi.VoiceUSSDResponse, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEOriginateUSSD(ctx, req)
}

func (m *Manager) VOICEOriginateUSSDNoWait(ctx context.Context, req qmi.VoiceUSSDRequest) error {
	if m == nil || m.qmiMgr == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEOriginateUSSDNoWait(ctx, req)
}

func (m *Manager) VOICECancelUSSD(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICECancelUSSD(ctx)
}

func (m *Manager) VOICEGetConfig(ctx context.Context, query qmi.VoiceConfigQuery) (*qmi.VoiceConfig, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, qmimanager.ErrServiceNotReady("VOICE")
	}
	return m.qmiMgr.VOICEGetConfig(ctx, query)
}

func (m *Manager) OnVoiceUSSD(handler func(*qmi.VoiceUSSDIndication)) error {
	if m == nil || m.qmiMgr == nil || handler == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	m.qmiMgr.OnVoiceUSSD(handler)
	return nil
}

func (m *Manager) OnVoiceUSSDReleased(handler func()) error {
	if m == nil || m.qmiMgr == nil || handler == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	m.qmiMgr.OnVoiceUSSDReleased(handler)
	return nil
}

func (m *Manager) OnVoiceUSSDNoWaitResult(handler func(*qmi.VoiceUSSDNoWaitIndication)) error {
	if m == nil || m.qmiMgr == nil || handler == nil {
		return qmimanager.ErrServiceNotReady("VOICE")
	}
	m.qmiMgr.OnVoiceUSSDNoWaitResult(handler)
	return nil
}

func (m *Manager) AckRawSMS(ctx context.Context, info RawSMSIndication, success bool) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	protocol := qmi.WMSMessageProtocolWCDMA
	if info.Format == 0x00 {
		protocol = qmi.WMSMessageProtocolCDMA
	}
	_, err := m.qmiMgr.WMSSendAck(ctx, qmi.WMSAckRequest{
		TransactionID: info.TransactionID,
		Protocol:      protocol,
		Success:       success,
	})
	return err
}

// ============================================================================
// SMS Methods (Wrapper around qmimanager)
// ============================================================================

// ListSMS lists SMS messages
func (m *Manager) ListSMS(storageType uint8, tag qmi.MessageTagType) ([]struct {
	Index uint32
	Tag   qmi.MessageTagType
}, error) {
	return m.qmiMgr.ListSMS(storageType, tag)
}

// ReadSMS reads and decodes an SMS message
func (m *Manager) ReadSMS(preferredStorage uint8, index uint32) (*qmimanager.DecodedSMS, error) {
	if preferredStorage == 0 || preferredStorage == 1 {
		return m.qmiMgr.ReadSMS(preferredStorage, index)
	}

	sms, err := m.qmiMgr.ReadSMS(1, index)
	if err == nil {
		return sms, nil
	}

	logger.Warn(fmt.Sprintf("[%s] Storage 1 读取失败，尝试 Storage 0", m.cfg.ID), "err", err)
	return m.qmiMgr.ReadSMS(0, index)
}

// SendSMS sends a text message
func (m *Manager) SendSMS(number, text string) error {
	return m.qmiMgr.SendSMS(number, text)
}

func (m *Manager) EnsureSMSReady(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.EnsureSMSReady(ctx)
}

func (m *Manager) OnNewSMSWithStorage(handler func(storage uint8, index uint32)) {
	if handler == nil {
		return
	}
	m.smsHandlersMu.Lock()
	m.onNewSMSStored = append(m.onNewSMSStored, handler)
	m.smsHandlersMu.Unlock()
}

func (m *Manager) OnNewSMSRaw(handler func(RawSMSIndication)) {
	if handler == nil {
		return
	}
	m.smsHandlersMu.Lock()
	m.onNewSMSRaw = append(m.onNewSMSRaw, handler)
	m.smsHandlersMu.Unlock()
}

// OnUIMRefresh registers a callback for UIM refresh indications.
func (m *Manager) OnUIMRefresh(handler func(info *qmi.UIMRefreshIndication)) {
	if handler == nil {
		return
	}
	m.uimHandlersMu.Lock()
	m.onUIMRefresh = append(m.onUIMRefresh, handler)
	m.uimHandlersMu.Unlock()
}

// OnUIMSlotStatus registers a callback for UIM slot status indications.
func (m *Manager) OnUIMSlotStatus(handler func(info *qmi.UIMSlotStatus)) {
	if handler == nil {
		return
	}
	m.uimHandlersMu.Lock()
	m.onUIMSlotStatus = append(m.onUIMSlotStatus, handler)
	m.uimHandlersMu.Unlock()
}

// OnModemReset registers a callback for modem reset indications.
func (m *Manager) OnModemReset(handler func()) {
	if handler == nil {
		return
	}
	m.uimHandlersMu.Lock()
	m.onModemReset = append(m.onModemReset, handler)
	m.uimHandlersMu.Unlock()
}

// GetDeviceSnapshot 返回底层 QMI 库的设备状态快照（由 NAS Indication 事件驱动更新）。
// 实现 backend.QMISource 接口，供 QMIBackend 零 IPC 读取运营商/信号数据。
func (m *Manager) GetDeviceSnapshot() *qmimanager.DeviceSnapshot {
	if m == nil || m.qmiMgr == nil {
		return nil
	}
	return m.qmiMgr.GetDeviceSnapshot()
}

// OnSimStatusChanged 注册 SIM 卡状态变化回调（插拔/状态切换时触发）。
// 供 Pool 层订阅，当 SIM 卡变化时触发 ICCID/IMSI 缓存刷新。
func (m *Manager) OnSimStatusChanged(handler func()) {
	if handler == nil {
		return
	}
	m.qmiMgr.OnEvent(func(e qmimanager.Event) {
		if e.Type == qmimanager.EventSimStatusChanged {
			handler()
		}
	})
}

// GetPrivateIP 获取私有 IP (内网 IP)
func (m *Manager) GetPrivateIP() string {
	settings := m.qmiMgr.Settings()
	if settings != nil && settings.IPv4Address != nil {
		return settings.IPv4Address.String()
	}
	return ""
}

// IsInterfaceUp 检查接口是否启动
func (m *Manager) IsInterfaceUp() bool {
	return m.IsConnected()
}

// IsConnected 检查数据面是否已建立。
func (m *Manager) IsConnected() bool {
	if m == nil || m.qmiMgr == nil {
		return false
	}
	return m.qmiMgr.IsConnected()
}

// WaitCoreReady 等待底层 QMI core 服务完全就绪。
// 典型场景：切卡后 modem reset 恢复完成后再发起 UIM/APDU 操作。
func (m *Manager) WaitCoreReady(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WaitCoreReady(ctx)
}

// WaitControlReady waits until QMI control services are available.
// It does not require SIM identity convergence.
func (m *Manager) WaitControlReady(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WaitControlReady(ctx)
}

// WaitIdentityReady waits until QMI identity convergence is complete.
func (m *Manager) WaitIdentityReady(ctx context.Context) error {
	if m == nil || m.qmiMgr == nil {
		return fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.WaitIdentityReady(ctx)
}

// GetPublicIPNoCache 并发极速探测公网 IP (外网 IP)，不区分地址族，谁先连上用谁。
// 双栈场景下该结果的地址族不确定，需要同时拿到 v4/v6 时请使用 GetPublicIPv4AndV6NoCache。
func (m *Manager) GetPublicIPNoCache() string {
	return m.publicIPProber().Probe(context.Background(), netprobe.FamilyAny)
}

// GetPublicIPv4AndV6NoCache 按 DeviceConfig.IPVersion 对 v4/v6 分别独立探测，
// 避免双栈模式下两个族共用一次探测时被其中一族（通常是更快建联的 v6）持续抢跑，
// 导致另一族的公网地址永远拿不到、缓存与数据库字段无法刷新。
func (m *Manager) GetPublicIPv4AndV6NoCache() (publicV4 string, publicV6 string) {
	enableV4, enableV6, err := config.ResolveIPFamily(m.cfg.IPVersion)
	if err != nil {
		enableV4, enableV6 = true, false
	}
	// 配置允许 v6 不代表 v6 数据承载真的建立成功（网络可能以 ESM cause #50 等拒绝 v6 PDP）。
	// 承载未建立时 v6 探测必然失败，跳过它以避免每轮刷新都白白等满超时。
	if enableV6 && !m.ipv6BearerUp() {
		enableV6 = false
	}

	var wg sync.WaitGroup
	prober := m.publicIPProber()
	if enableV4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ip := prober.Probe(context.Background(), netprobe.FamilyV4); ip != "" {
				publicV4 = ip
			}
		}()
	}
	if enableV6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ip := prober.Probe(context.Background(), netprobe.FamilyV6); ip != "" {
				publicV6 = ip
			}
		}()
	}
	wg.Wait()
	return publicV4, publicV6
}

// ipv6BearerUp 报告 IPv6 数据承载是否已实际建立。
func (m *Manager) ipv6BearerUp() bool {
	if m.hasIPv6Bearer != nil {
		return m.hasIPv6Bearer()
	}
	return strings.TrimSpace(m.GetPrivateIPv6()) != ""
}

func (m *Manager) publicIPProber() *netprobe.Prober {
	return netprobe.New(netprobe.Config{
		Interface: m.cfg.Interface,
		URLs:      ipCheckURLs,
		Timeout:   publicIPRequestTimeout,
		Lookup:    m.lookupPublicIPHost,
	})
}

// GetPrivateIPv6 获取私有 IPv6 地址（v6/双栈模式下非空）
func (m *Manager) GetPrivateIPv6() string {
	if m == nil || m.qmiMgr == nil {
		return ""
	}
	return qmiSettingsIPv6(m.qmiMgr.Settings())
}

func (m *Manager) CachedSMSC() string {
	if m.qmiMgr == nil {
		return ""
	}
	return m.qmiMgr.CachedSMSC()
}

func (m *Manager) NASPerformNetworkScan(ctx context.Context) ([]qmi.NetworkScanResult, error) {
	if m == nil || m.qmiMgr == nil {
		return nil, fmt.Errorf("qmi_manager_not_available")
	}
	return m.qmiMgr.NASPerformNetworkScan(ctx)
}

func (m *Manager) NASIncrementalNetworkScanSnapshot() (*qmi.NASIncrementalNetworkScanInfo, time.Time, bool) {
	if m == nil || m.qmiMgr == nil {
		return nil, time.Time{}, false
	}
	snapshot := m.qmiMgr.GetDeviceSnapshot()
	if snapshot == nil {
		return nil, time.Time{}, false
	}
	return snapshot.NASIncrementalScan()
}
