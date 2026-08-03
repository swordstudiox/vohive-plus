package device

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	baiwangVendorID  = "2ca3"
	baiwangProductID = "4006"
)

type WSLUSBPrepareOptions struct {
	USBDevicesPath       string
	USBDriversPath       string
	USBSerialDriversPath string
	DevPath              string
	WaitTimeout          time.Duration
	PollInterval         time.Duration
	Modprobe             func(context.Context, string) error
}

type WSLUSBPrepareResult struct {
	SupportedDeviceFound bool                   `json:"supported_device_found"`
	Prepared             bool                   `json:"prepared"`
	Message              string                 `json:"message"`
	Actions              []string               `json:"actions,omitempty"`
	Devices              []WSLUSBPreparedDevice `json:"devices,omitempty"`
}

type WSLUSBPreparedDevice struct {
	USBPath      string   `json:"usb_path"`
	VendorID     string   `json:"vendor_id"`
	ProductID    string   `json:"product_id"`
	ControlPath  string   `json:"control_path,omitempty"`
	NetInterface string   `json:"net_interface,omitempty"`
	ATPorts      []string `json:"at_ports,omitempty"`
	QMIInterface string   `json:"qmi_interface,omitempty"`
	DriverName   string   `json:"driver_name,omitempty"`
}

func PrepareWSLUSB(ctx context.Context, opts WSLUSBPrepareOptions) (WSLUSBPrepareResult, error) {
	opts = normalizeWSLUSBPrepareOptions(opts)
	result := WSLUSBPrepareResult{}

	roots, err := findBaiwangUSBDevices(opts.USBDevicesPath)
	if err != nil {
		return result, err
	}
	if len(roots) == 0 {
		result.Message = "未发现 DJI/Baiwang 4G 模组，请确认 Windows 侧已通过 usbipd attach 到 WSL。"
		return result, nil
	}
	result.SupportedDeviceFound = true

	for _, module := range []string{"usbserial", "option", "qmi_wwan", "cdc_wdm"} {
		if err := opts.Modprobe(ctx, module); err != nil {
			return result, fmt.Errorf("加载内核模块 %s 失败: %w", module, err)
		}
		result.Actions = append(result.Actions, "modprobe:"+module)
	}

	if action, err := ensureOptionNewID(opts.USBSerialDriversPath, baiwangVendorID, baiwangProductID); err != nil {
		return result, err
	} else if action != "" {
		result.Actions = append(result.Actions, action)
	}

	for _, usbPath := range roots {
		if action, err := bindBaiwangQMIInterface(opts.USBDriversPath, usbPath); err != nil {
			return result, err
		} else {
			result.Actions = append(result.Actions, action...)
		}
	}

	waitUntil := time.Now().Add(opts.WaitTimeout)
	for {
		result.Devices = summarizePreparedBaiwangDevices(roots)
		result.Prepared = anyBaiwangDeviceReady(result.Devices)
		if result.Prepared || time.Now().After(waitUntil) {
			break
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}

	if result.Prepared {
		result.Message = "WSL USB 已准备完成。"
	} else {
		result.Message = "已识别 DJI/Baiwang 模组，但尚未生成完整 QMI/串口设备节点。"
	}
	return result, nil
}

func normalizeWSLUSBPrepareOptions(opts WSLUSBPrepareOptions) WSLUSBPrepareOptions {
	if strings.TrimSpace(opts.USBDevicesPath) == "" {
		opts.USBDevicesPath = "/sys/bus/usb/devices"
	}
	if strings.TrimSpace(opts.USBDriversPath) == "" {
		opts.USBDriversPath = "/sys/bus/usb/drivers"
	}
	if strings.TrimSpace(opts.USBSerialDriversPath) == "" {
		opts.USBSerialDriversPath = "/sys/bus/usb-serial/drivers"
	}
	if strings.TrimSpace(opts.DevPath) == "" {
		opts.DevPath = "/dev"
	}
	if opts.WaitTimeout <= 0 {
		opts.WaitTimeout = 5 * time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 100 * time.Millisecond
	}
	if opts.Modprobe == nil {
		opts.Modprobe = defaultModprobe
	}
	return opts
}

func defaultModprobe(ctx context.Context, module string) error {
	cmd := exec.CommandContext(ctx, "modprobe", module)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func findBaiwangUSBDevices(usbDevicesPath string) ([]string, error) {
	entries, err := os.ReadDir(usbDevicesPath)
	if err != nil {
		return nil, fmt.Errorf("读取 USB 设备失败: %w", err)
	}

	out := make([]string, 0)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "usb") {
			continue
		}
		usbPath := filepath.Join(usbDevicesPath, entry.Name())
		vid := strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(usbPath, "idVendor"))))
		pid := strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(usbPath, "idProduct"))))
		if vid == baiwangVendorID && pid == baiwangProductID {
			out = append(out, usbPath)
		}
	}
	sort.Strings(out)
	return out, nil
}

