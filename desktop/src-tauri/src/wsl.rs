use std::path::Path;

use crate::models::ToolStatus;
use crate::process::{clean_output, run_output};

const DEFAULT_WSL: &str = r"C:\Windows\System32\wsl.exe";
pub const DISTRO: &str = "Ubuntu-24.04";

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

pub fn run_root(args: &[&str]) -> std::io::Result<std::process::Output> {
    let mut full = vec!["-d", DISTRO, "-u", "root"];
    full.extend_from_slice(args);
    run_output(DEFAULT_WSL, &full)
}

pub fn run_root_shell(script: &str) -> std::io::Result<std::process::Output> {
    run_root(&["--exec", "/bin/sh", "-lc", script])
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
    use super::{sh_quote, windows_path_to_wsl};
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
        let p = PathBuf::from(r"\\?\F:\mySoftwareTools\vohive-plus\desktop\src-tauri\target\debug\resources\vohive\vohive-open_linux_amd64");
        assert_eq!(
            windows_path_to_wsl(&p),
            "/mnt/f/mySoftwareTools/vohive-plus/desktop/src-tauri/target/debug/resources/vohive/vohive-open_linux_amd64"
        );
    }

    #[test]
    fn quotes_single_quotes_for_shell() {
        assert_eq!(sh_quote("/tmp/a'b"), "'/tmp/a'\\''b'");
    }
}
