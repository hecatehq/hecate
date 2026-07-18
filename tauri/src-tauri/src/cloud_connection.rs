use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;

#[derive(Debug, Clone, Serialize)]
pub struct CloudConnectionStatus {
    pub available: bool,
    pub running: bool,
    pub gateway_ready: bool,
    pub auto_start_enabled: bool,
    pub hec_path: Option<String>,
    pub base_url: Option<String>,
    pub message: String,
    pub last_exit_status: Option<String>,
}

pub struct CloudConnectionSupervisor {
    child: Mutex<Option<CloudConnectionProcess>>,
    last_exit_status: Mutex<Option<String>>,
    auto_start_enabled: Mutex<bool>,
    preferences_path: Option<PathBuf>,
}

struct CloudConnectionProcess {
    child: Child,
    base_url: String,
    hec_path: PathBuf,
}

impl Default for CloudConnectionSupervisor {
    fn default() -> Self {
        Self {
            child: Mutex::new(None),
            last_exit_status: Mutex::new(None),
            auto_start_enabled: Mutex::new(false),
            preferences_path: None,
        }
    }
}

impl CloudConnectionSupervisor {
    pub fn new(preferences_path: PathBuf) -> Self {
        let auto_start_enabled = read_preferences(&preferences_path).auto_start_enabled;
        Self {
            child: Mutex::new(None),
            last_exit_status: Mutex::new(None),
            auto_start_enabled: Mutex::new(auto_start_enabled),
            preferences_path: Some(preferences_path),
        }
    }

    pub fn status(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let mut slot = match self.child.lock() {
            Ok(slot) => slot,
            Err(_) => {
                return CloudConnectionStatus {
                    available: false,
                    running: false,
                    gateway_ready: base_url.is_some(),
                    auto_start_enabled: self.auto_start_enabled(),
                    hec_path: None,
                    base_url,
                    message: "Cloud connection state is unavailable.".to_string(),
                    last_exit_status: None,
                };
            }
        };
        if let Some(status) = self.running_status_locked(&mut slot, base_url.clone()) {
            return status;
        }
        self.idle_status(base_url)
    }

    pub fn start_if_enabled(&self, base_url: Option<String>) {
        if !self.auto_start_enabled() {
            return;
        }
        if let Err(err) = self.start_with_persistence(base_url, false) {
            log::warn!("auto-start Hecate Cloud connector failed: {err}");
        }
    }

    pub fn start(&self, base_url: Option<String>) -> Result<CloudConnectionStatus, String> {
        self.start_with_persistence(base_url, true)
    }

    fn start_with_persistence(
        &self,
        base_url: Option<String>,
        persist_auto_start: bool,
    ) -> Result<CloudConnectionStatus, String> {
        let base_url = base_url.ok_or_else(|| {
            "Hecate is still starting. Try again once the app finishes loading.".to_string()
        })?;
        let mut slot = self
            .child
            .lock()
            .map_err(|_| "Cloud connection state is unavailable.".to_string())?;
        if let Some(mut status) = self.running_status_locked(&mut slot, Some(base_url.clone())) {
            if persist_auto_start {
                self.set_auto_start_enabled(true)?;
                status.auto_start_enabled = self.auto_start_enabled();
            }
            return Ok(status);
        }
        let hec_path = resolve_hec_cli()
            .ok_or_else(|| "Install the hec CLI before connecting to Hecate Cloud.".to_string())?;
        let mut command = Command::new(&hec_path);
        command
            .arg("connect")
            .arg("--local-url")
            .arg(&base_url)
            .arg("--name")
            .arg(default_host_name())
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        log::info!(
            "starting Hecate Cloud connector hec_path={} local_url={}",
            hec_path.display(),
            base_url
        );
        if persist_auto_start {
            self.set_auto_start_enabled(true)?;
        }
        let child = match command.spawn() {
            Ok(child) => child,
            Err(err) => {
                if persist_auto_start {
                    if let Err(reset_err) = self.set_auto_start_enabled(false) {
                        log::warn!(
                            "failed to clear Hecate Cloud auto-start preference after connector spawn failure: {reset_err}"
                        );
                    }
                }
                return Err(format!("failed to start hec connect: {err}"));
            }
        };
        log::info!("Hecate Cloud connector spawned pid={}", child.id());
        if let Ok(mut last) = self.last_exit_status.lock() {
            *last = None;
        }
        let hec_path_string = hec_path.display().to_string();
        *slot = Some(CloudConnectionProcess {
            child,
            base_url: base_url.clone(),
            hec_path,
        });
        Ok(CloudConnectionStatus {
            available: true,
            running: true,
            gateway_ready: true,
            auto_start_enabled: self.auto_start_enabled(),
            hec_path: Some(hec_path_string),
            base_url: Some(base_url),
            message: "Connected to Hecate Cloud. Keep this app open for remote access.".to_string(),
            last_exit_status: None,
        })
    }

