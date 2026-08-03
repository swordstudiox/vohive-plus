mod commands;
mod health;
mod logs;
mod models;
mod process;
mod usbipd;
mod wsl;

use std::process::Child;
use std::sync::Mutex;

pub struct AppState {
    backend: Mutex<Option<Child>>,
    wsl_keepalive: Mutex<Option<Child>>,
    logs: logs::RingLog,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            backend: Mutex::new(None),
            wsl_keepalive: Mutex::new(None),
            logs: logs::RingLog::new(400),
        }
    }
}

pub fn run() {
    tauri::Builder::default()
        .manage(AppState::default())
        .invoke_handler(tauri::generate_handler![
            commands::detect,
            commands::status,
            commands::start_wsl,
            commands::attach_usb,
            commands::prepare_usb,
            commands::start_backend,
            commands::stop_backend,
            commands::logs,
            commands::open_web
        ])
        .run(tauri::generate_context!())
        .expect("failed to run VoHive Plus desktop shell");
}
