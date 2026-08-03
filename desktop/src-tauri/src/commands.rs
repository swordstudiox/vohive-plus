use std::io::{BufRead, BufReader};
use std::path::Path;
use std::process::Stdio;
use std::thread;
use std::time::Duration;

use tauri::{AppHandle, Manager, State};

use crate::health::{check_health, WEB_URL};
use crate::logs::RingLog;
use crate::models::{ActionResult, BackendStatus, RuntimeStatus, UsbDevice};
use crate::process::{clean_output, hidden_command, run_output};
use crate::{usbipd, wsl, AppState};

#[tauri::command]
pub fn detect(state: State<'_, AppState>) -> RuntimeStatus {
    build_status(&state)
}

#[tauri::command]
pub fn status(state: State<'_, AppState>) -> RuntimeStatus {
    build_status(&state)
}

#[tauri::command]
pub fn attach_usb(state: State<'_, AppState>) -> ActionResult {
    let usbipd_status = usbipd::detect_usbipd();
    let Some(path) = usbipd_status.path.clone() else {
        return action(false, "未找到 usbipd-win", None, None);
    };
    let devices = usbipd::list_devices(&path);
    let Some(target) = devices.iter().find(|d| d.is_target) else {
        return action(false, "未发现 2ca3:4006 Baiwang", None, None);
    };

    match usb_attach_step(target) {
        UsbAttachStep::AlreadyAttached => {
            return action(true, "USB 已连接到 WSL", Some(build_status(&state)), None);
        }
        UsbAttachStep::BindThenAttach => {
            match run_output(&path, &["bind", "--busid", &target.busid]) {
                Ok(out) if out.status.success() => {}
                Ok(out) => {
                    let msg = clean_output(&out.stderr);
                    return action(
                        false,
                        format!("usbipd bind 失败: {msg}"),
                        None,
                        Some(format!("\"{path}\" bind --busid {}", target.busid)),
                    );
                }
                Err(err) => {
                    return action(
                        false,
                        format!("usbipd bind 执行失败: {err}"),
                        None,
                        Some(format!("\"{path}\" bind --busid {}", target.busid)),
                    );
                }
            }
        }
        UsbAttachStep::AttachOnly => {}
    }

    let attach = run_output(&path, &["attach", "--wsl", "--busid", &target.busid]);
    match attach {
        Ok(out) if out.status.success() => {
            action(true, "USB 已连接到 WSL", Some(build_status(&state)), None)
        }
        Ok(out) => action(
            false,
            format!("usbipd attach 失败: {}", clean_output(&out.stderr)),
            None,
            Some(format!("\"{path}\" attach --wsl --busid {}", target.busid)),
        ),
        Err(err) => action(false, err.to_string(), None, None),
    }
}

#[tauri::command]
pub fn prepare_usb(state: State<'_, AppState>) -> ActionResult {
    match wsl::run_root(&["--exec", "/opt/vohive/bin/vohive-usb-prepare.sh"]) {
        Ok(out) if out.status.success() => {
            state.logs.push(clean_output(&out.stdout));
            action(true, "WSL USB 准备完成", Some(build_status(&state)), None)
        }
        Ok(out) => action(
            false,
            format!("WSL USB 准备失败: {}", clean_output(&out.stderr)),
            None,
            None,
        ),
        Err(err) => action(false, err.to_string(), None, None),
    }
}

#[tauri::command]
pub fn start_backend(app: AppHandle, state: State<'_, AppState>) -> ActionResult {
    if check_health().ok {
        state.logs.push("后端健康检查正常，复用已有 WSL 进程");
        return action(true, "后端已在运行", Some(build_status(&state)), None);
    }

    if let Err(err) = install_or_import(&app, &state) {
        return action(false, err, None, None);
    }
    let mut guard = state.backend.lock().expect("backend mutex poisoned");
    if guard.is_some() {
        return action(true, "后端已在运行", Some(build_status(&state)), None);
    }

    let mut cmd = hidden_command(r"C:\Windows\System32\wsl.exe");
    cmd.args([
        "-d",
        wsl::DISTRO,
        "-u",
        "root",
        "--cd",
        "/opt/vohive",
        "--exec",
        "/opt/vohive/bin/vohive",
        "-c",
        "/opt/vohive/config/config.yaml",
    ]);
    cmd.stdout(Stdio::piped()).stderr(Stdio::piped());
    match cmd.spawn() {
        Ok(mut child) => {
            attach_reader(child.stdout.take(), "stdout", &state);
            attach_reader(child.stderr.take(), "stderr", &state);
            let pid = child.id();
            state.logs.push(format!("已启动 WSL 后端 pid={pid}"));
            *guard = Some(child);
            drop(guard);
            action(true, "后端启动中", Some(build_status(&state)), None)
        }
        Err(err) => action(false, err.to_string(), None, None),
    }
}