    pub fn stop(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let mut slot = match self.child.lock() {
            Ok(slot) => slot,
            Err(_) => {
                return CloudConnectionStatus {
                    available: false,
                    running: false,
                    gateway_ready: base_url.is_some(),
                    auto_start_enabled: self.auto_start_enabled(),
                    hec_path: None,
                    base_url,
                    message: "Cloud connection state is unavailable.".to_string(),
                    last_exit_status: None,
                };
            }
        };
        if let Err(err) = self.set_auto_start_enabled(false) {
            log::warn!("failed to persist Hecate Cloud auto-start preference: {err}");
        }
        if let Some(mut process) = slot.take() {
            log::info!("stopping Hecate Cloud connector pid={}", process.child.id());
            if let Err(err) = process.child.kill() {
                log::warn!("failed to stop hec connect: {err}");
            }
            if let Err(err) = process.child.wait() {
                log::warn!("failed to reap hec connect: {err}");
            }
            if let Ok(mut last) = self.last_exit_status.lock() {
                *last = Some("Stopped from the Hecate app.".to_string());
            }
        }
        self.idle_status(base_url)
    }

    pub fn kill_for_exit(&self) {
        let Ok(mut slot) = self.child.lock() else {
            return;
        };
        let Some(mut process) = slot.take() else {
            return;
        };
        log::info!(
            "stopping Hecate Cloud connector during app exit pid={}",
            process.child.id()
        );
        if let Err(err) = process.child.kill() {
            log::warn!("failed to stop hec connect during app exit: {err}");
        }
        if let Err(err) = process.child.wait() {
            log::warn!("failed to reap hec connect during app exit: {err}");
        }
    }

    fn running_status_locked(
        &self,
        slot: &mut Option<CloudConnectionProcess>,
        base_url: Option<String>,
    ) -> Option<CloudConnectionStatus> {
        let process = slot.as_mut()?;
        match process.child.try_wait() {
            Ok(None) => Some(CloudConnectionStatus {
                available: true,
                running: true,
                gateway_ready: base_url.is_some(),
                hec_path: Some(process.hec_path.display().to_string()),
                base_url: Some(process.base_url.clone()),
                auto_start_enabled: self.auto_start_enabled(),
                message: "Connected to Hecate Cloud. Keep this app open for remote access."
                    .to_string(),
                last_exit_status: None,
            }),
            Ok(Some(status)) => {
                let message = if status.success() {
                    "hec connect exited.".to_string()
                } else {
                    format!("hec connect exited with {status}.")
                };
                if let Ok(mut last) = self.last_exit_status.lock() {
                    *last = Some(message);
                }
                *slot = None;
                None
            }
            Err(err) => {
                if let Ok(mut last) = self.last_exit_status.lock() {
                    *last = Some(format!("hec connect status check failed: {err}"));
                }
                *slot = None;
                None
            }
        }
    }

    fn idle_status(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let hec_path = resolve_hec_cli();
        let last_exit_status = self
            .last_exit_status
            .lock()
            .ok()
            .and_then(|last| last.clone());
        let gateway_ready = base_url.is_some();
        let message = if hec_path.is_none() {
            "Install the hec CLI before connecting to Hecate Cloud.".to_string()
        } else if !gateway_ready {
            "Hecate is still starting. Remote access will be available after the local runtime is ready."
                .to_string()
        } else if self.auto_start_enabled() && last_exit_status.is_some() {
            "Remote access is on, but the connector stopped. Reconnect from this panel.".to_string()
        } else if last_exit_status.is_some() {
            "Disconnected from Hecate Cloud.".to_string()
        } else {
            "Ready to connect. Sign in or approve in the browser when prompted.".to_string()
        };
        CloudConnectionStatus {
            available: hec_path.is_some(),
            running: false,
            gateway_ready,
            auto_start_enabled: self.auto_start_enabled(),
            hec_path: hec_path.map(|path| path.display().to_string()),
            base_url,
            message,
            last_exit_status,
        }
    }

    fn auto_start_enabled(&self) -> bool {
        self.auto_start_enabled
            .lock()
            .map(|enabled| *enabled)
            .unwrap_or(false)
    }

