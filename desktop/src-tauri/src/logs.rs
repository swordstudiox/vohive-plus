use std::collections::VecDeque;
use std::sync::{Arc, Mutex};

#[derive(Clone)]
pub struct RingLog {
    capacity: usize,
    lines: Arc<Mutex<VecDeque<String>>>,
}

impl RingLog {
    pub fn new(capacity: usize) -> Self {
        Self {
            capacity,
            lines: Arc::new(Mutex::new(VecDeque::with_capacity(capacity))),
        }
    }

    pub fn push(&self, line: impl Into<String>) {
        let mut lines = self.lines.lock().expect("ring log mutex poisoned");
        if lines.len() >= self.capacity {
            lines.pop_front();
        }
        lines.push_back(line.into());
    }

    pub fn snapshot(&self) -> Vec<String> {
        self.lines
            .lock()
            .expect("ring log mutex poisoned")
            .iter()
            .cloned()
            .collect()
    }
}
