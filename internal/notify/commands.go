package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/messaging"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/swordstudiox/vohive-plus/internal/db"
)

// ---------- 通用命令 handler（TG 和飞书共用） ----------

func switchProfileIndexLabel(idx int) string {
	return fmt.Sprintf("%d.", idx)
}

func commandUsageBlock(title, usage, example string) string {
	return fmt.Sprintf("%s / 用法\n用法    %s\n示例    %s", title, usage, example)
}

func commandFailureBlock(title, deviceID, reason string) string {
	return fmt.Sprintf("%s / 失败\n设备    %s\n原因    %s", title, strings.TrimSpace(deviceID), strings.TrimSpace(reason))
}

func commandEmptyBlock(title, result string) string {
	return fmt.Sprintf("%s / 空\n结果    %s", title, strings.TrimSpace(result))
}

func unknownCommandReply(command string) string {
	return fmt.Sprintf("未知命令 / %s\n提示    请检查命令名或使用 /list、/status、/send 等已注册命令", strings.TrimSpace(command))
}

func commandValidationBlock(title string, fields ...string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s / 参数错误", strings.TrimSpace(title)))
	for i := 0; i+1 < len(fields); i += 2 {
		sb.WriteString(fmt.Sprintf("\n%s    %s", strings.TrimSpace(fields[i]), strings.TrimSpace(fields[i+1])))
	}
	return sb.String()
}

func switchAcceptedBlock(deviceID, profileName string) string {
	return fmt.Sprintf("切换 eSIM / 已受理\n设备    %s\n目标    %s", strings.TrimSpace(deviceID), strings.TrimSpace(profileName))
}

// handleCmdSendSMS 处理 /send 命令
// 命令格式: /send <device_id> <phone> <message>
func (m *Manager) handleCmdSendSMS(cmdCtx CommandContext, args []string) string {
	if len(args) < 3 {
		return commandUsageBlock("发送短信", "/send [设备ID] [手机号] [消息]", "/send ec20_1 +8613812345678 测试短信")
	}

	deviceID := args[0]
	phone := args[1]
	message := strings.Join(args[2:], " ")

	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return commandFailureBlock("发送短信", deviceID, "设备未找到")
	}

	displayName := worker.ID
	if worker.Config.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", worker.Config.Name, worker.ID)
	}
	isVoWiFi := m.pool.IsVoWiFiActive(deviceID)

	// /send 是用户的显式操作，不能因 notifyPool 满载被丢弃。
	// 这里使用独立 goroutine，确保命令被执行并回执结果。
	go func() {
		var sendErr error
		if isVoWiFi {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx = messaging.WithSuppressSendTGSuccess(ctx)
			sendErr = m.pool.SendVoWiFiSMS(ctx, deviceID, phone, message)
			if sendErr != nil {
				cmdCtx.Reply(fmt.Sprintf("发送短信 / 失败\n设备    %s\n号码    %s\n通道    VoWiFi\n原因    %v", displayName, phone, sendErr))
				return
			}
		} else {
			sendErr = worker.SendSMS(phone, message)
			if sendErr != nil {
				cmdCtx.Reply(fmt.Sprintf("发送短信 / 失败\n设备    %s\n号码    %s\n通道    蜂窝\n原因    %v", displayName, phone, sendErr))
				return
			}
			_ = db.SaveSMS(worker.GetIMSI(), worker.ID, phone, message, 2, 2, time.Now())
		}

		channel := "蜂窝"
		if isVoWiFi {
			channel = "VoWiFi"
		}
		cmdCtx.Reply(fmt.Sprintf("发送短信 / 完成\n设备    %s\n号码    %s\n通道    %s\n内容    %s", displayName, phone, channel, message))
	}()

	channel := "蜂窝"
	if isVoWiFi {
		channel = "VoWiFi"
	}
	return fmt.Sprintf("发送短信 / 已受理\n设备    %s\n号码    %s\n通道    %s", displayName, phone, channel)
}