#[tauri::command]
pub fn stop_backend(state: State<'_, AppState>) -> ActionResult {
    let stop_result = wsl::run_root_shell_timeout(backend_stop_script(), Duration::from_secs(8));

    let mut guard = state.backend.lock().expect("backend mutex poisoned");
    if let Some(mut child) = guard.take() {
        let _ = child.kill();
        let _ = child.try_wait();
        state.logs.push("已释放桌面壳持有的 WSL 子进程");
    }
    drop(guard);

    match stop_result {
        Ok(out) if out.status.success() => {
            let msg = clean_output(&out.stdout);
            if !msg.is_empty() {
                state.logs.push(msg);
            }
            action(true, "后端已停止", Some(build_status(&state)), None)
        }
        Ok(out) => action(
            false,
            format!("停止 WSL 后端失败: {}", clean_output(&out.stderr)),
            Some(build_status(&state)),
            None,
        ),
        Err(err) => action(
            false,
            format!("停止 WSL 后端失败: {err}"),
            Some(build_status(&state)),
            None,
        ),
    }
}

#[tauri::command]
pub fn logs(state: State<'_, AppState>) -> Vec<String> {
    state.logs.snapshot()
}

#[tauri::command]
pub fn open_web() -> ActionResult {
    match hidden_command("cmd")
        .args(["/C", "start", "", WEB_URL])
        .spawn()
    {
        Ok(_) => action(true, "已打开 Web UI", None, None),
        Err(err) => action(false, err.to_string(), None, None),
    }
}

fn build_status(state: &State<'_, AppState>) -> RuntimeStatus {
    let wsl = wsl::detect_wsl();
    let usbipd = usbipd::detect_usbipd();
    let devices = usbipd
        .path
        .as_deref()
        .map(usbipd::list_devices)
        .unwrap_or_default();
    let backend = backend_status(state);
    RuntimeStatus {
        route: "wsl2".to_string(),
        wsl,
        usbipd,
        devices,
        backend,
        health: check_health(),
    }
}

fn backend_status(state: &State<'_, AppState>) -> BackendStatus {
    let mut guard = state.backend.lock().expect("backend mutex poisoned");
    if let Some(child) = guard.as_mut() {
        match child.try_wait() {
            Ok(Some(status)) => {
                let message = format!("后端已退出: {status}");
                state.logs.push(message.clone());
                *guard = None;
                BackendStatus {
                    running: false,
                    pid: None,
                    message: Some(message),
                }
            }
            Ok(None) => BackendStatus {
                running: true,
                pid: Some(child.id()),
                message: None,
            },
            Err(err) => {
                let message = format!("读取后端状态失败: {err}");
                state.logs.push(message.clone());
                *guard = None;
                BackendStatus {
                    running: false,
                    pid: None,
                    message: Some(message),
                }
            }
        }
    } else {
        BackendStatus {
            running: false,
            pid: None,
            message: None,
        }
    }
}

fn install_or_import(app: &AppHandle, state: &State<'_, AppState>) -> Result<(), String> {
    let resource_dir = app
        .path()
        .resource_dir()
        .map_err(|err| format!("读取资源目录失败: {err}"))?;
    let bin = resource_dir.join("resources/vohive/vohive-open_linux_amd64");
    let cfg = resource_dir.join("resources/vohive/config.example.yaml");
    let script = resource_dir.join("resources/vohive/vohive-usb-prepare.sh");
    validate_vohive_resources(&bin, &cfg, &script)?;
    let bin_wsl = wsl::sh_quote(&wsl::windows_path_to_wsl(&bin));
    let cfg_wsl = wsl::sh_quote(&wsl::windows_path_to_wsl(&cfg));
    let script_wsl = wsl::sh_quote(&wsl::windows_path_to_wsl(&script));
    let deploy = format!(
        "mkdir -p /opt/vohive/bin /opt/vohive/config /opt/vohive/data /opt/vohive/logs && \
         cp {bin_wsl} /opt/vohive/bin/vohive && \
         cp {script_wsl} /opt/vohive/bin/vohive-usb-prepare.sh && \
         if [ ! -f /opt/vohive/config/config.yaml ]; then cp {cfg_wsl} /opt/vohive/config/config.yaml; fi && \
         chmod +x /opt/vohive/bin/vohive /opt/vohive/bin/vohive-usb-prepare.sh"
    );
    match wsl::run_root_shell(&deploy) {
        Ok(out) if out.status.success() => {
            state.logs.push("已部署 VoHive 资源到 WSL /opt/vohive");
            Ok(())
        }
        Ok(out) => Err(format!("部署 WSL 资源失败: {}", clean_output(&out.stderr))),
        Err(err) => Err(err.to_string()),
    }
}

