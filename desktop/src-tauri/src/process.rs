use std::io::{Error, ErrorKind};
use std::process::{Command, Output, Stdio};
use std::thread;
use std::time::{Duration, Instant};

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
    run_output_with_timeout(program, args, Duration::from_secs(60))
}

pub fn run_output_with_timeout(
    program: &str,
    args: &[&str],
    timeout: Duration,
) -> std::io::Result<Output> {
    let mut child = hidden_command(program)
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let start = Instant::now();

    loop {
        if child.try_wait()?.is_some() {
            return child.wait_with_output();
        }
        if start.elapsed() >= timeout {
            let _ = child.kill();
            let _ = child.wait();
            return Err(Error::new(
                ErrorKind::TimedOut,
                format!("{program} timed out after {}ms", timeout.as_millis()),
            ));
        }
        thread::sleep(Duration::from_millis(25));
    }
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
    use super::{clean_output, run_output_with_timeout};
    use std::time::{Duration, Instant};

    #[test]
    fn clean_output_removes_nul_bytes_from_wsl_errors() {
        let raw = b"W\0s\0l\0/\0S\0e\0r\0v\0i\0c\0e\0/\0E\0_\0A\0C\0C\0E\0S\0S\0D\0E\0N\0I\0E\0D\0\r\0\n\0";
        assert_eq!(clean_output(raw), "Wsl/Service/E_ACCESSDENIED");
    }

    #[test]
    fn run_output_with_timeout_stops_long_running_commands() {
        let start = Instant::now();
        let err = run_output_with_timeout(
            "powershell.exe",
            &["-NoProfile", "-Command", "Start-Sleep -Seconds 2"],
            Duration::from_millis(100),
        )
        .expect_err("command should time out");

        assert!(
            start.elapsed() < Duration::from_secs(2),
            "timeout should not wait for the child process to finish naturally"
        );
        assert!(err.to_string().contains("timed out"));
    }
}