func summarizeVoWiFiReady(st runtimehost.State) string {
	notReady := make([]string, 0, 5)
	if !st.SIMReady {
		notReady = append(notReady, "SIM")
	}
	if !st.AccessReady {
		notReady = append(notReady, "Access")
	}
	if !st.TunnelReady {
		notReady = append(notReady, "Tunnel")
	}
	if !st.IMSReady {
		notReady = append(notReady, "IMS")
	}
	if !st.SMSReady {
		notReady = append(notReady, "SMS")
	}
	if len(notReady) == 0 {
		return "SIM / Access / Tunnel / IMS / SMS 全部就绪"
	}
	return strings.Join(notReady, " / ") + " 未就绪"
}

func formatVoWiFiDataplane(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "--"
	}
	return mode
}

// handleCmdStatus 处理 /status 命令
func (m *Manager) handleCmdStatus(cmdCtx CommandContext, args []string) string {
	if len(args) == 0 {
		if len(m.pool.GetAllWorkers()) == 0 {
			return "设备状态 / 空\n结果    没有可用设备"
		}
		return "设备状态 / 用法\n用法    /status [设备ID]\n提示    如需查看总览请使用 /list"
	}

	deviceID := args[0]
	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return commandFailureBlock("设备状态", deviceID, "设备未找到")
	}

	status := worker.GetDeviceStatus()
	privateIP := ""
	if worker.QMICore != nil {
		privateIP = worker.QMICore.GetPrivateIP()
	}
	activeESIMProfileName := ""
	if worker.EsimMgr != nil {
		if name, err := worker.EsimMgr.ActiveProfileName(); err == nil {
			activeESIMProfileName = name
		}
	}
	localPhone := "--"
	if phone, err := db.GetSIMCardPhoneNumberByIMSI(status.IMSI); err == nil && strings.TrimSpace(phone) != "" {
		localPhone = strings.TrimSpace(phone)
	}

	displayName := worker.ID
	if worker.Config.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", worker.Config.Name, worker.ID)
	}

	publicIP := worker.GetCachedIP()
	if strings.TrimSpace(publicIP) == "" {
		publicIP = "N/A"
	}
	if strings.TrimSpace(privateIP) == "" {
		privateIP = "N/A"
	}
	operator := strings.TrimSpace(status.Operator)
	if operator == "" {
		operator = "未知网络"
	}
	regStatus := strings.TrimSpace(status.RegStatusText)
	if regStatus == "" {
		regStatus = "--"
	}
	firmware := strings.TrimSpace(status.Firmware)
	if firmware == "" {
		firmware = "--"
	}
	apn := strings.TrimSpace(status.APN)
	if apn == "" {
		apn = "--"
	}
	lacCell := "--"
	if strings.TrimSpace(status.LAC) != "" || strings.TrimSpace(status.CellID) != "" {
		lac := strings.TrimSpace(status.LAC)
		if lac == "" {
			lac = "--"
		}
		cellID := strings.TrimSpace(status.CellID)
		if cellID == "" {
			cellID = "--"
		}
		lacCell = fmt.Sprintf("%s / %s", lac, cellID)
	}
	healthText := "异常"
	if worker.IsDeviceHealthy() {
		healthText = "正常"
	}
	isVoWiFiActive := m.pool.IsVoWiFiActive(worker.ID)
	voWiFiState, hasVoWiFiState := m.pool.GetVoWiFiRuntimeState(worker.ID)
	lastReason := "--"
	if strings.TrimSpace(voWiFiState.LastReason) != "" {
		lastReason = strings.TrimSpace(voWiFiState.LastReason)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("设备详情 / %s\n\n", displayName))
	sb.WriteString("基本信息\n")
	sb.WriteString(fmt.Sprintf("健康    %s\n", healthText))
	sb.WriteString(fmt.Sprintf("IMEI    %s\n", status.IMEI))
	sb.WriteString(fmt.Sprintf("ICCID   %s\n", status.ICCID))
	sb.WriteString(fmt.Sprintf("本机号码  %s\n", localPhone))
	sb.WriteString(fmt.Sprintf("固件    %s\n", firmware))
	if activeESIMProfileName != "" {
		sb.WriteString(fmt.Sprintf("eSIM   %s\n", activeESIMProfileName))
	}
	sb.WriteString("\n网络状态\n")
	if isVoWiFiActive {
		readySummary := summarizeVoWiFiReady(voWiFiState)
		dataplane := "--"
		if hasVoWiFiState {
			dataplane = formatVoWiFiDataplane(voWiFiState.DataplaneMode)
		}
		sb.WriteString("模式    VoWiFi\n")
		sb.WriteString(fmt.Sprintf("数据平面  %s\n", dataplane))
		sb.WriteString(fmt.Sprintf("就绪项  %s\n", readySummary))
		sb.WriteString(fmt.Sprintf("最后原因  %s\n", lastReason))
	} else {
		sb.WriteString(fmt.Sprintf("运营商  %s\n", operator))
		sb.WriteString(fmt.Sprintf("注册    %s\n", regStatus))
		sb.WriteString(fmt.Sprintf("信号    %d dBm (RSRP: %d, RSRQ: %d)\n", status.SignalDBM, status.SignalRSRP, status.SignalRSRQ))
		sb.WriteString(fmt.Sprintf("LAC/CI  %s\n", lacCell))
		sb.WriteString(fmt.Sprintf("APN     %s\n", apn))
		sb.WriteString("\n连接\n")
		sb.WriteString(fmt.Sprintf("公网 IP  %s\n", publicIP))
		sb.WriteString(fmt.Sprintf("内网 IP  %s\n", privateIP))
	}

	return sb.String()
}

