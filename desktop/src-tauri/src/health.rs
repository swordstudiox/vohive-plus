use std::io::{Read, Write};
use std::net::TcpStream;
use std::time::Duration;

use crate::models::HealthStatus;

pub const WEB_URL: &str = "http://127.0.0.1:7575/";

pub fn check_health() -> HealthStatus {
    match TcpStream::connect_timeout(
        &"127.0.0.1:7575".parse().expect("valid socket"),
        Duration::from_millis(800),
    ) {
        Ok(mut stream) => {
            let _ = stream.set_read_timeout(Some(Duration::from_millis(800)));
            let req = b"GET /ping HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n";
            if let Err(err) = stream.write_all(req) {
                return health(false, format!("健康检查写入失败: {err}"));
            }
            let mut buf = String::new();
            if let Err(err) = stream.read_to_string(&mut buf) {
                return health(false, format!("健康检查读取失败: {err}"));
            }
            if buf.contains("200") {
                health(true, "VoHive 后端正常".to_string())
            } else {
                health(false, "端口 7575 有响应，但不像 VoHive /ping".to_string())
            }
        }
        Err(err) => health(false, format!("未监听: {err}")),
    }
}

fn health(ok: bool, message: String) -> HealthStatus {
    HealthStatus {
        ok,
        url: WEB_URL.to_string(),
        message,
    }
}