func ensureOptionNewID(usbSerialDriversPath, vendorID, productID string) (string, error) {
	newIDPath := filepath.Join(usbSerialDriversPath, "option1", "new_id")
	idLine := strings.ToLower(strings.TrimSpace(vendorID + " " + productID))
	if existing := strings.ToLower(readTextFile(newIDPath)); containsLine(existing, idLine) {
		return "", nil
	}
	if err := writeSysfsValue(newIDPath, idLine); err != nil {
		if isAlreadyBoundSysfsError(err) {
			return "", nil
		}
		return "", fmt.Errorf("绑定 option 驱动 VID/PID %s 失败: %w", idLine, err)
	}
	return "option-new-id:" + strings.ReplaceAll(idLine, " ", ":"), nil
}

func bindBaiwangQMIInterface(usbDriversPath, usbPath string) ([]string, error) {
	usbName := filepath.Base(usbPath)
	qmiInterface := filepath.Join(usbPath, usbName+":1.4")
	interfaceName := filepath.Base(qmiInterface)
	driver := readUSBInterfaceDriver(qmiInterface)
	if driver == "qmi_wwan" {
		return nil, nil
	}

	actions := make([]string, 0, 2)
	if driver != "" {
		if err := writeSysfsValue(filepath.Join(usbDriversPath, driver, "unbind"), interfaceName); err != nil && !isNotBoundSysfsError(err) {
			return actions, fmt.Errorf("释放 interface %s 的 %s 驱动失败: %w", interfaceName, driver, err)
		}
		actions = append(actions, "unbind:"+driver+":"+interfaceName)
	}
	if err := writeSysfsValue(filepath.Join(usbDriversPath, "qmi_wwan", "bind"), interfaceName); err != nil {
		if !isAlreadyBoundSysfsError(err) {
			return actions, fmt.Errorf("绑定 interface %s 到 qmi_wwan 失败: %w", interfaceName, err)
		}
	}
	actions = append(actions, "bind:qmi_wwan:"+interfaceName)
	return actions, nil
}

func summarizePreparedBaiwangDevices(roots []string) []WSLUSBPreparedDevice {
	out := make([]WSLUSBPreparedDevice, 0, len(roots))
	for _, usbPath := range roots {
		usbName := filepath.Base(usbPath)
		qmiInterface := filepath.Join(usbPath, usbName+":1.4")
		dev := WSLUSBPreparedDevice{
			USBPath:      usbPath,
			VendorID:     strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(usbPath, "idVendor")))),
			ProductID:    strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(usbPath, "idProduct")))),
			ControlPath:  findCDCWDMInUSB(usbPath),
			NetInterface: firstNetInterfaceInUSBInterface(qmiInterface),
			ATPorts:      findATPorts(usbPath),
			QMIInterface: qmiInterface,
			DriverName:   readUSBInterfaceDriver(qmiInterface),
		}
		out = append(out, dev)
	}
	return out
}

func anyBaiwangDeviceReady(devices []WSLUSBPreparedDevice) bool {
	for _, dev := range devices {
		if strings.TrimSpace(dev.ControlPath) != "" &&
			strings.TrimSpace(dev.NetInterface) != "" &&
			len(dev.ATPorts) > 0 {
			return true
		}
	}
	return false
}

func readUSBInterfaceDriver(ifPath string) string {
	driverPath := filepath.Join(ifPath, "driver")
	if target, err := os.Readlink(driverPath); err == nil {
		return strings.TrimSpace(filepath.Base(target))
	}
	return strings.TrimSpace(readTextFile(driverPath))
}

func readTextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func containsLine(content, line string) bool {
	line = strings.TrimSpace(line)
	for _, candidate := range strings.Split(content, "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}

func writeSysfsValue(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(value))
	return err
}

func isAlreadyBoundSysfsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return errors.Is(err, os.ErrExist) ||
		strings.Contains(msg, "file exists") ||
		strings.Contains(msg, "already") ||
		strings.Contains(msg, "busy")
}

func isNotBoundSysfsError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such device") ||
		strings.Contains(msg, "not bound") ||
		strings.Contains(msg, "invalid argument")
}
