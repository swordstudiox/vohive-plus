package device

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// CompatibleModem 描述可接管的 modem（QMI + 非QMI）。
type CompatibleModem struct {
	ControlPath    string
	NetInterface   string
	USBPath        string
	IMEI           string
	VendorID       uint16
	ProductID      uint16
	DriverName     string
	ATPorts        []string
	ATPort         string
	AudioDevice    string
	Mode           string
	TransportType  string
	NetworkCapable bool
}

var discoverFallbackModemsFn = discoverFallbackModems

// DiscoveryKey 用于去重 (USBPath + ATPort)。
func (m CompatibleModem) DiscoveryKey() string {
	return strings.TrimSpace(m.USBPath) + "|" + strings.TrimSpace(m.ATPort)
}

// DiscoverCompatibleModems 发现 QMI 与非QMI设备。
// 目前已革新为纯静态、纯无感树形目录遍历！严禁任何向 /dev 进行 Open 和写 AT 操作，以防底层硬件发生热启竞态死锁。
func DiscoverCompatibleModems() ([]CompatibleModem, error) {
	return discoverFallbackModems()
}

// DiscoverCompatibleModemsFromQMI 基于已发现的 QMI 设备结果聚合兼容发现。
// 该入口用于复用本地静态扫描结果，避免重复遍历 sysfs。
func DiscoverCompatibleModemsFromQMI(qmiList []QMIDevice) ([]CompatibleModem, error) {
	out := make([]CompatibleModem, 0)
	seen := make(map[string]struct{})
	qmiUSBPaths := make(map[string]struct{})

	for _, d := range qmiList {
		m := compatibleModemFromQMIStaticDevice(d)
		// 如果 AT 端口为空但有 QMI 控制路径，允许纯 QMI 设备通过（IMEI 通过 QMI 探测）
		if m.ATPort == "" && strings.TrimSpace(m.ControlPath) == "" {
			continue
		}
		if usb := strings.TrimSpace(m.USBPath); usb != "" {
			qmiUSBPaths[usb] = struct{}{}
		}
		k := m.DiscoveryKey()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, m)
	}

	fallback, err := discoverFallbackModemsFn()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	for _, m := range fallback {
		// 如果该 USB 设备已经通过 QMI 主路径识别到，则忽略 fallback，避免切模后出现“残留重复项”。
		if usb := strings.TrimSpace(m.USBPath); usb != "" {
			if _, ok := qmiUSBPaths[usb]; ok {
				continue
			}
		}
		k := m.DiscoveryKey()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, m)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("未发现调制解调器")
	}
	return out, nil
}

