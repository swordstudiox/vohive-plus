use serde::Serialize;

#[derive(Debug, Clone, Serialize)]
pub struct ToolStatus {
    pub available: bool,
    pub path: Option<String>,
    pub version: Option<String>,
    pub message: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct UsbDevice {
    pub busid: String,
    pub vid_pid: String,
    pub device: String,
    pub state: String,
    pub is_target: bool,
}

#[derive(Debug, Clone, Serialize)]
pub struct BackendStatus {
    pub running: bool,
    pub pid: Option<u32>,
    pub message: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct HealthStatus {
    pub ok: bool,
    pub url: String,
    pub message: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct RuntimeStatus {
    pub route: String,
    pub wsl: ToolStatus,
    pub usbipd: ToolStatus,
    pub devices: Vec<UsbDevice>,
    pub backend: BackendStatus,
    pub health: HealthStatus,
}

#[derive(Debug, Clone, Serialize)]
pub struct ActionResult {
    pub ok: bool,
    pub message: String,
    pub status: Option<RuntimeStatus>,
    pub suggested_admin_command: Option<String>,
}
