use std::process::{Command, Output};

#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x08000000;

pub fn hidden_command(program: &str) -> Command {
    let mut cmd = Command::new(program);
    #[cfg(windows)]
    cmd.creation_flags(CREATE_NO_WINDOW);
    cmd
}

pub fn run_output(program: &str, args: &[&str]) -> std::io::Result<Output> {
    hidden_command(program).args(args).output()
}

pub fn clean_output(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes)
        .replace('\0', "")
        .replace("\r\n", "\n")
        .trim()
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::clean_output;

    #[test]
    fn clean_output_removes_nul_bytes_from_wsl_errors() {
        let raw = b"W\0s\0l\0/\0S\0e\0r\0v\0i\0c\0e\0/\0E\0_\0A\0C\0C\0E\0S\0S\0D\0E\0N\0I\0E\0D\0\r\0\n\0";
        assert_eq!(clean_output(raw), "Wsl/Service/E_ACCESSDENIED");
    }
}