// handleCmdRotate 处理 /rotate 命令
func (m *Manager) handleCmdRotate(cmdCtx CommandContext, args []string) string {
	var deviceID string
	workers := m.pool.GetAllWorkers()

	if len(args) == 0 {
		if len(workers) == 1 {
			deviceID = workers[0].ID
		} else {
			return commandUsageBlock("切换公网 IP", "/rotate [设备ID]", "/rotate ec20_1")
		}
	} else {
		deviceID = args[0]
	}

	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return fmt.Sprintf("切换公网 IP / 失败\n设备    %s\n原因    设备未找到", deviceID)
	}

	go func() {
		oldIP, newIP, err := worker.Rotate()
		if err != nil {
			cmdCtx.Reply(fmt.Sprintf("切换公网 IP / 失败\n设备    %s\n旧 IP   %s\n原因    %v", deviceID, oldIP, err))
			return
		}
		cmdCtx.Reply(fmt.Sprintf("切换公网 IP / 完成\n设备    %s\n旧 IP   %s\n新 IP   %s", deviceID, oldIP, newIP))
	}()

	return fmt.Sprintf("切换公网 IP / 已受理\n设备    %s", deviceID)
}

// handleCmdList 处理 /list 命令
func (m *Manager) handleCmdList(cmdCtx CommandContext, args []string) string {
	workers := m.pool.GetAllWorkers()
	if len(workers) == 0 {
		return commandEmptyBlock("设备列表", "没有可用设备")
	}

	var sb strings.Builder
	sb.WriteString("设备列表\n\n")
	for _, w := range workers {
		status := w.GetDeviceStatus()
		healthy := "正常"
		if !w.IsDeviceHealthy() {
			healthy = "异常"
		}
		displayName := w.ID
		if w.Config.Name != "" {
			displayName = fmt.Sprintf("%s (%s)", w.Config.Name, w.ID)
		}

		privateIP := "N/A"
		if w.QMICore != nil {
			privateIP = strings.TrimSpace(w.QMICore.GetPrivateIP())
			if privateIP == "" {
				privateIP = "N/A"
			}
		}

		publicIP := strings.TrimSpace(w.GetCachedIP())
		if publicIP == "" {
			publicIP = "N/A"
		}

		phone := "N/A"
		opName := status.Operator
		if phoneByIMSI, err := db.GetSIMCardPhoneNumberByIMSI(status.IMSI); err == nil && strings.TrimSpace(phoneByIMSI) != "" {
			phone = strings.TrimSpace(phoneByIMSI)
		}
		if status.ICCID != "" {
			var sim db.SIMCard
			if err := db.DB.Where("iccid = ?", status.ICCID).First(&sim).Error; err == nil {
				if opName == "" && sim.Operator != "" {
					opName = sim.Operator
				}
			}
		}

		if opName == "" || opName == "Unknown" {
			opName = "未知网络"
		}

		iccidShort := status.ICCID
		if len(iccidShort) > 4 {
			iccidShort = iccidShort[len(iccidShort)-4:]
		}

		netMode := status.NetworkMode
		if netMode == "" || netMode == "Unknown" {
			netMode = ""
		}

		if m.pool.IsVoWiFiActive(w.ID) {
			if netMode == "" {
				netMode = "VoWiFi"
			} else {
				netMode = "VoWiFi (" + netMode + ")"
			}
		}

		netDisplay := opName
		if netMode != "" {
			if opName == "未知网络" && strings.HasPrefix(netMode, "VoWiFi") {
				netDisplay = netMode
			} else {
				netDisplay = opName + " " + netMode
			}
		}

		sb.WriteString(fmt.Sprintf("%s / %s\n", displayName, healthy))
		sb.WriteString(fmt.Sprintf("本号   %s\n", phone))
		sb.WriteString(fmt.Sprintf("ICCID  *%s\n", iccidShort))
		if m.pool.IsVoWiFiActive(w.ID) {
			sb.WriteString(fmt.Sprintf("网络   %s\n", netDisplay))
		} else {
			sb.WriteString(fmt.Sprintf("网络   %s\n", netDisplay))
			sb.WriteString(fmt.Sprintf("信号   %d dBm\n", status.SignalDBM))
		}
		sb.WriteString(fmt.Sprintf("公网   %s\n", publicIP))
		sb.WriteString(fmt.Sprintf("内网   %s\n\n", privateIP))
	}
	return sb.String()
}

