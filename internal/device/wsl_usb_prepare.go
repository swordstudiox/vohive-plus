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
	SysfsWrite           func(string, string) error
	Sleep                func(time.Duration)
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

	modules := []string{"usbserial", "option", "qmi_wwan", "cdc_wdm"}
	if anyBaiwangECMTopology(roots) {
		modules = append(modules, "usbnet", "cdc_ether")
	}
	for _, module := range modules {
		if err := opts.Modprobe(ctx, module); err != nil {
			return result, fmt.Errorf("加载内核模块 %s 失败: %w", module, err)
		}
		result.Actions = append(result.Actions, "modprobe:"+module)
	}

	if action, err := ensureOptionNewID(opts.USBSerialDriversPath, baiwangVendorID, baiwangProductID, opts.SysfsWrite); err != nil {
		return result, err
	} else if action != "" {
		result.Actions = append(result.Actions, action)
	}

	for _, usbPath := range roots {
		if action, err := bindBaiwangDataInterface(opts, usbPath); err != nil {
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
	if opts.SysfsWrite == nil {
		opts.SysfsWrite = writeSysfsValue
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
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

func ensureOptionNewID(usbSerialDriversPath, vendorID, productID string, write func(string, string) error) (string, error) {
	newIDPath := filepath.Join(usbSerialDriversPath, "option1", "new_id")
	return ensureDriverNewID(newIDPath, vendorID, productID, "option-new-id", write)
}

func ensureQMIWWANNewID(usbDriversPath, vendorID, productID string, write func(string, string) error) (string, error) {
	newIDPath := filepath.Join(usbDriversPath, "qmi_wwan", "new_id")
	return ensureDriverNewID(newIDPath, vendorID, productID, "qmi-wwan-new-id", write)
}

func ensureDriverNewID(newIDPath, vendorID, productID, actionPrefix string, write func(string, string) error) (string, error) {
	idLine := strings.ToLower(strings.TrimSpace(vendorID + " " + productID))
	if existing := strings.ToLower(readTextFile(newIDPath)); containsLine(existing, idLine) {
		return "", nil
	}
	if err := write(newIDPath, idLine); err != nil {
		if isAlreadyBoundSysfsError(err) {
			return "", nil
		}
		return "", fmt.Errorf("绑定驱动 VID/PID %s 到 %s 失败: %w", idLine, filepath.Base(filepath.Dir(newIDPath)), err)
	}
	return actionPrefix + ":" + strings.ReplaceAll(idLine, " ", ":"), nil
}

func bindBaiwangDataInterface(opts WSLUSBPrepareOptions, usbPath string) ([]string, error) {
	if isBaiwangECMTopology(usbPath) {
		return bindBaiwangECMInterface(opts, usbPath)
	}
	return bindBaiwangQMIInterface(opts, usbPath)
}

func bindBaiwangQMIInterface(opts WSLUSBPrepareOptions, usbPath string) ([]string, error) {
	usbName := filepath.Base(usbPath)
	qmiInterface := filepath.Join(usbPath, usbName+":1.4")
	interfaceName := filepath.Base(qmiInterface)
	driver := readUSBInterfaceDriver(qmiInterface)
	if driver == "qmi_wwan" {
		return nil, nil
	}

	actions := make([]string, 0, 3)
	if action, err := ensureQMIWWANNewID(opts.USBDriversPath, baiwangVendorID, baiwangProductID, opts.SysfsWrite); err != nil {
		return actions, err
	} else if action != "" {
		actions = append(actions, action)
	}
	if driver != "" {
		if err := opts.SysfsWrite(filepath.Join(opts.USBDriversPath, driver, "unbind"), interfaceName); err != nil && !isNotBoundSysfsError(err) {
			return actions, fmt.Errorf("释放 interface %s 的 %s 驱动失败: %w", interfaceName, driver, err)
		}
		actions = append(actions, "unbind:"+driver+":"+interfaceName)
	}
	if err := bindQMIInterfaceWithRetry(opts, qmiInterface, interfaceName); err != nil {
		return actions, fmt.Errorf("绑定 interface %s 到 qmi_wwan 失败: %w", interfaceName, err)
	}
	actions = append(actions, "bind:qmi_wwan:"+interfaceName)
	return actions, nil
}

func bindBaiwangECMInterface(opts WSLUSBPrepareOptions, usbPath string) ([]string, error) {
	usbName := filepath.Base(usbPath)
	controlInterface := filepath.Join(usbPath, usbName+":1.4")
	dataInterface := filepath.Join(usbPath, usbName+":1.5")
	controlName := filepath.Base(controlInterface)
	if readUSBInterfaceDriver(controlInterface) == "cdc_ether" {
		return nil, nil
	}

	actions := make([]string, 0, 3)
	for _, ifPath := range []string{controlInterface, dataInterface} {
		interfaceName := filepath.Base(ifPath)
		driver := readUSBInterfaceDriver(ifPath)
		if driver == "" || driver == "cdc_ether" {
			continue
		}
		if err := opts.SysfsWrite(filepath.Join(opts.USBDriversPath, driver, "unbind"), interfaceName); err != nil && !isNotBoundSysfsError(err) {
			return actions, fmt.Errorf("释放 ECM interface %s 的 %s 驱动失败: %w", interfaceName, driver, err)
		}
		actions = append(actions, "unbind:"+driver+":"+interfaceName)
	}
	if err := bindECMInterfaceWithRetry(opts, controlInterface, controlName); err != nil {
		return actions, fmt.Errorf("绑定 interface %s 到 cdc_ether 失败: %w", controlName, err)
	}
	actions = append(actions, "bind:cdc_ether:"+controlName)
	return actions, nil
}

func bindQMIInterfaceWithRetry(opts WSLUSBPrepareOptions, qmiInterface, interfaceName string) error {
	bindPath := filepath.Join(opts.USBDriversPath, "qmi_wwan", "bind")
	return bindInterfaceWithRetry(opts, bindPath, qmiInterface, interfaceName, "qmi_wwan")
}

func bindECMInterfaceWithRetry(opts WSLUSBPrepareOptions, ecmInterface, interfaceName string) error {
	bindPath := filepath.Join(opts.USBDriversPath, "cdc_ether", "bind")
	return bindInterfaceWithRetry(opts, bindPath, ecmInterface, interfaceName, "cdc_ether")
}

func bindInterfaceWithRetry(opts WSLUSBPrepareOptions, bindPath, interfacePath, interfaceName, expectedDriver string) error {
	waitUntil := time.Now().Add(1200 * time.Millisecond)
	var lastErr error

	for {
		if readUSBInterfaceDriver(interfacePath) == expectedDriver {
			return nil
		}
		err := opts.SysfsWrite(bindPath, interfaceName)
		if err == nil || isAlreadyBoundSysfsError(err) {
			return nil
		}
		lastErr = err
		if !isNotBoundSysfsError(err) || time.Now().After(waitUntil) {
			break
		}
		if readUSBInterfaceDriver(interfacePath) == expectedDriver {
			return nil
		}
		opts.Sleep(100 * time.Millisecond)
	}

	if readUSBInterfaceDriver(interfacePath) == expectedDriver {
		return nil
	}
	return lastErr
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
		if dev.DriverName == "cdc_ether" &&
			strings.TrimSpace(dev.NetInterface) != "" &&
			len(dev.ATPorts) > 0 {
			return true
		}
		if strings.TrimSpace(dev.ControlPath) != "" &&
			strings.TrimSpace(dev.NetInterface) != "" &&
			len(dev.ATPorts) > 0 {
			return true
		}
	}
	return false
}

func isBaiwangECMTopology(usbPath string) bool {
	usbName := filepath.Base(usbPath)
	controlInterface := filepath.Join(usbPath, usbName+":1.4")
	dataInterface := filepath.Join(usbPath, usbName+":1.5")
	return readUSBInterfaceClass(controlInterface) == "02" &&
		readUSBInterfaceSubClass(controlInterface) == "06" &&
		readUSBInterfaceClass(dataInterface) == "0a"
}

func anyBaiwangECMTopology(roots []string) bool {
	for _, usbPath := range roots {
		if isBaiwangECMTopology(usbPath) {
			return true
		}
	}
	return false
}

func readUSBInterfaceClass(ifPath string) string {
	return strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(ifPath, "bInterfaceClass"))))
}

func readUSBInterfaceSubClass(ifPath string) string {
	return strings.ToLower(strings.TrimSpace(readTextFile(filepath.Join(ifPath, "bInterfaceSubClass"))))
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
