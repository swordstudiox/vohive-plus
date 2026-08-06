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
            if is_vohive_ping_response(&buf) {
                health(true, "VoHive 后端正常".to_string())
            } else {
                health(false, "端口 7575 有响应，但不像 VoHive /ping".to_string())
            }
        }
        Err(err) => health(false, format!("未监听: {err}")),
    }
}

fn is_vohive_ping_response(response: &str) -> bool {
    let mut parts = response.splitn(2, "\r\n\r\n");
    let headers = parts.next().unwrap_or_default();
    let body = parts.next().unwrap_or_default().trim();
    let status_line = headers.lines().next().unwrap_or_default();
    let status_code = status_line.split_whitespace().nth(1).unwrap_or_default();
    if status_code != "200" {
        return false;
    }
    if body == "pong" {
        return true;
    }
    serde_json::from_str::<serde_json::Value>(body)
        .ok()
        .and_then(|value| {
            value
                .get("message")
                .and_then(|message| message.as_str().map(|s| s == "pong"))
        })
        .unwrap_or(false)
}

fn health(ok: bool, message: String) -> HealthStatus {
    HealthStatus {
        ok,
        url: WEB_URL.to_string(),
        message,
    }
}

#[cfg(test)]
mod tests {
    use super::is_vohive_ping_response;

    #[test]
    fn accepts_real_vohive_ping_response() {
        let response =
            "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"message\":\"pong\"}";

        assert!(is_vohive_ping_response(response));
    }

    #[test]
    fn rejects_unrelated_http_200_response() {
        let response = "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nhello";

        assert!(!is_vohive_ping_response(response));
    }

    #[test]
    fn rejects_non_200_response_even_when_body_mentions_200() {
        let response = "HTTP/1.1 503 Service Unavailable\r\n\r\nretry after 200ms";

        assert!(!is_vohive_ping_response(response));
    }
}
