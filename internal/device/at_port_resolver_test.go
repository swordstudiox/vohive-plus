package device

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestOrderedATPortCandidatesPrefersHintWithinDevicePorts(t *testing.T) {
	got := orderedATPortCandidates("/dev/ttyUSB3", []string{"/dev/ttyUSB2", "/dev/ttyUSB3"})
	want := []string{"/dev/ttyUSB3", "/dev/ttyUSB2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderedATPortCandidates()=%v want=%v", got, want)
	}
}

func TestOrderedATPortCandidatesIgnoresHintOutsideDevicePorts(t *testing.T) {
	got := orderedATPortCandidates("/dev/ttyUSB6", []string{"/dev/ttyUSB2", "/dev/ttyUSB3"})
	want := []string{"/dev/ttyUSB2", "/dev/ttyUSB3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderedATPortCandidates()=%v want=%v", got, want)
	}
}

func TestOrderedATPortCandidatesPrioritizesTTYUSBOverTTYACMWithoutHint(t *testing.T) {
	got := orderedATPortCandidates("", []string{"/dev/ttyACM0", "/dev/ttyUSB6", "/dev/ttyACM1"})
	want := []string{"/dev/ttyUSB6", "/dev/ttyACM0", "/dev/ttyACM1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderedATPortCandidates()=%v want=%v", got, want)
	}
}

func TestResolveATPortForDeviceScopesProbingToOwnPorts(t *testing.T) {
	orig := probeIMEICachedFn
	defer func() { probeIMEICachedFn = orig }()

	var probed []string
	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		probed = append(probed, atPort)
		if atPort == "/dev/ttyUSB6" {
			return "imei-b", nil
		}
		return "", fmt.Errorf("no imei")
	}

	atPort, imei := ResolveATPortForDevice("/dev/ttyUSB3", []string{"/dev/ttyUSB2", "/dev/ttyUSB3"}, 50*time.Millisecond)
	if atPort != "" || imei != "" {
		t.Fatalf("ResolveATPortForDevice()=(%q,%q) want empty", atPort, imei)
	}

	wantProbed := []string{"/dev/ttyUSB3", "/dev/ttyUSB2"}
	if !reflect.DeepEqual(probed, wantProbed) {
		t.Fatalf("probed=%v want=%v", probed, wantProbed)
	}
}

func TestResolveATPortForDeviceSelectsFirstSuccessfulOwnPort(t *testing.T) {
	orig := probeIMEICachedFn
	defer func() { probeIMEICachedFn = orig }()

	var probed []string
	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		probed = append(probed, atPort)
		if atPort == "/dev/ttyUSB6" {
			return "imei-b", nil
		}
		return "", fmt.Errorf("no imei")
	}

	atPort, imei := ResolveATPortForDevice("/dev/ttyUSB4", []string{"/dev/ttyUSB6", "/dev/ttyUSB7"}, 50*time.Millisecond)
	if atPort != "/dev/ttyUSB6" || imei != "imei-b" {
		t.Fatalf("ResolveATPortForDevice()=(%q,%q) want=(%q,%q)", atPort, imei, "/dev/ttyUSB6", "imei-b")
	}

	wantProbed := []string{"/dev/ttyUSB6"}
	if !reflect.DeepEqual(probed, wantProbed) {
		t.Fatalf("probed=%v want=%v", probed, wantProbed)
	}
}

func TestResolveATPortForDeviceFallsBackToCandidateWhenNoATPortsList(t *testing.T) {
	orig := probeIMEICachedFn
	defer func() { probeIMEICachedFn = orig }()

	var probed []string
	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		probed = append(probed, atPort)
		if atPort == "/dev/ttyUSB9" {
			return "imei-x", nil
		}
		return "", fmt.Errorf("no imei")
	}

	atPort, imei := ResolveATPortForDevice("/dev/ttyUSB9", nil, 50*time.Millisecond)
	if atPort != "/dev/ttyUSB9" || imei != "imei-x" {
		t.Fatalf("ResolveATPortForDevice()=(%q,%q) want=(%q,%q)", atPort, imei, "/dev/ttyUSB9", "imei-x")
	}
	if !reflect.DeepEqual(probed, []string{"/dev/ttyUSB9"}) {
		t.Fatalf("probed=%v want=%v", probed, []string{"/dev/ttyUSB9"})
	}
}

func TestResolveATPortForDeviceBoundsSlowProbePerPort(t *testing.T) {
	orig := probeIMEICachedFn
	defer func() { probeIMEICachedFn = orig }()

	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		if atPort == "/dev/ttyUSB2" {
			time.Sleep(200 * time.Millisecond)
			return "", fmt.Errorf("slow port")
		}
		return "imei-b", nil
	}

	start := time.Now()
	atPort, imei := ResolveATPortForDevice("", []string{"/dev/ttyUSB2", "/dev/ttyUSB3"}, 30*time.Millisecond)
	elapsed := time.Since(start)

	if atPort != "/dev/ttyUSB3" || imei != "imei-b" {
		t.Fatalf("ResolveATPortForDevice()=(%q,%q) want=(%q,%q)", atPort, imei, "/dev/ttyUSB3", "imei-b")
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("ResolveATPortForDevice() elapsed=%s, slow first port should be bounded by per-port timeout", elapsed)
	}
}
