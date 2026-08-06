use std::path::Path;
use std::thread;
use std::time::Duration;
use std::time::Instant;

use crate::models::ToolStatus;
use crate::process::{clean_output, run_output, run_output_with_timeout};

const DEFAULT_WSL: &str = r"C:\Windows\System32\wsl.exe";
pub const DISTRO: &str = "Ubuntu-24.04";

pub fn executable() -> &'static str {
    DEFAULT_WSL
}

pub fn detect_wsl() -> ToolStatus {
    let path = if Path::new(DEFAULT_WSL).exists() {
        DEFAULT_WSL.to_string()
    } else {
        "wsl.exe".to_string()
    };
    match run_output(&path, &["--status"]) {
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

pub fn keepalive_args() -> Vec<&'static str> {
    vec![
        "-d",
        DISTRO,
        "--exec",
        "/bin/sh",
        "-lc",
        "while true; do sleep 3600; done",
    ]
}

pub fn terminate_args() -> [&'static str; 2] {
    ["--terminate", DISTRO]
}

pub fn is_distro_running(list_output: &str) -> bool {
    list_output
        .lines()
        .any(|line| line.contains(DISTRO) && line.contains("Running"))
}

pub fn current_distro_running() -> Result<bool, String> {
    match run_output_with_timeout(DEFAULT_WSL, &["-l", "-v"], Duration::from_secs(3)) {
        Ok(out) if out.status.success() => Ok(is_distro_running(&clean_output(&out.stdout))),
        Ok(out) => Err(clean_output(&out.stderr)),
        Err(err) => Err(err.to_string()),
    }
}

pub fn wait_until_distro_running(timeout: Duration) -> Result<(), String> {
    let start = Instant::now();
    let mut last_error = String::new();

    while start.elapsed() < timeout {
        match run_output_with_timeout(DEFAULT_WSL, &["-l", "-v"], Duration::from_secs(3)) {
            Ok(out) if out.status.success() => {
                let stdout = clean_output(&out.stdout);
                if is_distro_running(&stdout) {
                    return Ok(());
                }
                last_error = stdout;
            }
            Ok(out) => {
                last_error = clean_output(&out.stderr);
            }
            Err(err) => {
                last_error = err.to_string();
            }
        }
        thread::sleep(Duration::from_millis(150));
    }

    if last_error.is_empty() {
        Err(format!("等待 {DISTRO} 进入 Running 超时"))
    } else {
        Err(format!("等待 {DISTRO} 进入 Running 超时: {last_error}"))
    }
}

pub fn run_root(args: &[&str]) -> std::io::Result<std::process::Output> {
    run_root_timeout(args, Duration::from_secs(60))
}

pub fn run_root_timeout(args: &[&str], timeout: Duration) -> std::io::Result<std::process::Output> {
    let mut full = vec!["-d", DISTRO, "-u", "root"];
    full.extend_from_slice(args);
    run_output_with_timeout(DEFAULT_WSL, &full, timeout)
}

pub fn run_root_shell(script: &str) -> std::io::Result<std::process::Output> {
    run_root(&["--exec", "/bin/sh", "-lc", script])
}

pub fn run_root_shell_timeout(
    script: &str,
    timeout: Duration,
) -> std::io::Result<std::process::Output> {
    run_root_timeout(&["--exec", "/bin/sh", "-lc", script], timeout)
}

pub fn terminate_distro(timeout: Duration) -> std::io::Result<std::process::Output> {
    run_output_with_timeout(DEFAULT_WSL, &terminate_args(), timeout)
}

pub fn vohive_backend_pids(timeout: Duration) -> Result<Vec<u32>, String> {
    if !should_probe_backend_pids(current_distro_running()?) {
        return Ok(Vec::new());
    }

    let output = run_root_shell_timeout(
        "pgrep -f '^/opt/vohive/bin/vohive( |$)' || true",
        timeout,
    )
    .map_err(|err| err.to_string())?;

    if !output.status.success() {
        return Err(clean_output(&output.stderr));
    }

    Ok(parse_pid_lines(&clean_output(&output.stdout)))
}

fn should_probe_backend_pids(distro_running: bool) -> bool {
    distro_running
}

pub fn parse_pid_lines(output: &str) -> Vec<u32> {
    output
        .lines()
        .filter_map(|line| line.trim().parse::<u32>().ok())
        .collect()
}

pub fn windows_path_to_wsl(path: &Path) -> String {
    let mut s = path.to_string_lossy().replace('\\', "/");
    if let Some(rest) = s.strip_prefix("//?/") {
        s = rest.to_string();
    }
    let bytes = s.as_bytes();
    if bytes.len() > 2 && bytes[1] == b':' {
        let drive = (bytes[0] as char).to_ascii_lowercase();
        let rest = s[2..].trim_start_matches('/');
        return format!("/mnt/{drive}/{rest}");
    }
    s
}

pub fn sh_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

#[cfg(test)]
mod tests {
    use super::{
        is_distro_running, keepalive_args, parse_pid_lines, sh_quote, should_probe_backend_pids,
        terminate_args, windows_path_to_wsl, DISTRO,
    };
    use std::path::PathBuf;

    #[test]
    fn converts_windows_path_to_wsl_mount_path() {
        let p = PathBuf::from(r"F:\mySoftwareTools\vohive-plus\dist\vohive");
        assert_eq!(
            windows_path_to_wsl(&p),
            "/mnt/f/mySoftwareTools/vohive-plus/dist/vohive"
        );
    }

    #[test]
    fn converts_windows_verbatim_path_to_wsl_mount_path() {
        let p = PathBuf::from(
            r"\\?\F:\mySoftwareTools\vohive-plus\desktop\src-tauri\target\debug\resources\vohive\vohive-open_linux_amd64",
        );
        assert_eq!(
            windows_path_to_wsl(&p),
            "/mnt/f/mySoftwareTools/vohive-plus/desktop/src-tauri/target/debug/resources/vohive/vohive-open_linux_amd64"
        );
    }

    #[test]
    fn quotes_single_quotes_for_shell() {
        assert_eq!(sh_quote("/tmp/a'b"), "'/tmp/a'\\''b'");
    }

    #[test]
    fn keepalive_args_start_the_configured_wsl_distribution() {
        let args = keepalive_args();

        assert!(args.windows(2).any(|pair| pair == ["-d", DISTRO]));
        assert!(args.contains(&"--exec"));
        assert!(args.contains(&"while true; do sleep 3600; done"));
    }

    #[test]
    fn terminate_args_stop_the_configured_wsl_distribution() {
        assert_eq!(terminate_args(), ["--terminate", DISTRO]);
    }

    #[test]
    fn parses_pgrep_pid_lines() {
        assert_eq!(parse_pid_lines("4242\nnot-a-pid\n4343\n"), vec![4242, 4343]);
    }

    #[test]
    fn skips_backend_pid_probe_when_distro_is_stopped() {
        assert!(!should_probe_backend_pids(false));
    }

    #[test]
    fn probes_backend_pids_when_distro_is_running() {
        assert!(should_probe_backend_pids(true));
    }

    #[test]
    fn parses_running_state_from_wsl_list_verbose_output() {
        let output =
            "  NAME            STATE           VERSION\n* Ubuntu-24.04    Running         2\n";

        assert!(is_distro_running(output));
    }

    #[test]
    fn parses_stopped_state_from_wsl_list_verbose_output() {
        let output =
            "  NAME            STATE           VERSION\n* Ubuntu-24.04    Stopped         2\n";

        assert!(!is_distro_running(output));
    }
}