func discoverFallbackModems() ([]CompatibleModem, error) {
	out := make([]CompatibleModem, 0)
	if wwanList, err := discoverWWANQMIDevices(); err == nil {
		for _, d := range wwanList {
			out = append(out, compatibleModemFromQMIStaticDevice(d))
		}
	}

	entries, err := os.ReadDir("/sys/bus/usb/devices")
	if err != nil {
		if len(out) > 0 {
			return out, nil
		}
		return nil, fmt.Errorf("读取 USB 设备失败: %w", err)
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "usb") {
			continue
		}
		usbPath := filepath.Join("/sys/bus/usb/devices", name)
		m, ok := discoverFallbackOne(usbPath)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func compatibleModemFromQMIStaticDevice(d QMIDevice) CompatibleModem {
	mode := classifyMode(strings.TrimSpace(d.ControlPath), strings.TrimSpace(d.DriverName))
	return CompatibleModem{
		ControlPath:    strings.TrimSpace(d.ControlPath),
		NetInterface:   strings.TrimSpace(d.NetInterface),
		USBPath:        strings.TrimSpace(d.USBPath),
		VendorID:       d.VendorID,
		ProductID:      d.ProductID,
		DriverName:     strings.TrimSpace(d.DriverName),
		ATPorts:        dedupSortedNonEmpty(d.ATPorts),
		ATPort:         strings.TrimSpace(d.ATPort),
		AudioDevice:    strings.TrimSpace(d.AudioDevice),
		Mode:           mode,
		TransportType:  mode,
		NetworkCapable: mode == "qmi" || mode == "mbim",
	}
}

func discoverFallbackOne(usbPath string) (CompatibleModem, bool) {
	scanUSBPath := resolveUSBPathForScan(usbPath)

	vid := readHexFile16(filepath.Join(scanUSBPath, "idVendor"))
	pid := readHexFile16(filepath.Join(scanUSBPath, "idProduct"))
	if capability, ok := detectQMIUSBCapability(scanUSBPath); ok {
		atPorts := findATPortsInUSBPath(scanUSBPath)
		atPort, imei := selectBestATPort(atPorts)
		mode := classifyMode(capability.ControlPath, capability.DriverName)
		return CompatibleModem{
			ControlPath:    capability.ControlPath,
			NetInterface:   capability.NetInterface,
			USBPath:        usbPath,
			IMEI:           imei,
			VendorID:       vid,
			ProductID:      pid,
			DriverName:     capability.DriverName,
			ATPorts:        atPorts,
			ATPort:         atPort,
			AudioDevice:    "",
			Mode:           mode,
			TransportType:  mode,
			NetworkCapable: mode == "qmi" || mode == "mbim",
		}, true
	}
	if capability, ok := detectMBIMUSBCapability(scanUSBPath); ok {
		atPorts := findATPortsInUSBPath(scanUSBPath)
		atPort, imei := selectBestATPort(atPorts)
		mode := classifyMode(capability.ControlPath, capability.DriverName)
		return CompatibleModem{
			ControlPath:    capability.ControlPath,
			NetInterface:   capability.NetInterface,
			USBPath:        usbPath,
			IMEI:           imei,
			VendorID:       vid,
			ProductID:      pid,
			DriverName:     capability.DriverName,
			ATPorts:        atPorts,
			ATPort:         atPort,
			AudioDevice:    "",
			Mode:           mode,
			TransportType:  mode,
			NetworkCapable: mode == "qmi" || mode == "mbim",
		}, true
	}

	if vid != 0x2c7c && vid != 0x05c6 {
		return CompatibleModem{}, false
	}

	iface, driver := findNetInterfaceAndDriver(scanUSBPath)
	atPorts := findATPortsInUSBPath(scanUSBPath)
	atPort, imei := selectBestATPort(atPorts)
	if atPort == "" {
		return CompatibleModem{}, false
	}

	controlPath := findCDCWDMInUSBPath(scanUSBPath)
	mode := classifyMode(controlPath, driver)

	return CompatibleModem{
		ControlPath:    controlPath,
		NetInterface:   iface,
		USBPath:        usbPath,
		IMEI:           imei,
		VendorID:       vid,
		ProductID:      pid,
		DriverName:     driver,
		ATPorts:        atPorts,
		ATPort:         atPort,
		AudioDevice:    "",
		Mode:           mode,
		TransportType:  mode,
		NetworkCapable: mode == "qmi" || mode == "mbim",
	}, true
}

func classifyMode(controlPath, driver string) string {
	c := strings.ToLower(filepath.Base(strings.TrimSpace(controlPath)))
	d := strings.ToLower(strings.TrimSpace(driver))
	switch {
	case strings.Contains(d, "mbim"), strings.Contains(c, "mbim"):
		return "mbim"
	case strings.Contains(d, "qmi"), strings.Contains(d, "gobinet"), strings.Contains(d, "qcqmi"), strings.Contains(c, "qmi"):
		return "qmi"
	case strings.Contains(d, "rndis"):
		return "rndis"
	case strings.Contains(d, "ncm"):
		return "ncm"
	case strings.Contains(d, "ecm"), strings.Contains(d, "ether"):
		return "ecm"
	case strings.TrimSpace(controlPath) != "":
		// 仅有 cdc-wdm 但驱动未知时，不默认判定为 QMI，避免把 MBIM 误判。
		return "unknown"
	default:
		return "unknown"
	}
}

func findNetInterfaceAndDriver(usbPath string) (string, string) {
	usbName := filepath.Base(usbPath)
	pattern := filepath.Join(usbPath, usbName+":1.*")
	ifaces, _ := filepath.Glob(pattern)
	sortUSBInterfacePaths(ifaces)

	for _, ifPath := range ifaces {
		netPattern := filepath.Join(ifPath, "net", "*")
		nets, _ := filepath.Glob(netPattern)
		if len(nets) == 0 {
			continue
		}
		netIf := filepath.Base(nets[0])
		driver := readDriverName(ifPath)
		return netIf, driver
	}
	return "", ""
}

func readDriverName(ifPath string) string {
	target, err := os.Readlink(filepath.Join(ifPath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func findCDCWDMInUSBPath(usbPath string) string {
	return findCDCWDMInUSB(usbPath)
}

func resolveUSBPathForScan(usbPath string) string {
	p := strings.TrimSpace(usbPath)
	if p == "" {
		return p
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil || strings.TrimSpace(resolved) == "" {
		return p
	}
	return resolved
}

func findATPortsInUSBPath(usbPath string) []string {
	ports := make([]string, 0)
	for _, ttyPattern := range []string{"ttyUSB*", "ttyACM*"} {
		patterns := []string{
			filepath.Join(usbPath, "*", ttyPattern),
			filepath.Join(usbPath, "*", "tty", ttyPattern),
		}
		for _, pattern := range patterns {
			matches, _ := filepath.Glob(pattern)
			for _, match := range matches {
				ports = append(ports, filepath.Join("/dev", filepath.Base(match)))
			}
		}
	}
	return sortATPortCandidates(ports)
}

func selectBestATPort(atPorts []string) (bestPort, imei string) {
	if len(atPorts) == 0 {
		return "", ""
	}
	ports := sortATPortCandidates(atPorts)

	// 斩断万恶之源：绝对不允许发任何哪怕是 1mm 秒的串口实际探测与读写。
	// 直接根据命名顺序经验盲抽最高优可用口即可，以此杜绝系统硬件占用锁死！
	return ports[0], ""
}

func sortATPortCandidates(atPorts []string) []string {
	out := dedupSortedNonEmpty(atPorts)
	sort.SliceStable(out, func(i, j int) bool {
		pi := atPortPriority(out[i])
		pj := atPortPriority(out[j])
		if pi != pj {
			return pi < pj
		}
		return out[i] < out[j]
	})
	return out
}

func atPortPriority(port string) int {
	base := filepath.Base(strings.TrimSpace(port))
	if strings.HasPrefix(base, "ttyACM") {
		n, err := strconv.Atoi(strings.TrimPrefix(base, "ttyACM"))
		if err != nil {
			return 1900
		}
		return 1000 + n
	}
	if !strings.HasPrefix(base, "ttyUSB") {
		return 2000
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "ttyUSB"))
	if err != nil {
		return 900
	}
	// 经验值：2~5 更可能是可用 AT 口，其次高位口，再次低位口(0/1 常是诊断/NMEA)。
	switch {
	case n >= 2 && n <= 5:
		return n - 2
	case n > 5:
		return 20 + n
	default:
		return 200 + n
	}
}

func dedupSortedNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.TrimSpace(v)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func readHexFile16(path string) uint16 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 16, 16)
	if err != nil {
		return 0
	}
	return uint16(v)
}

// ProbeIMEIViaQMI 通过 QMI DMS.GetDeviceSerialNumbers 探测设备 IMEI
// 用于纯 QMI 设备（无 AT 端口）的设备发现
func ProbeIMEIViaQMI(controlPath string) (string, error) {
	clientOptions, ok := qmicore.DiscoveryClientOptionsForControlDevice(controlPath)
	if !ok {
		return "", fmt.Errorf("QMI control path %s is already held by a non-proxy process", strings.TrimSpace(controlPath))
	}
	return ProbeIMEIViaQMIWithOptions(controlPath, clientOptions)
}

func ProbeIMEIViaQMIWithOptions(controlPath string, clientOptions qmi.ClientOptions) (string, error) {
	controlPath = strings.TrimSpace(controlPath)
	if controlPath == "" {
		return "", fmt.Errorf("QMI control path is empty")
	}

	openCtx, openCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer openCancel()

	client, err := qmi.NewClientWithOptions(openCtx, controlPath, clientOptions)
	if err != nil {
		return "", fmt.Errorf("打开 QMI 设备 %s 失败: %w", controlPath, err)
	}
	defer client.Close()

	dms, err := qmi.NewDMSService(client)
	if err != nil {
		return "", fmt.Errorf("初始化 DMS service 失败: %w", err)
	}
	defer dms.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	info, err := dms.GetDeviceSerialNumbers(ctx)
	if err != nil {
		return "", fmt.Errorf("QMI DMS 获取 IMEI 失败: %w", err)
	}

	imei := strings.TrimSpace(info.IMEI)
	if imei == "" {
		return "", fmt.Errorf("QMI DMS 返回的 IMEI 为空")
	}

	logger.Debug("QMI IMEI 探测成功", "control_path", controlPath, "imei", imei)
	return imei, nil
}
