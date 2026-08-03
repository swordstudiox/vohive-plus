use std::path::Path;

use crate::models::{ToolStatus, UsbDevice};
use crate::process::{clean_output, run_output};

const DEFAULT_USBIPD: &str = r"C:\Program Files\usbipd-win\usbipd.exe";

pub fn detect_usbipd() -> ToolStatus {
    let path = if Path::new(DEFAULT_USBIPD).exists() {
        DEFAULT_USBIPD.to_string()
    } else {
        "usbipd.exe".to_string()
    };

    match run_output(&path, &["--version"]) {
        Ok(out) if out.status.success() => ToolStatus {
            available: true,
            path: Some(path),
            version: Some(clean_output(&out.stdout)),
            message: None,
        },
        Ok(out) => ToolStatus {
            available: false,
            path: Some(path),
            version: None,
            message: Some(clean_output(&out.stderr)),
        },
        Err(err) => ToolStatus {
            available: false,
            path: None,
            version: None,
            message: Some(err.to_string()),
        },
    }
}

pub fn list_devices(usbipd_path: &str) -> Vec<UsbDevice> {
    match run_output(usbipd_path, &["list"]) {
        Ok(out) => parse_usbipd_list(&format!(
            "{}\n{}",
            clean_output(&out.stdout),
            clean_output(&out.stderr)
        )),
        Err(_) => Vec::new(),
    }
}

pub fn parse_usbipd_list(output: &str) -> Vec<UsbDevice> {
    let mut in_connected = false;
    let mut out = Vec::new();
    for raw in output.lines() {
        let line = raw.trim();
        if line.starts_with("Connected:") {
            in_connected = true;
            continue;
        }
        if line.starts_with("Persisted:") {
            break;
        }
        if !in_connected
            || line.is_empty()
            || line.starts_with("BUSID")
            || line.starts_with("usbipd:")
        {
            continue;
        }
        let mut parts = line.split_whitespace();
        let Some(busid) = parts.next() else { continue };
        let Some(vid_pid) = parts.next() else {
            continue;
        };
        if !vid_pid.contains(':') {
            continue;
        }
        let rest = line
            .split_once(vid_pid)
            .map(|(_, r)| r.trim())
            .unwrap_or_default();
        let (device, state) = split_device_state(rest);
        let is_target = vid_pid.eq_ignore_ascii_case("2ca3:4006")
            || device.to_ascii_lowercase().contains("baiwang");
        out.push(UsbDevice {
            busid: busid.to_string(),
            vid_pid: vid_pid.to_string(),
            device,
            state,
            is_target,
        });
    }
    out
}

fn split_device_state(rest: &str) -> (String, String) {
    for state in ["Not shared", "Attached", "Shared"] {
        if let Some(device) = rest.strip_suffix(state) {
            return (device.trim().to_string(), state.to_string());
        }
    }
    (rest.to_string(), "Unknown".to_string())
}

#[cfg(test)]
mod tests {
    use super::parse_usbipd_list;

    #[test]
    fn parse_baiwang_device_and_ignore_warning() {
        let sample = r#"
Connected:
BUSID  VID:PID    DEVICE                                                        STATE
2-1    2ca3:4006  Baiwang                                                       Attached
2-10   8087:0033  Intel(R) Wireless Bluetooth(R)                                 Not shared
usbipd: warning: A reboot may be required.

Persisted:
GUID                                  DEVICE
"#;
        let devices = parse_usbipd_list(sample);
        assert_eq!(devices.len(), 2);
        assert_eq!(devices[0].busid, "2-1");
        assert_eq!(devices[0].state, "Attached");
        assert!(devices[0].is_target);
        assert_eq!(devices[1].state, "Not shared");
    }
}