// handleCmdSMSInbox 处理 /sms 命令
func (m *Manager) handleCmdSMSInbox(cmdCtx CommandContext, args []string) string {
	limit := 5
	var deviceID string
	var smsList []db.SMS
	var err error

	if len(args) > 0 {
		deviceID = args[0]
		worker := m.pool.GetWorker(deviceID)
		if worker == nil {
			return commandFailureBlock("短信列表", deviceID, "设备未找到")
		}

		imsi := worker.GetIMSI()
		if imsi == "" {
			return fmt.Sprintf("❌ 无法获取设备 %s 的 IMSI", deviceID)
		}

		smsList, err = db.GetSMSByIMSI(imsi, limit)
	} else {
		smsList, err = db.GetRecentSMS(limit)
	}

	if err != nil {
		return fmt.Sprintf("❌ 获取短信失败: %v", err)
	}

	if len(smsList) == 0 {
		return "短信列表 / 空\n结果    暂无短信"
	}

	var sb strings.Builder
	sb.WriteString("短信列表\n\n")
	for i, sms := range smsList {
		direction := "来信"
		peer := sms.Sender
		if sms.Type == 2 {
			direction = "去信"
			peer = sms.Recipient
		}

		timeStr := sms.Timestamp.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("%d. %s / %s\n", i+1, direction, peer))
		sb.WriteString(fmt.Sprintf("内容  %s\n", sms.Content))
		sb.WriteString(fmt.Sprintf("时间  %s\n\n", timeStr))
	}

	return sb.String()
}

// handleCmdEsim 处理 /esim 命令，列出设备上的 eSIM profiles
// 命令格式: /esim [设备ID]
func (m *Manager) handleCmdEsim(cmdCtx CommandContext, args []string) string {
	var deviceID string
	workers := m.pool.GetAllWorkers()

	if len(args) == 0 {
		if len(workers) == 1 {
			deviceID = workers[0].ID
		} else {
			return commandUsageBlock("查看 eSIM", "/esim [设备ID]", "/esim ec20_1")
		}
	} else {
		deviceID = args[0]
	}

	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return commandFailureBlock("查看 eSIM", deviceID, "设备未找到")
	}
	if worker.EsimMgr == nil {
		return commandFailureBlock("查看 eSIM", deviceID, "设备不支持 eSIM")
	}

	profileGroups, err := worker.EsimMgr.GetProfiles()
	if err != nil {
		return fmt.Sprintf("❌ 获取 eSIM profiles 失败: %v", err)
	}

	if len(profileGroups) == 0 {
		return fmt.Sprintf("eSIM 列表 / 空\n设备    %s\n结果    未发现 eUICC", deviceID)
	}

	var sb strings.Builder
	displayName := deviceID
	if worker.Config.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", worker.Config.Name, deviceID)
	}
	sb.WriteString(fmt.Sprintf("eSIM 列表 / %s\n\n", displayName))

	idx := 1
	for _, group := range profileGroups {
		if len(profileGroups) > 1 {
			eidShort := group.EID
			if len(eidShort) > 12 {
				eidShort = eidShort[:6] + "..." + eidShort[len(eidShort)-6:]
			}
			sb.WriteString(fmt.Sprintf("eUICC  %s\n", eidShort))
		}
		for _, p := range group.Profiles {
			stateText := "未启用"
			if p.State == 1 {
				stateText = "已启用"
			}
			iccidShort := p.ICCID
			if len(iccidShort) > 10 {
				iccidShort = p.ICCID[:6] + "..." + p.ICCID[len(p.ICCID)-4:]
			}
			name := p.Name
			if name == "" {
				name = "未命名"
			}
			sb.WriteString(fmt.Sprintf("%s %s / %s\n", switchProfileIndexLabel(idx), stateText, name))
			sb.WriteString(fmt.Sprintf("ICCID  %s\n", iccidShort))
			if strings.TrimSpace(p.ServiceProviderName) != "" {
				sb.WriteString(fmt.Sprintf("运营商  %s\n", p.ServiceProviderName))
			}
			sb.WriteString("\n")
			idx++
		}
	}

	sb.WriteString(fmt.Sprintf("切换    /switch %s [序号或最后四位ICCID]", deviceID))
	return sb.String()
}