    fn set_auto_start_enabled(&self, enabled: bool) -> Result<(), String> {
        if let Some(path) = &self.preferences_path {
            write_preferences(
                path,
                &CloudConnectionPreferences {
                    auto_start_enabled: enabled,
                },
            )?;
        }
        if let Ok(mut current) = self.auto_start_enabled.lock() {
            *current = enabled;
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Deserialize, Serialize)]
struct CloudConnectionPreferences {
    #[serde(default)]
    auto_start_enabled: bool,
}

impl Default for CloudConnectionPreferences {
    fn default() -> Self {
        Self {
            auto_start_enabled: false,
        }
    }
}

fn read_preferences(path: &Path) -> CloudConnectionPreferences {
    let Ok(raw) = std::fs::read_to_string(path) else {
        return CloudConnectionPreferences::default();
    };
    match serde_json::from_str(&raw) {
        Ok(preferences) => preferences,
        Err(err) => {
            log::warn!(
                "failed to read Hecate Cloud connection preferences {}: {err}",
                path.display()
            );
            CloudConnectionPreferences::default()
        }
    }
}

fn write_preferences(path: &Path, preferences: &CloudConnectionPreferences) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).map_err(|e| {
            format!(
                "failed to create Hecate Cloud connection settings directory {}: {e}",
                parent.display()
            )
        })?;
    }
    let raw = serde_json::to_vec_pretty(preferences)
        .map_err(|e| format!("failed to encode Hecate Cloud connection settings: {e}"))?;
    std::fs::write(path, raw).map_err(|e| {
        format!(
            "failed to save Hecate Cloud connection settings {}: {e}",
            path.display()
        )
    })
}

fn resolve_hec_cli() -> Option<PathBuf> {
    for key in ["HECATE_CLOUD_CLI", "HEC_CLI"] {
        if let Ok(value) = std::env::var(key) {
            let path = PathBuf::from(value);
            if path.is_file() {
                return Some(path);
            }
        }
    }

    for dir in common_cli_dirs() {
        for name in hec_binary_names() {
            let candidate = dir.join(name);
            if candidate.is_file() {
                return Some(candidate);
            }
        }
    }
    None
}

fn hec_binary_names() -> Vec<String> {
    let suffix = std::env::consts::EXE_SUFFIX;
    let mut names = vec![format!("hec{suffix}")];
    if !suffix.is_empty() {
        names.push("hec".to_string());
    }
    names
}

fn common_cli_dirs() -> Vec<PathBuf> {
    let mut dirs = Vec::new();
    if let Some(path) = std::env::var_os("PATH") {
        dirs.extend(std::env::split_paths(&path));
    }
    if let Some(home) = home_dir() {
        dirs.push(home.join(".local").join("bin"));
        dirs.push(home.join("bin"));
    }
    dirs.push(PathBuf::from("/opt/homebrew/bin"));
    dirs.push(PathBuf::from("/usr/local/bin"));
    dirs
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
}

fn default_host_name() -> String {
    for key in ["HECATE_DESKTOP_HOST_NAME", "HOSTNAME", "COMPUTERNAME"] {
        if let Ok(value) = std::env::var(key) {
            let value = value.trim();
            if !value.is_empty() {
                return value.to_string();
            }
        }
    }
    "Hecate desktop app".to_string()
}

#[cfg(test)]
mod tests {
    use super::{
        hec_binary_names, home_dir, read_preferences, write_preferences, CloudConnectionPreferences,
    };

    #[test]
    fn test_hec_binary_names_include_platform_executable_suffix() {
        let names = hec_binary_names();
        let expected = format!("hec{}", std::env::consts::EXE_SUFFIX);

        assert_eq!(names.first(), Some(&expected));
    }

    #[test]
    fn test_home_dir_uses_standard_environment_when_present() {
        if std::env::var_os("HOME").is_none() && std::env::var_os("USERPROFILE").is_none() {
            return;
        }
        assert!(home_dir().is_some());
    }

    #[test]
    fn test_connection_preferences_round_trip_auto_start_flag() {
        let path = std::env::temp_dir().join(format!(
            "hecate-cloud-connection-{}.json",
            std::process::id()
        ));
        let _ = std::fs::remove_file(&path);

        assert!(!read_preferences(&path).auto_start_enabled);
        write_preferences(
            &path,
            &CloudConnectionPreferences {
                auto_start_enabled: true,
            },
        )
        .expect("write preferences");
        assert!(read_preferences(&path).auto_start_enabled);

        let _ = std::fs::remove_file(path);
    }
}
