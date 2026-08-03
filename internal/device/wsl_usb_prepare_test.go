package device

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrepareWSLUSBPreparesBaiwangQMIInterface(t *testing.T) {
	root := t.TempDir()
	usbDevices := filepath.Join(root, "sys", "bus", "usb", "devices")
	usbDrivers := filepath.Join(root, "sys", "bus", "usb", "drivers")
	serialDrivers := filepath.Join(root, "sys", "bus", "usb-serial", "drivers")
	devRoot := filepath.Join(root, "dev")

	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "idVendor"), "2ca3\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "idProduct"), "4006\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.2", "tty", "ttyUSB2"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "driver"), "option\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "usbmisc", "cdc-wdm0"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "net", "wwan0"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDrivers, "option", "unbind"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDrivers, "qmi_wwan", "bind"), "")
	writeUSBPrepareFile(t, filepath.Join(serialDrivers, "option1", "new_id"), "")
	writeUSBPrepareFile(t, filepath.Join(devRoot, "ttyUSB2"), "")
	writeUSBPrepareFile(t, filepath.Join(devRoot, "cdc-wdm0"), "")

	var modules []string
	got, err := PrepareWSLUSB(context.Background(), WSLUSBPrepareOptions{
		USBDevicesPath:       usbDevices,
		USBDriversPath:       usbDrivers,
		USBSerialDriversPath: serialDrivers,
		DevPath:              devRoot,
		WaitTimeout:          time.Millisecond,
		PollInterval:         time.Millisecond,
		Modprobe: func(_ context.Context, module string) error {
			modules = append(modules, module)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("PrepareWSLUSB() error = %v", err)
	}
	if !got.SupportedDeviceFound || !got.Prepared {
		t.Fatalf("SupportedDeviceFound=%v Prepared=%v, want true true (%+v)", got.SupportedDeviceFound, got.Prepared, got)
	}
	if !reflect.DeepEqual(modules, []string{"usbserial", "option", "qmi_wwan", "cdc_wdm"}) {
		t.Fatalf("modprobe modules=%v", modules)
	}
	if got.Devices[0].ControlPath != "/dev/cdc-wdm0" || got.Devices[0].NetInterface != "wwan0" {
		t.Fatalf("unexpected device summary: %+v", got.Devices[0])
	}
	if got.Devices[0].ATPorts[0] != "/dev/ttyUSB2" {
		t.Fatalf("ATPorts=%v, want ttyUSB2", got.Devices[0].ATPorts)
	}
	if strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(serialDrivers, "option1", "new_id"))) != "2ca3 4006" {
		t.Fatalf("new_id not written correctly")
	}
	if strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(usbDrivers, "option", "unbind"))) != "1-1:1.4" {
		t.Fatalf("option unbind not written correctly")
	}
	if strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(usbDrivers, "qmi_wwan", "bind"))) != "1-1:1.4" {
		t.Fatalf("qmi_wwan bind not written correctly")
	}
}

func TestPrepareWSLUSBIsIdempotentWhenAlreadyPrepared(t *testing.T) {
	root := t.TempDir()
	usbDevices := filepath.Join(root, "sys", "bus", "usb", "devices")
	usbDrivers := filepath.Join(root, "sys", "bus", "usb", "drivers")
	serialDrivers := filepath.Join(root, "sys", "bus", "usb-serial", "drivers")
	devRoot := filepath.Join(root, "dev")

	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "idVendor"), "2ca3\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "idProduct"), "4006\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.2", "tty", "ttyUSB2"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "driver"), "qmi_wwan\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "usbmisc", "cdc-wdm0"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-1", "1-1:1.4", "net", "wwan0"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDrivers, "option", "unbind"), "")
	writeUSBPrepareFile(t, filepath.Join(usbDrivers, "qmi_wwan", "bind"), "")
	writeUSBPrepareFile(t, filepath.Join(serialDrivers, "option1", "new_id"), "2ca3 4006\n")
	writeUSBPrepareFile(t, filepath.Join(devRoot, "ttyUSB2"), "")
	writeUSBPrepareFile(t, filepath.Join(devRoot, "cdc-wdm0"), "")

	opts := WSLUSBPrepareOptions{
		USBDevicesPath:       usbDevices,
		USBDriversPath:       usbDrivers,
		USBSerialDriversPath: serialDrivers,
		DevPath:              devRoot,
		WaitTimeout:          time.Millisecond,
		PollInterval:         time.Millisecond,
		Modprobe:             func(context.Context, string) error { return nil },
	}

	for i := 0; i < 2; i++ {
		got, err := PrepareWSLUSB(context.Background(), opts)
		if err != nil {
			t.Fatalf("PrepareWSLUSB() run %d error = %v", i+1, err)
		}
		if !got.Prepared {
			t.Fatalf("PrepareWSLUSB() run %d Prepared=false, got %+v", i+1, got)
		}
	}
	if got := strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(serialDrivers, "option1", "new_id"))); got != "2ca3 4006" {
		t.Fatalf("new_id content=%q, want single id", got)
	}
	if got := strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(usbDrivers, "option", "unbind"))); got != "" {
		t.Fatalf("option unbind=%q, want empty for already qmi", got)
	}
	if got := strings.TrimSpace(readUSBPrepareFile(t, filepath.Join(usbDrivers, "qmi_wwan", "bind"))); got != "" {
		t.Fatalf("qmi bind=%q, want empty for already qmi", got)
	}
}

func TestPrepareWSLUSBReportsNoSupportedDevice(t *testing.T) {
	root := t.TempDir()
	usbDevices := filepath.Join(root, "sys", "bus", "usb", "devices")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-2", "idVendor"), "05c6\n")
	writeUSBPrepareFile(t, filepath.Join(usbDevices, "1-2", "idProduct"), "9003\n")

	got, err := PrepareWSLUSB(context.Background(), WSLUSBPrepareOptions{
		USBDevicesPath: usbDevices,
		Modprobe:       func(context.Context, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("PrepareWSLUSB() error = %v", err)
	}
	if got.SupportedDeviceFound || got.Prepared || len(got.Devices) != 0 {
		t.Fatalf("unexpected supported device result: %+v", got)
	}
}

func writeUSBPrepareFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readUSBPrepareFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