#[derive(Debug, PartialEq, Eq)]
enum UsbAttachStep {
    AlreadyAttached,
    BindThenAttach,
    AttachOnly,
}

fn usb_attach_step(target: &UsbDevice) -> UsbAttachStep {
    match target.state.as_str() {
        "Attached" => UsbAttachStep::AlreadyAttached,
        "Not shared" => UsbAttachStep::BindThenAttach,
        _ => UsbAttachStep::AttachOnly,
    }
}

fn validate_vohive_resources(bin: &Path, cfg: &Path, script: &Path) -> Result<(), String> {
    let missing = [
        ("vohive-open_linux_amd64", bin),
        ("config.example.yaml", cfg),
        ("vohive-usb-prepare.sh", script),
    ]
    .into_iter()
    .filter_map(|(name, path)| (!path.exists()).then_some(name))
    .collect::<Vec<_>>();

    if missing.is_empty() {
        Ok(())
    } else {
        Err(format!("桌面壳资源不完整，缺少: {}", missing.join(", ")))
    }
}

fn attach_reader(
    pipe: Option<impl std::io::Read + Send + 'static>,
    label: &'static str,
    state: &State<'_, AppState>,
) {
    let Some(pipe) = pipe else { return };
    let logs: RingLog = state.logs.clone();
    thread::spawn(move || {
        let reader = BufReader::new(pipe);
        for line in reader.lines().map_while(Result::ok) {
            logs.push(format!("[backend {label}] {line}"));
        }
    });
}

fn action(
    ok: bool,
    message: impl Into<String>,
    status: Option<RuntimeStatus>,
    suggested_admin_command: Option<String>,
) -> ActionResult {
    ActionResult {
        ok,
        message: message.into(),
        status,
        suggested_admin_command,
    }
}

fn backend_stop_script() -> &'static str {
    r#"pids=$(pgrep -f '^/opt/vohive/bin/vohive( |$)' || true)
if [ -z "$pids" ]; then
  echo no-process
  exit 0
fi
kill $pids || true
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if ! pgrep -f '^/opt/vohive/bin/vohive( |$)' >/dev/null; then
    echo stopped
    exit 0
  fi
  sleep 0.2
done
pids=$(pgrep -f '^/opt/vohive/bin/vohive( |$)' || true)
if [ -n "$pids" ]; then
  kill -KILL $pids || true
fi
echo killed"#
}

#[cfg(test)]
mod tests {
    use super::{backend_stop_script, usb_attach_step, validate_vohive_resources, UsbAttachStep};
    use crate::models::UsbDevice;
    use std::fs;

    #[test]
    fn backend_stop_script_targets_only_opt_vohive_processes() {
        let script = backend_stop_script();
        assert!(script.contains("pgrep -f '^/opt/vohive/bin/vohive( |$)'"));
        assert!(!script.contains("pkill -f"));
    }

    #[test]
    fn usb_attach_step_is_idempotent_for_attached_target() {
        let target = UsbDevice {
            busid: "2-1".to_string(),
            vid_pid: "2ca3:4006".to_string(),
            device: "Baiwang".to_string(),
            state: "Attached".to_string(),
            is_target: true,
        };

        assert_eq!(usb_attach_step(&target), UsbAttachStep::AlreadyAttached);
    }

    #[test]
    fn validate_vohive_resources_rejects_missing_files() {
        let dir =
            std::env::temp_dir().join(format!("vohive-plus-resource-test-{}", std::process::id()));
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();
        let bin = dir.join("vohive-open_linux_amd64");
        let cfg = dir.join("config.example.yaml");
        let script = dir.join("vohive-usb-prepare.sh");

        let err = validate_vohive_resources(&bin, &cfg, &script)
            .expect_err("missing resources must fail");

        let _ = fs::remove_dir_all(&dir);
        assert!(err.contains("桌面壳资源不完整"));
        assert!(err.contains("vohive-open_linux_amd64"));
        assert!(err.contains("config.example.yaml"));
        assert!(err.contains("vohive-usb-prepare.sh"));
    }
}