// handleCmdSwitch 处理 /switch 命令，切换 eSIM profile
// 命令格式: /switch <设备ID> <序号或ICCID>
func (m *Manager) handleCmdSwitch(cmdCtx CommandContext, args []string) string {
	if len(args) < 2 {
		return commandUsageBlock("切换 eSIM", "/switch [设备ID] [序号或ICCID]", "/switch ec20_1 2")
	}

	deviceID := args[0]
	target := args[1]

	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return commandFailureBlock("切换 eSIM", deviceID, "设备未找到")
	}
	if worker.EsimMgr == nil {
		return commandFailureBlock("切换 eSIM", deviceID, "设备不支持 eSIM")
	}

	// 获取当前 profiles 列表（用于序号匹配和获取 aidHex）
	profileGroups, err := worker.EsimMgr.GetProfiles()
	if err != nil {
		return fmt.Sprintf("❌ 获取 eSIM profiles 失败: %v", err)
	}

	// 将所有 profiles 展平为有序列表，同时记录每个 profile 的 aidHex
	type flatProfile struct {
		iccid  string
		name   string
		aidHex string
	}
	var allProfiles []flatProfile
	for _, group := range profileGroups {
		for _, p := range group.Profiles {
			allProfiles = append(allProfiles, flatProfile{
				iccid:  p.ICCID,
				name:   p.Name,
				aidHex: group.AIDHex,
			})
		}
	}

	if len(allProfiles) == 0 {
		return commandFailureBlock("切换 eSIM", deviceID, "没有可用的 eSIM 配置")
	}

	// 解析目标：序号或 ICCID
	var targetProfile flatProfile
	var found bool

	// 先尝试解析为序号
	if num, err := fmt.Sscanf(target, "%d", new(int)); err == nil && num == 1 {
		var idx int
		fmt.Sscanf(target, "%d", &idx)
		if idx >= 1 && idx <= len(allProfiles) {
			targetProfile = allProfiles[idx-1]
			found = true
		} else {
			return commandValidationBlock("切换 eSIM", "序号", strconv.Itoa(idx), "范围", fmt.Sprintf("1-%d", len(allProfiles)))
		}
	}

	// 未通过序号找到，尝试按 ICCID 匹配
	if !found {
		for _, p := range allProfiles {
			if p.iccid == target || strings.HasSuffix(p.iccid, target) {
				targetProfile = p
				found = true
				break
			}
		}
	}

	if !found {
		return fmt.Sprintf("❌ 未找到匹配的 profile: %s\n请使用 /esim %s 查看可用列表", target, deviceID)
	}

	profileName := targetProfile.name
	if profileName == "" {
		profileName = "未命名"
	}

	// 异步执行切卡
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := worker.EsimMgr.SwitchProfile(ctx, targetProfile.iccid, targetProfile.aidHex); err != nil {
			cmdCtx.Reply(fmt.Sprintf("❌ eSIM 切换失败 [%s]\nProfile: %s\nICCID: %s\n错误: %v",
				deviceID, profileName, targetProfile.iccid, err))
		} else {

			cmdCtx.Reply(fmt.Sprintf("✅ eSIM 切换成功 [%s]\n新 Profile: %s\nICCID: %s\n",
				deviceID, profileName, targetProfile.iccid))
		}
	}()

	return switchAcceptedBlock(deviceID, profileName)
}

// broadcast 向所有通知渠道广播消息
func (m *Manager) broadcast(text string) {
	m.broadcastWithContext(NotificationContext{
		Event:     "raw",
		Text:      text,
		Timestamp: time.Now(),
	})
}

// handleCmdCall 处理 /vocall 命令，用于发起无头模拟呼叫
// 命令格式: /vocall <设备ID> <号码> [保持秒数]
func (m *Manager) handleCmdCall(cmdCtx CommandContext, args []string) string {
	if len(args) < 2 || len(args) > 3 {
		return commandUsageBlock("发起 VoWiFi 呼叫", "/vocall [设备ID] [接收号码] [保持秒数(可选)]", "/vocall ec20_1 888 15")
	}

	deviceID := args[0]
	callee := args[1]
	holdSeconds := voicehost.DefaultSimulateCallHoldSeconds
	if len(args) == 3 {
		parsedHold, err := strconv.Atoi(strings.TrimSpace(args[2]))
		if err != nil || parsedHold <= 0 {
			return fmt.Sprintf("发起 VoWiFi 呼叫 / 参数错误\n保持秒数  %s\n要求      正整数", args[2])
		}
		if parsedHold > voicehost.MaxSimulateCallHoldSeconds {
			parsedHold = voicehost.MaxSimulateCallHoldSeconds
		}
		holdSeconds = parsedHold
	}

	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return fmt.Sprintf("发起 VoWiFi 呼叫 / 失败\n设备    %s\n原因    设备未找到", deviceID)
	}

	voiceGW := m.pool.GetVoiceGateway()
	if voiceGW == nil || voiceGW.GetAgent(deviceID) == nil {
		return fmt.Sprintf("发起 VoWiFi 呼叫 / 失败\n设备    %s\n原因    VoWiFi 未就绪", deviceID)
	}

	displayName := worker.ID
	if worker.Config.Name != "" {
		displayName = fmt.Sprintf("%s (%s)", worker.Config.Name, worker.ID)
	}
	caller := "未知"
	if worker.Modem != nil {
		if imsi := strings.TrimSpace(worker.GetIMSI()); imsi != "" {
			if phone, err := db.GetSIMCardPhoneNumberByIMSI(imsi); err == nil && strings.TrimSpace(phone) != "" {
				caller = strings.TrimSpace(phone)
			}
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		req := voicehost.SimulateCallRequest{
			Callee:      callee,
			HoldSeconds: holdSeconds,
			OnConnected: func() {
				cmdCtx.Reply(fmt.Sprintf("发起 VoWiFi 呼叫 / 已接通\n设备    %s\n主叫    %s\n被叫    %s\n保持    %d 秒", displayName, caller, callee, holdSeconds))
			},
		}

		res, err := voiceGW.SimulateCall(ctx, deviceID, req)
		if err != nil {
			cmdCtx.Reply(fmt.Sprintf("发起 VoWiFi 呼叫 / 失败\n设备    %s\n主叫    %s\n被叫    %s\n原因    %v", displayName, caller, callee, err))
			return
		}

		if res.Success {
			durationSeconds := res.DurationMs / 1000
			if res.DurationMs > 0 && durationSeconds == 0 {
				durationSeconds = 1
			}
			cmdCtx.Reply(fmt.Sprintf("发起 VoWiFi 呼叫 / 完成\n设备    %s\n主叫    %s\n被叫    %s\n时长    %d 秒", displayName, caller, callee, durationSeconds))
		} else {
			cmdCtx.Reply(fmt.Sprintf("发起 VoWiFi 呼叫 / 未接通\n设备    %s\n主叫    %s\n被叫    %s\n原因    %s", displayName, caller, callee, res.Reason))
		}
	}()

	return fmt.Sprintf("发起 VoWiFi 呼叫 / 已受理\n设备    %s\n主叫    %s\n被叫    %s\n保持    %d 秒", displayName, caller, callee, holdSeconds)
}
