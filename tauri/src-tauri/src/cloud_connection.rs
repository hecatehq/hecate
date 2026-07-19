use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine;
use futures_util::{SinkExt, StreamExt};
use rand::RngCore;
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::sync::{mpsc, watch, Semaphore};
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::header::AUTHORIZATION;
use tokio_tungstenite::tungstenite::protocol::Message;

const DEFAULT_CLOUD_URL: &str = "https://console.hecatehq.com";
const KEYRING_SERVICE: &str = "sh.hecate.app.cloud";
const SESSION_CREDENTIAL: &str = "account-session";
const HOST_CREDENTIAL: &str = "desktop-host";
const LOGIN_TIMEOUT: Duration = Duration::from_secs(3 * 60);
const LOGIN_POLL_INTERVAL: Duration = Duration::from_secs(2);
const RELAY_RECONNECT_MAX: Duration = Duration::from_secs(30);
const RELAY_MAX_CONCURRENCY: usize = 16;
const RELAY_MAX_REQUEST_BODY: usize = 64 * 1024;
const RELAY_MAX_PROXY_BODY: usize = 16 * 1024 * 1024;
const RELAY_MAX_RESPONSE_BODY: usize = 16 * 1024 * 1024;

#[derive(Debug, Clone, Serialize)]
pub struct CloudConnectionStatus {
    pub available: bool,
    pub phase: String,
    pub running: bool,
    pub authorizing: bool,
    pub signed_in: bool,
    pub gateway_ready: bool,
    pub auto_start_enabled: bool,
    pub account_email: Option<String>,
    pub cloud_url: String,
    pub base_url: Option<String>,
    pub message: String,
    pub last_error: Option<String>,
}

#[derive(Clone)]
pub struct CloudConnectionSupervisor {
    inner: Arc<SupervisorInner>,
}

struct SupervisorInner {
    state: Mutex<ConnectionState>,
    preferences_path: Option<PathBuf>,
    credentials: Arc<dyn CredentialStore>,
    http: reqwest::Client,
    cloud_url: String,
}

struct ConnectionState {
    preferences: CloudConnectionPreferences,
    phase: ConnectionPhase,
    signed_in: bool,
    message: String,
    last_error: Option<String>,
    login_url: Option<String>,
    base_url: Option<String>,
    cancel: Option<watch::Sender<bool>>,
    generation: u64,
    credential_error: Option<String>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ConnectionPhase {
    Disconnected,
    Authorizing,
    Connecting,
    Connected,
    Reconnecting,
    Error,
}

impl ConnectionPhase {
    fn as_str(self) -> &'static str {
        match self {
            Self::Disconnected => "disconnected",
            Self::Authorizing => "authorizing",
            Self::Connecting => "connecting",
            Self::Connected => "connected",
            Self::Reconnecting => "reconnecting",
            Self::Error => "error",
        }
    }
}

impl Default for CloudConnectionSupervisor {
    fn default() -> Self {
        Self::with_store(
            None,
            Arc::new(MemoryCredentialStore::default()),
            DEFAULT_CLOUD_URL,
        )
    }
}

impl CloudConnectionSupervisor {
    pub fn new(preferences_path: PathBuf) -> Self {
        let cloud_url = std::env::var("HECATE_CLOUD_URL")
            .ok()
            .map(|value| value.trim().trim_end_matches('/').to_string())
            .filter(|value| !value.is_empty())
            .unwrap_or_else(|| DEFAULT_CLOUD_URL.to_string());
        Self::with_store(
            Some(preferences_path),
            Arc::new(KeyringCredentialStore),
            &cloud_url,
        )
    }

    fn with_store(
        preferences_path: Option<PathBuf>,
        credentials: Arc<dyn CredentialStore>,
        cloud_url: &str,
    ) -> Self {
        let preferences = preferences_path
            .as_deref()
            .map(read_preferences)
            .unwrap_or_default();
        let (signed_in, credential_error) = match credentials.get(SESSION_CREDENTIAL) {
            Ok(token) => (token.is_some(), None),
            Err(err) => (false, Some(err)),
        };
        let phase = if credential_error.is_some() {
            ConnectionPhase::Error
        } else {
            ConnectionPhase::Disconnected
        };
        let message = if signed_in {
            "Remote access is off.".to_string()
        } else if credential_error.is_some() {
            "Secure credential storage is unavailable.".to_string()
        } else {
            "Sign in to use this Hecate from another device.".to_string()
        };
        Self {
            inner: Arc::new(SupervisorInner {
                state: Mutex::new(ConnectionState {
                    preferences,
                    phase,
                    signed_in,
                    message,
                    last_error: credential_error.clone(),
                    login_url: None,
                    base_url: None,
                    cancel: None,
                    generation: 0,
                    credential_error,
                }),
                preferences_path,
                credentials,
                http: reqwest::Client::builder()
                    .connect_timeout(Duration::from_secs(10))
                    .build()
                    .expect("native Cloud HTTP client configuration is valid"),
                cloud_url: cloud_url.trim_end_matches('/').to_string(),
            }),
        }
    }

    pub fn status(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let Ok(mut state) = self.inner.state.lock() else {
            return unavailable_status(base_url, &self.inner.cloud_url);
        };
        if base_url.is_some() {
            state.base_url = base_url;
        }
        status_from_state(&state, &self.inner.cloud_url)
    }

    pub fn start_if_enabled(&self, base_url: Option<String>) {
        let should_start = self
            .inner
            .state
            .lock()
            .map(|state| state.preferences.auto_start_enabled)
            .unwrap_or(false);
        if !should_start {
            return;
        }
        let session_token = match self.inner.credentials.get(SESSION_CREDENTIAL) {
            Ok(Some(token)) => token,
            Ok(None) => {
                self.set_error("Sign in again to restore remote access.", None);
                return;
            }
            Err(err) => {
                self.set_error("Secure credential storage is unavailable.", Some(err));
                return;
            }
        };
        if let Err(err) = self.launch_authenticated(base_url, session_token, false) {
            self.set_error("Remote access could not start.", Some(err));
        }
    }

    pub fn start(&self, base_url: Option<String>) -> Result<CloudConnectionStatus, String> {
        let base_url = validated_local_base_url(base_url)?;
        {
            let mut state = self
                .inner
                .state
                .lock()
                .map_err(|_| "Remote access state is unavailable.".to_string())?;
            state.base_url = Some(base_url.clone());
            if matches!(
                state.phase,
                ConnectionPhase::Authorizing
                    | ConnectionPhase::Connecting
                    | ConnectionPhase::Connected
                    | ConnectionPhase::Reconnecting
            ) {
                return Ok(status_from_state(&state, &self.inner.cloud_url));
            }
        }

        match self.inner.credentials.get(SESSION_CREDENTIAL) {
            Ok(Some(token)) => {
                self.launch_authenticated(Some(base_url), token, true)?;
            }
            Ok(None) => {
                self.launch_authorization(base_url)?;
            }
            Err(err) => return Err(err),
        }
        Ok(self.status(None))
    }

    pub fn pending_login_url(&self) -> Option<String> {
        self.inner
            .state
            .lock()
            .ok()
            .and_then(|state| state.login_url.clone())
    }

    pub fn stop(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let Ok(mut state) = self.inner.state.lock() else {
            return unavailable_status(base_url, &self.inner.cloud_url);
        };
        let clear_pending_credential =
            state.phase == ConnectionPhase::Authorizing && !state.signed_in;
        cancel_current(&mut state);
        state.generation = state.generation.wrapping_add(1);
        state.preferences.auto_start_enabled = false;
        if base_url.is_some() {
            state.base_url = base_url;
        }
        state.phase = ConnectionPhase::Disconnected;
        state.login_url = None;
        state.last_error = None;
        state.message = if state.signed_in {
            "Remote access is off.".to_string()
        } else {
            "Sign in to use this Hecate from another device.".to_string()
        };
        if let Err(err) = self.persist_preferences(&state.preferences) {
            state.last_error = Some(err);
        }
        if clear_pending_credential {
            if let Err(err) = self.inner.credentials.delete(SESSION_CREDENTIAL) {
                state.last_error = Some(err);
            }
        }
        status_from_state(&state, &self.inner.cloud_url)
    }

    pub async fn sign_out(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let (session_token, host_id, org_id) = {
            let mut state = match self.inner.state.lock() {
                Ok(state) => state,
                Err(_) => return unavailable_status(base_url, &self.inner.cloud_url),
            };
            cancel_current(&mut state);
            state.generation = state.generation.wrapping_add(1);
            (
                self.inner
                    .credentials
                    .get(SESSION_CREDENTIAL)
                    .ok()
                    .flatten(),
                state.preferences.host_id.clone(),
                state.preferences.org_id.clone(),
            )
        };

        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let mut revoke_warning = None;
        if let (Some(session), Some(host), Some(org)) = (
            session_token.as_deref(),
            host_id.as_deref(),
            org_id.as_deref(),
        ) {
            if let Err(err) = client.revoke_host(session, org, host).await {
                revoke_warning = Some(format!("Could not revoke this computer: {err}"));
            }
        }
        if let Some(session) = session_token.as_deref() {
            if let Err(err) = client.revoke_session(session).await {
                revoke_warning = Some(format!("Could not revoke the account session: {err}"));
            }
        }
        let _ = self.inner.credentials.delete(SESSION_CREDENTIAL);
        let _ = self.inner.credentials.delete(HOST_CREDENTIAL);

        let mut state = match self.inner.state.lock() {
            Ok(state) => state,
            Err(_) => return unavailable_status(base_url, &self.inner.cloud_url),
        };
        state.preferences = CloudConnectionPreferences::default();
        state.signed_in = false;
        state.phase = ConnectionPhase::Disconnected;
        state.base_url = base_url.or_else(|| state.base_url.clone());
        state.login_url = None;
        state.last_error = revoke_warning;
        state.message = "Signed out of Hecate Cloud.".to_string();
        if let Err(err) = self.persist_preferences(&state.preferences) {
            state.last_error = Some(err);
        }
        status_from_state(&state, &self.inner.cloud_url)
    }

    pub fn kill_for_exit(&self) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        cancel_current(&mut state);
        state.generation = state.generation.wrapping_add(1);
        state.phase = ConnectionPhase::Disconnected;
    }

    fn launch_authorization(&self, base_url: String) -> Result<(), String> {
        let token = new_app_token()?;
        self.inner.credentials.set(SESSION_CREDENTIAL, &token)?;
        let login_url = format!("{}/desktop-login#token={token}", self.inner.cloud_url);
        let (generation, cancel_rx) = {
            let mut state = self
                .inner
                .state
                .lock()
                .map_err(|_| "Remote access state is unavailable.".to_string())?;
            cancel_current(&mut state);
            state.generation = state.generation.wrapping_add(1);
            let generation = state.generation;
            let (cancel_tx, cancel_rx) = watch::channel(false);
            state.cancel = Some(cancel_tx);
            state.preferences.auto_start_enabled = true;
            state.phase = ConnectionPhase::Authorizing;
            state.signed_in = false;
            state.base_url = Some(base_url.clone());
            state.login_url = Some(login_url);
            state.last_error = None;
            state.message = "Finish signing in in your browser.".to_string();
            self.persist_preferences(&state.preferences)?;
            (generation, cancel_rx)
        };
        let supervisor = self.clone();
        tauri::async_runtime::spawn(async move {
            supervisor
                .authorize_then_connect(generation, base_url, token, cancel_rx)
                .await;
        });
        Ok(())
    }

    fn launch_authenticated(
        &self,
        base_url: Option<String>,
        session_token: String,
        persist_auto_start: bool,
    ) -> Result<(), String> {
        let base_url = validated_local_base_url(base_url)?;
        let (generation, cancel_rx) = {
            let mut state = self
                .inner
                .state
                .lock()
                .map_err(|_| "Remote access state is unavailable.".to_string())?;
            cancel_current(&mut state);
            state.generation = state.generation.wrapping_add(1);
            let generation = state.generation;
            let (cancel_tx, cancel_rx) = watch::channel(false);
            state.cancel = Some(cancel_tx);
            if persist_auto_start {
                state.preferences.auto_start_enabled = true;
            }
            state.phase = ConnectionPhase::Connecting;
            state.signed_in = true;
            state.base_url = Some(base_url.clone());
            state.login_url = None;
            state.last_error = None;
            state.message = "Connecting this Hecate...".to_string();
            self.persist_preferences(&state.preferences)?;
            (generation, cancel_rx)
        };
        let supervisor = self.clone();
        tauri::async_runtime::spawn(async move {
            supervisor
                .connect_authenticated(generation, base_url, session_token, cancel_rx)
                .await;
        });
        Ok(())
    }

    async fn authorize_then_connect(
        &self,
        generation: u64,
        base_url: String,
        token: String,
        mut cancel_rx: watch::Receiver<bool>,
    ) {
        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let deadline = tokio::time::Instant::now() + LOGIN_TIMEOUT;
        loop {
            if *cancel_rx.borrow() {
                return;
            }
            match client.me(&token).await {
                Ok(actor) => {
                    self.apply_actor(generation, &actor);
                    self.connect_authenticated(generation, base_url, token, cancel_rx)
                        .await;
                    return;
                }
                Err(err) if err.status == Some(401) && tokio::time::Instant::now() < deadline => {}
                Err(err) if tokio::time::Instant::now() < deadline => {
                    self.update_message_if_current(
                        generation,
                        "Waiting for browser approval.",
                        Some(err.to_string()),
                    );
                }
                Err(err) => {
                    let _ = self.inner.credentials.delete(SESSION_CREDENTIAL);
                    self.set_error_if_current(
                        generation,
                        "Sign-in was not completed. Try again.",
                        Some(err.to_string()),
                    );
                    return;
                }
            }
            tokio::select! {
                _ = tokio::time::sleep(LOGIN_POLL_INTERVAL) => {}
                result = cancel_rx.changed() => {
                    if result.is_err() || *cancel_rx.borrow() { return; }
                }
            }
        }
    }

    async fn connect_authenticated(
        &self,
        generation: u64,
        base_url: String,
        session_token: String,
        mut cancel_rx: watch::Receiver<bool>,
    ) {
        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let actor = match client.me(&session_token).await {
            Ok(actor) => actor,
            Err(err) if err.status == Some(401) => {
                let _ = self.inner.credentials.delete(SESSION_CREDENTIAL);
                let _ = self.inner.credentials.delete(HOST_CREDENTIAL);
                self.clear_account_if_current(
                    generation,
                    "Your Hecate Cloud session expired. Sign in again.",
                );
                return;
            }
            Err(err) => {
                self.set_error_if_current(
                    generation,
                    "Hecate Cloud is not reachable.",
                    Some(err.to_string()),
                );
                return;
            }
        };
        self.apply_actor(generation, &actor);

        let (host_id, host_token) = match self.ensure_host(&client, &session_token, &actor).await {
            Ok(credentials) => credentials,
            Err(err) => {
                self.set_error_if_current(
                    generation,
                    "This computer could not be registered.",
                    Some(err),
                );
                return;
            }
        };

        let mut delay = Duration::from_secs(1);
        loop {
            if *cancel_rx.borrow() || !self.is_current(generation) {
                return;
            }
            self.set_phase_if_current(
                generation,
                ConnectionPhase::Connecting,
                "Connecting this Hecate...",
                None,
            );
            match run_relay(
                &client,
                &host_id,
                &host_token,
                &base_url,
                cancel_rx.clone(),
                || {
                    self.set_phase_if_current(
                        generation,
                        ConnectionPhase::Connected,
                        "Remote access is on.",
                        None,
                    );
                },
            )
            .await
            {
                Ok(()) if *cancel_rx.borrow() => return,
                Ok(()) => {
                    self.set_phase_if_current(
                        generation,
                        ConnectionPhase::Reconnecting,
                        "Connection interrupted. Reconnecting...",
                        None,
                    );
                }
                Err(err) => {
                    self.set_phase_if_current(
                        generation,
                        ConnectionPhase::Reconnecting,
                        "Connection interrupted. Reconnecting...",
                        Some(err),
                    );
                }
            }
            tokio::select! {
                _ = tokio::time::sleep(delay) => {}
                result = cancel_rx.changed() => {
                    if result.is_err() || *cancel_rx.borrow() { return; }
                }
            }
            delay = std::cmp::min(delay.saturating_mul(2), RELAY_RECONNECT_MAX);
        }
    }

    async fn ensure_host(
        &self,
        client: &CloudClient,
        session_token: &str,
        actor: &CloudActor,
    ) -> Result<(String, String), String> {
        let existing_id = self
            .inner
            .state
            .lock()
            .ok()
            .and_then(|state| state.preferences.host_id.clone());
        let existing_token = self.inner.credentials.get(HOST_CREDENTIAL)?;
        if let (Some(id), Some(token)) = (existing_id, existing_token) {
            return Ok((id, token));
        }

        let created = client
            .create_host(session_token, &actor.org_id, &default_host_name())
            .await
            .map_err(|err| err.to_string())?;
        if created.host.id.trim().is_empty() || created.host_token.trim().is_empty() {
            return Err("Cloud did not return desktop host credentials.".to_string());
        }
        self.inner
            .credentials
            .set(HOST_CREDENTIAL, &created.host_token)?;
        let mut state = self
            .inner
            .state
            .lock()
            .map_err(|_| "Remote access state is unavailable.".to_string())?;
        state.preferences.host_id = Some(created.host.id.clone());
        self.persist_preferences(&state.preferences)?;
        Ok((created.host.id, created.host_token))
    }

    fn apply_actor(&self, generation: u64, actor: &CloudActor) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        state.signed_in = true;
        state.preferences.account_email = Some(actor.email.clone());
        state.preferences.org_id = Some(actor.org_id.clone());
        state.login_url = None;
        state.last_error = None;
        if let Err(err) = self.persist_preferences(&state.preferences) {
            state.last_error = Some(err);
        }
    }

    fn clear_account_if_current(&self, generation: u64, message: &str) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        state.signed_in = false;
        state.phase = ConnectionPhase::Disconnected;
        state.preferences = CloudConnectionPreferences::default();
        state.message = message.to_string();
        state.login_url = None;
        state.last_error = None;
        let _ = self.persist_preferences(&state.preferences);
    }

    fn set_phase_if_current(
        &self,
        generation: u64,
        phase: ConnectionPhase,
        message: &str,
        error: Option<String>,
    ) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        state.phase = phase;
        state.message = message.to_string();
        state.last_error = error;
    }

    fn update_message_if_current(&self, generation: u64, message: &str, error: Option<String>) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        state.message = message.to_string();
        state.last_error = error;
    }

    fn set_error_if_current(&self, generation: u64, message: &str, error: Option<String>) {
        self.set_phase_if_current(generation, ConnectionPhase::Error, message, error);
    }

    fn set_error(&self, message: &str, error: Option<String>) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        state.phase = ConnectionPhase::Error;
        state.message = message.to_string();
        state.last_error = error;
    }

    fn is_current(&self, generation: u64) -> bool {
        self.inner
            .state
            .lock()
            .map(|state| state.generation == generation)
            .unwrap_or(false)
    }

    fn persist_preferences(&self, preferences: &CloudConnectionPreferences) -> Result<(), String> {
        match self.inner.preferences_path.as_deref() {
            Some(path) => write_preferences(path, preferences),
            None => Ok(()),
        }
    }
}

fn status_from_state(state: &ConnectionState, cloud_url: &str) -> CloudConnectionStatus {
    CloudConnectionStatus {
        available: state.credential_error.is_none(),
        phase: state.phase.as_str().to_string(),
        running: state.phase == ConnectionPhase::Connected,
        authorizing: state.phase == ConnectionPhase::Authorizing,
        signed_in: state.signed_in,
        gateway_ready: state.base_url.is_some(),
        auto_start_enabled: state.preferences.auto_start_enabled,
        account_email: state.preferences.account_email.clone(),
        cloud_url: cloud_url.to_string(),
        base_url: state.base_url.clone(),
        message: state.message.clone(),
        last_error: state.last_error.clone(),
    }
}

fn unavailable_status(base_url: Option<String>, cloud_url: &str) -> CloudConnectionStatus {
    CloudConnectionStatus {
        available: false,
        phase: "error".to_string(),
        running: false,
        authorizing: false,
        signed_in: false,
        gateway_ready: base_url.is_some(),
        auto_start_enabled: false,
        account_email: None,
        cloud_url: cloud_url.to_string(),
        base_url,
        message: "Remote access state is unavailable.".to_string(),
        last_error: None,
    }
}

fn cancel_current(state: &mut ConnectionState) {
    if let Some(cancel) = state.cancel.take() {
        let _ = cancel.send(true);
    }
}

#[derive(Debug, Clone, Deserialize, Serialize, Default)]
struct CloudConnectionPreferences {
    #[serde(default)]
    auto_start_enabled: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    account_email: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    org_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    host_id: Option<String>,
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
        std::fs::create_dir_all(parent).map_err(|err| {
            format!(
                "failed to create remote access settings directory {}: {err}",
                parent.display()
            )
        })?;
    }
    let raw = serde_json::to_vec_pretty(preferences)
        .map_err(|err| format!("failed to encode remote access settings: {err}"))?;
    std::fs::write(path, raw).map_err(|err| {
        format!(
            "failed to save remote access settings {}: {err}",
            path.display()
        )
    })
}

trait CredentialStore: Send + Sync {
    fn get(&self, name: &str) -> Result<Option<String>, String>;
    fn set(&self, name: &str, value: &str) -> Result<(), String>;
    fn delete(&self, name: &str) -> Result<(), String>;
}

struct KeyringCredentialStore;

impl KeyringCredentialStore {
    fn entry(name: &str) -> Result<keyring::Entry, String> {
        keyring::Entry::new(KEYRING_SERVICE, name)
            .map_err(|err| format!("secure credential storage is unavailable: {err}"))
    }
}

impl CredentialStore for KeyringCredentialStore {
    fn get(&self, name: &str) -> Result<Option<String>, String> {
        match Self::entry(name)?.get_password() {
            Ok(value) => Ok(Some(value)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(err) => Err(format!("could not read secure credential: {err}")),
        }
    }

    fn set(&self, name: &str, value: &str) -> Result<(), String> {
        Self::entry(name)?
            .set_password(value)
            .map_err(|err| format!("could not save secure credential: {err}"))
    }

    fn delete(&self, name: &str) -> Result<(), String> {
        match Self::entry(name)?.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(err) => Err(format!("could not remove secure credential: {err}")),
        }
    }
}

#[derive(Default)]
struct MemoryCredentialStore(Mutex<HashMap<String, String>>);

impl CredentialStore for MemoryCredentialStore {
    fn get(&self, name: &str) -> Result<Option<String>, String> {
        Ok(self
            .0
            .lock()
            .ok()
            .and_then(|values| values.get(name).cloned()))
    }

    fn set(&self, name: &str, value: &str) -> Result<(), String> {
        self.0
            .lock()
            .map_err(|_| "test credential store is unavailable".to_string())?
            .insert(name.to_string(), value.to_string());
        Ok(())
    }

    fn delete(&self, name: &str) -> Result<(), String> {
        if let Ok(mut values) = self.0.lock() {
            values.remove(name);
        }
        Ok(())
    }
}

fn new_app_token() -> Result<String, String> {
    let mut raw = [0u8; 32];
    rand::rng().fill_bytes(&mut raw);
    Ok(format!("happ_{}", URL_SAFE_NO_PAD.encode(raw)))
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

fn validated_local_base_url(base_url: Option<String>) -> Result<String, String> {
    let raw = base_url.ok_or_else(|| {
        "Hecate is still starting. Try again once the app finishes loading.".to_string()
    })?;
    let url = reqwest::Url::parse(raw.trim().trim_end_matches('/'))
        .map_err(|_| "The local Hecate address is invalid.".to_string())?;
    if url.scheme() != "http" {
        return Err("The desktop app only connects to its loopback Hecate runtime.".to_string());
    }
    let host = url.host_str().unwrap_or_default();
    if host != "127.0.0.1" && host != "localhost" && host != "::1" {
        return Err("The desktop app only connects to its loopback Hecate runtime.".to_string());
    }
    Ok(url.as_str().trim_end_matches('/').to_string())
}

#[derive(Clone)]
struct CloudClient {
    base_url: String,
    http: reqwest::Client,
    app_version: String,
}

impl CloudClient {
    fn new(base_url: String, http: reqwest::Client, app_version: &str) -> Self {
        Self {
            base_url,
            http,
            app_version: app_version.to_string(),
        }
    }

    async fn me(&self, token: &str) -> Result<CloudActor, CloudAPIError> {
        self.request(reqwest::Method::GET, "/api/v1/me", token, None::<&()>)
            .await
    }

    async fn create_host(
        &self,
        token: &str,
        org_id: &str,
        name: &str,
    ) -> Result<CreatedHost, CloudAPIError> {
        self.request(
            reqwest::Method::POST,
            "/api/v1/hosts",
            token,
            Some(&serde_json::json!({
                "org_id": org_id,
                "name": name,
                "capabilities": ["browser_proxy", "healthz", "whoami"],
                "hecate_version": self.app_version,
            })),
        )
        .await
    }

    async fn revoke_host(
        &self,
        token: &str,
        org_id: &str,
        host_id: &str,
    ) -> Result<(), CloudAPIError> {
        let path = format!("/api/v1/hosts/{host_id}");
        let _: CloudHost = self
            .request(
                reqwest::Method::PATCH,
                &path,
                token,
                Some(&serde_json::json!({ "org_id": org_id, "revoke": true })),
            )
            .await?;
        Ok(())
    }

    async fn revoke_session(&self, token: &str) -> Result<(), CloudAPIError> {
        let _: serde_json::Value = self
            .request(
                reqwest::Method::POST,
                "/api/v1/sessions/current",
                token,
                None::<&()>,
            )
            .await?;
        Ok(())
    }

    async fn request<T, B>(
        &self,
        method: reqwest::Method,
        path: &str,
        token: &str,
        body: Option<&B>,
    ) -> Result<T, CloudAPIError>
    where
        T: DeserializeOwned,
        B: Serialize + ?Sized,
    {
        let mut request = self
            .http
            .request(method, format!("{}{}", self.base_url, path))
            .bearer_auth(token)
            .header("x-hecate-app-version", &self.app_version)
            .timeout(Duration::from_secs(20));
        if let Some(body) = body {
            request = request.json(body);
        }
        let response = request.send().await.map_err(CloudAPIError::network)?;
        let status = response.status();
        let payload = response.bytes().await.map_err(CloudAPIError::network)?;
        if !status.is_success() {
            let message = serde_json::from_slice::<CloudErrorEnvelope>(&payload)
                .ok()
                .map(|body| body.error.message)
                .filter(|message| !message.trim().is_empty())
                .unwrap_or_else(|| format!("Hecate Cloud returned HTTP {}", status.as_u16()));
            return Err(CloudAPIError {
                status: Some(status.as_u16()),
                message,
            });
        }
        serde_json::from_slice::<CloudEnvelope<T>>(&payload)
            .map(|envelope| envelope.data)
            .map_err(|err| CloudAPIError {
                status: Some(status.as_u16()),
                message: format!("Hecate Cloud returned an invalid response: {err}"),
            })
    }
}

#[derive(Debug)]
struct CloudAPIError {
    status: Option<u16>,
    message: String,
}

impl CloudAPIError {
    fn network(error: reqwest::Error) -> Self {
        Self {
            status: error.status().map(|status| status.as_u16()),
            message: if error.is_timeout() {
                "Hecate Cloud did not respond in time.".to_string()
            } else {
                "Hecate Cloud is not reachable.".to_string()
            },
        }
    }
}

impl fmt::Display for CloudAPIError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

#[derive(Deserialize)]
struct CloudEnvelope<T> {
    data: T,
}

#[derive(Deserialize)]
struct CloudErrorEnvelope {
    error: CloudErrorBody,
}

#[derive(Deserialize)]
struct CloudErrorBody {
    message: String,
}

#[derive(Clone, Deserialize)]
struct CloudActor {
    email: String,
    org_id: String,
}

#[derive(Deserialize)]
struct CreatedHost {
    host: CloudHost,
    host_token: String,
}

#[derive(Deserialize)]
struct CloudHost {
    id: String,
}

async fn run_relay<F>(
    client: &CloudClient,
    host_id: &str,
    host_token: &str,
    local_base_url: &str,
    mut cancel_rx: watch::Receiver<bool>,
    on_connected: F,
) -> Result<(), String>
where
    F: FnOnce(),
{
    if !host_id
        .chars()
        .all(|character| character.is_ascii_alphanumeric() || character == '_' || character == '-')
    {
        return Err("Cloud returned an invalid desktop host id.".to_string());
    }
    let mut websocket_url = reqwest::Url::parse(&client.base_url)
        .map_err(|_| "Hecate Cloud URL is invalid.".to_string())?;
    websocket_url
        .set_scheme(if websocket_url.scheme() == "http" {
            "ws"
        } else {
            "wss"
        })
        .map_err(|_| "Hecate Cloud URL is invalid.".to_string())?;
    websocket_url.set_path(&format!("/api/v1/hosts/{host_id}/connect"));
    websocket_url.set_query(None);
    websocket_url.set_fragment(None);
    let mut request = websocket_url
        .as_str()
        .into_client_request()
        .map_err(|_| "Could not create the remote access connection.".to_string())?;
    request.headers_mut().insert(
        AUTHORIZATION,
        HeaderValue::from_str(&format!("Bearer {host_token}"))
            .map_err(|_| "Desktop host credentials are invalid.".to_string())?,
    );
    request.headers_mut().insert(
        HeaderName::from_static("x-hecate-app-version"),
        HeaderValue::from_str(&client.app_version)
            .map_err(|_| "Hecate app version is invalid.".to_string())?,
    );
    let (socket, _) = tokio_tungstenite::connect_async(request)
        .await
        .map_err(|err| format!("Remote access connection failed: {err}"))?;
    on_connected();

    let (mut sink, mut stream) = socket.split();
    let (outgoing_tx, mut outgoing_rx) = mpsc::channel::<String>(64);
    let permits = Arc::new(Semaphore::new(RELAY_MAX_CONCURRENCY));
    loop {
        tokio::select! {
            result = cancel_rx.changed() => {
                if result.is_err() || *cancel_rx.borrow() {
                    let _ = sink.send(Message::Close(None)).await;
                    return Ok(());
                }
            }
            outgoing = outgoing_rx.recv() => {
                let Some(payload) = outgoing else { return Ok(()); };
                sink.send(Message::Text(payload.into()))
                    .await
                    .map_err(|err| format!("Remote access response failed: {err}"))?;
            }
            incoming = stream.next() => {
                match incoming {
                    Some(Ok(Message::Text(payload))) => {
                        let permit = match permits.clone().try_acquire_owned() {
                            Ok(permit) => permit,
                            Err(_) => {
                                if let Some(id) = relay_request_id(payload.as_str()) {
                                    let response = relay_error_response(&id, 503, "Hecate is busy. Try again.");
                                    let _ = outgoing_tx.send(response).await;
                                }
                                continue;
                            }
                        };
                        let http = client.http.clone();
                        let local = local_base_url.to_string();
                        let outgoing = outgoing_tx.clone();
                        tauri::async_runtime::spawn(async move {
                            let _permit = permit;
                            handle_relay_payload(&http, &local, payload.as_str(), &outgoing).await;
                        });
                    }
                    Some(Ok(Message::Ping(payload))) => {
                        sink.send(Message::Pong(payload))
                            .await
                            .map_err(|err| format!("Remote access heartbeat failed: {err}"))?;
                    }
                    Some(Ok(Message::Close(_))) | None => return Ok(()),
                    Some(Ok(_)) => {}
                    Some(Err(err)) => return Err(format!("Remote access connection closed: {err}")),
                }
            }
        }
    }
}

#[derive(Deserialize)]
struct RelayRequest {
    #[serde(default)]
    id: String,
    #[serde(default, rename = "type")]
    kind: String,
    #[serde(default)]
    method: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    headers: HashMap<String, String>,
    #[serde(default)]
    body_base64: String,
}

#[derive(Serialize)]
struct RelayResponse {
    id: String,
    #[serde(rename = "type")]
    kind: &'static str,
    status: u16,
    headers: HashMap<String, String>,
    body_base64: String,
}

#[derive(Serialize)]
struct RelayResponseStart {
    id: String,
    #[serde(rename = "type")]
    kind: &'static str,
    status: u16,
    headers: HashMap<String, String>,
}

#[derive(Serialize)]
struct RelayResponseChunk {
    id: String,
    #[serde(rename = "type")]
    kind: &'static str,
    body_base64: String,
}

#[derive(Serialize)]
struct RelayResponseEnd {
    id: String,
    #[serde(rename = "type")]
    kind: &'static str,
}

async fn handle_relay_payload(
    http: &reqwest::Client,
    local_base_url: &str,
    payload: &str,
    outgoing: &mpsc::Sender<String>,
) {
    let request = match serde_json::from_str::<RelayRequest>(payload) {
        Ok(request) => request,
        Err(_) => {
            let _ = outgoing
                .send(relay_error_response("", 400, "Remote request was invalid."))
                .await;
            return;
        }
    };
    match request.kind.as_str() {
        "http_request" => {
            let response = execute_status_request(http, local_base_url, request).await;
            let _ = outgoing.send(response).await;
        }
        "http_proxy_request" => {
            execute_proxy_request(http, local_base_url, request, outgoing).await;
        }
        _ => {
            let _ = outgoing
                .send(relay_error_response(
                    &request.id,
                    403,
                    "This remote route is not available.",
                ))
                .await;
        }
    }
}

async fn execute_status_request(
    http: &reqwest::Client,
    local_base_url: &str,
    request: RelayRequest,
) -> String {
    let method = request.method.trim().to_ascii_uppercase();
    let path = request.path.trim();
    if !status_route_allowed(&method, path) {
        return relay_error_response(&request.id, 403, "This remote route is not available.");
    }
    let body = match decode_request_body(&method, &request.body_base64, RELAY_MAX_REQUEST_BODY) {
        Ok(body) => body,
        Err(response) => return relay_error_response(&request.id, response.0, response.1),
    };
    execute_buffered_request(
        http,
        local_base_url,
        &request.id,
        &method,
        path,
        &request.headers,
        body,
        RELAY_MAX_RESPONSE_BODY,
    )
    .await
}

async fn execute_proxy_request(
    http: &reqwest::Client,
    local_base_url: &str,
    request: RelayRequest,
    outgoing: &mpsc::Sender<String>,
) {
    let method = request.method.trim().to_ascii_uppercase();
    let path = request.path.trim();
    if !proxy_method_allowed(&method) || !proxy_path_allowed(path) {
        let _ = outgoing
            .send(relay_error_response(
                &request.id,
                403,
                "This remote route is not available.",
            ))
            .await;
        return;
    }
    let body = match decode_request_body(&method, &request.body_base64, RELAY_MAX_PROXY_BODY) {
        Ok(body) => body,
        Err(response) => {
            let _ = outgoing
                .send(relay_error_response(&request.id, response.0, response.1))
                .await;
            return;
        }
    };
    let target = match local_request_url(local_base_url, path) {
        Ok(target) => target,
        Err(message) => {
            let _ = outgoing
                .send(relay_error_response(&request.id, 502, &message))
                .await;
            return;
        }
    };
    let reqwest_method = match reqwest::Method::from_bytes(method.as_bytes()) {
        Ok(method) => method,
        Err(_) => {
            let _ = outgoing
                .send(relay_error_response(
                    &request.id,
                    405,
                    "Method is not available.",
                ))
                .await;
            return;
        }
    };
    let mut builder = http.request(reqwest_method, target);
    builder = apply_proxy_headers(builder, &request.headers);
    if !body.is_empty() {
        builder = builder.body(body);
    }
    let mut response = match builder.send().await {
        Ok(response) => response,
        Err(_) => {
            let _ = outgoing
                .send(relay_error_response(
                    &request.id,
                    502,
                    "Local Hecate is not reachable.",
                ))
                .await;
            return;
        }
    };
    let status = response.status().as_u16();
    let headers = safe_response_headers(response.headers());
    let is_stream = response
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .map(|value| value.to_ascii_lowercase().contains("text/event-stream"))
        .unwrap_or(false);
    if is_stream {
        let start = RelayResponseStart {
            id: request.id.clone(),
            kind: "http_response_start",
            status,
            headers,
        };
        if send_json(outgoing, &start).await.is_err() {
            return;
        }
        while let Ok(Some(chunk)) = response.chunk().await {
            let item = RelayResponseChunk {
                id: request.id.clone(),
                kind: "http_response_chunk",
                body_base64: STANDARD.encode(chunk),
            };
            if send_json(outgoing, &item).await.is_err() {
                return;
            }
        }
        let _ = send_json(
            outgoing,
            &RelayResponseEnd {
                id: request.id,
                kind: "http_response_end",
            },
        )
        .await;
        return;
    }

    let mut body = Vec::new();
    loop {
        match response.chunk().await {
            Ok(Some(chunk))
                if body.len().saturating_add(chunk.len()) <= RELAY_MAX_RESPONSE_BODY =>
            {
                body.extend_from_slice(&chunk);
            }
            Ok(Some(_)) => {
                let _ = outgoing
                    .send(relay_error_response(
                        &request.id,
                        502,
                        "Local Hecate response was too large.",
                    ))
                    .await;
                return;
            }
            Ok(None) => break,
            Err(_) => {
                let _ = outgoing
                    .send(relay_error_response(
                        &request.id,
                        502,
                        "Could not read local Hecate response.",
                    ))
                    .await;
                return;
            }
        }
    }
    let _ = send_json(
        outgoing,
        &RelayResponse {
            id: request.id,
            kind: "http_response",
            status,
            headers,
            body_base64: STANDARD.encode(body),
        },
    )
    .await;
}

async fn execute_buffered_request(
    http: &reqwest::Client,
    local_base_url: &str,
    id: &str,
    method: &str,
    path: &str,
    headers: &HashMap<String, String>,
    body: Vec<u8>,
    max_response: usize,
) -> String {
    let target = match local_request_url(local_base_url, path) {
        Ok(target) => target,
        Err(message) => return relay_error_response(id, 502, &message),
    };
    let reqwest_method = match reqwest::Method::from_bytes(method.as_bytes()) {
        Ok(method) => method,
        Err(_) => return relay_error_response(id, 405, "Method is not available."),
    };
    let mut builder = apply_proxy_headers(http.request(reqwest_method, target), headers)
        .timeout(Duration::from_secs(20));
    if !body.is_empty() {
        builder = builder.body(body);
    }
    let mut response = match builder.send().await {
        Ok(response) => response,
        Err(_) => return relay_error_response(id, 502, "Local Hecate is not reachable."),
    };
    let status = response.status().as_u16();
    let headers = safe_response_headers(response.headers());
    let mut response_body = Vec::new();
    loop {
        match response.chunk().await {
            Ok(Some(chunk)) if response_body.len().saturating_add(chunk.len()) <= max_response => {
                response_body.extend_from_slice(&chunk);
            }
            Ok(Some(_)) => {
                return relay_error_response(id, 502, "Local Hecate response was too large.")
            }
            Ok(None) => break,
            Err(_) => {
                return relay_error_response(id, 502, "Could not read local Hecate response.")
            }
        }
    }
    serde_json::to_string(&RelayResponse {
        id: id.to_string(),
        kind: "http_response",
        status,
        headers,
        body_base64: STANDARD.encode(response_body),
    })
    .unwrap_or_else(|_| relay_error_response(id, 500, "Could not encode local Hecate response."))
}

fn decode_request_body(
    method: &str,
    encoded: &str,
    limit: usize,
) -> Result<Vec<u8>, (u16, &'static str)> {
    if method == "GET" || method == "HEAD" || encoded.is_empty() {
        return Ok(Vec::new());
    }
    let decoded = STANDARD
        .decode(encoded)
        .map_err(|_| (400, "Remote request body was invalid."))?;
    if decoded.len() > limit {
        return Err((413, "Remote request body is too large."));
    }
    Ok(decoded)
}

fn apply_proxy_headers(
    mut builder: reqwest::RequestBuilder,
    headers: &HashMap<String, String>,
) -> reqwest::RequestBuilder {
    let allowed = [
        "accept",
        "accept-language",
        "cache-control",
        "content-type",
        "if-none-match",
        "if-modified-since",
        "range",
        "user-agent",
    ];
    let mut has_accept = false;
    for (name, value) in headers {
        let normalized = name.trim().to_ascii_lowercase();
        if !allowed.contains(&normalized.as_str()) || value.trim().is_empty() {
            continue;
        }
        if normalized == "accept" {
            has_accept = true;
        }
        builder = builder.header(normalized, value.trim());
    }
    if !has_accept {
        builder = builder.header("accept", "application/json, text/plain, */*");
    }
    builder
}

fn safe_response_headers(headers: &HeaderMap) -> HashMap<String, String> {
    let allowed = [
        "accept-ranges",
        "cache-control",
        "content-language",
        "content-range",
        "content-type",
        "etag",
        "expires",
        "last-modified",
        "referrer-policy",
        "vary",
        "x-content-type-options",
    ];
    let mut safe = HashMap::new();
    for name in allowed {
        if let Some(value) = headers.get(name).and_then(|value| value.to_str().ok()) {
            if !value.trim().is_empty() {
                safe.insert(name.to_string(), value.trim().to_string());
            }
        }
    }
    safe
}

fn local_request_url(base_url: &str, path: &str) -> Result<reqwest::Url, String> {
    let mut base = reqwest::Url::parse(base_url)
        .map_err(|_| "The local Hecate address is invalid.".to_string())?;
    if !proxy_path_allowed(path) {
        return Err("Remote request path is invalid.".to_string());
    }
    let request = reqwest::Url::parse(&format!("http://hecate.invalid{path}"))
        .map_err(|_| "Remote request path is invalid.".to_string())?;
    base.set_path(request.path());
    base.set_query(request.query());
    base.set_fragment(None);
    Ok(base)
}

fn status_route_allowed(method: &str, path: &str) -> bool {
    if path.contains(['?', '#']) {
        return false;
    }
    match method {
        "GET" => {
            matches!(
                path,
                "/healthz"
                    | "/hecate/v1/whoami"
                    | "/hecate/v1/projects"
                    | "/hecate/v1/chat/sessions"
                    | "/hecate/v1/tasks"
                    | "/hecate/v1/providers/status"
                    | "/hecate/v1/agent-adapters"
            ) || relay_path_matches("/hecate/v1/tasks/{id}/approvals", path)
                || relay_path_matches("/hecate/v1/chat/sessions/{id}/approvals", path)
        }
        "POST" => {
            relay_path_matches(
                "/hecate/v1/tasks/{id}/approvals/{approval_id}/resolve",
                path,
            ) || relay_path_matches(
                "/hecate/v1/chat/sessions/{id}/approvals/{approval_id}/resolve",
                path,
            )
        }
        _ => false,
    }
}

fn relay_path_matches(pattern: &str, path: &str) -> bool {
    let pattern_parts = pattern.split('/').collect::<Vec<_>>();
    let path_parts = path.split('/').collect::<Vec<_>>();
    pattern_parts.len() == path_parts.len()
        && pattern_parts
            .iter()
            .zip(path_parts)
            .all(|(expected, actual)| {
                if expected.starts_with('{') && expected.ends_with('}') {
                    !actual.is_empty() && !actual.contains(['/', '?', '#'])
                } else {
                    *expected == actual
                }
            })
}

fn proxy_method_allowed(method: &str) -> bool {
    matches!(
        method,
        "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS"
    )
}

fn proxy_path_allowed(path: &str) -> bool {
    path.starts_with('/')
        && !path.starts_with("//")
        && !path.contains('\0')
        && !path.contains('\r')
        && !path.contains('\n')
}

fn relay_request_id(payload: &str) -> Option<String> {
    serde_json::from_str::<serde_json::Value>(payload)
        .ok()?
        .get("id")?
        .as_str()
        .map(str::to_string)
}

fn relay_error_response(id: &str, status: u16, message: &str) -> String {
    let body = serde_json::json!({ "error": { "message": message } });
    serde_json::to_string(&RelayResponse {
        id: id.to_string(),
        kind: "http_response",
        status,
        headers: HashMap::from([("content-type".to_string(), "application/json".to_string())]),
        body_base64: STANDARD.encode(serde_json::to_vec(&body).unwrap_or_default()),
    })
    .unwrap_or_else(|_| "{\"type\":\"http_response\",\"status\":500}".to_string())
}

async fn send_json<T: Serialize>(sender: &mpsc::Sender<String>, value: &T) -> Result<(), ()> {
    let payload = serde_json::to_string(value).map_err(|_| ())?;
    sender.send(payload).await.map_err(|_| ())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn app_tokens_are_random_bearer_tokens() {
        let first = new_app_token().expect("first token");
        let second = new_app_token().expect("second token");
        assert!(first.starts_with("happ_"));
        assert!(first.len() >= 40);
        assert_ne!(first, second);
    }

    #[test]
    fn preferences_never_serialize_credentials() {
        let preferences = CloudConnectionPreferences {
            auto_start_enabled: true,
            account_email: Some("operator@example.com".to_string()),
            org_id: Some("org_1".to_string()),
            host_id: Some("host_1".to_string()),
        };
        let encoded = serde_json::to_string(&preferences).expect("preferences");
        assert!(!encoded.contains("token"));
        assert!(!encoded.contains("secret"));
        assert!(encoded.contains("host_1"));
    }

    #[test]
    fn connection_preferences_round_trip_without_secrets() {
        let path = std::env::temp_dir().join(format!(
            "hecate-cloud-connection-{}.json",
            std::process::id()
        ));
        let _ = std::fs::remove_file(&path);
        let preferences = CloudConnectionPreferences {
            auto_start_enabled: true,
            account_email: Some("operator@example.com".to_string()),
            org_id: Some("org_1".to_string()),
            host_id: Some("host_1".to_string()),
        };
        write_preferences(&path, &preferences).expect("write preferences");
        let restored = read_preferences(&path);
        assert!(restored.auto_start_enabled);
        assert_eq!(
            restored.account_email.as_deref(),
            Some("operator@example.com")
        );
        assert_eq!(restored.host_id.as_deref(), Some("host_1"));
        let _ = std::fs::remove_file(path);
    }

    #[test]
    fn local_runtime_url_must_be_loopback_http() {
        assert!(validated_local_base_url(Some("http://127.0.0.1:8765".to_string())).is_ok());
        assert!(validated_local_base_url(Some("http://localhost:8765".to_string())).is_ok());
        assert!(validated_local_base_url(Some("https://example.com".to_string())).is_err());
        assert!(validated_local_base_url(Some("http://192.0.2.1:8765".to_string())).is_err());
    }

    #[test]
    fn status_routes_keep_the_narrow_approval_allowlist() {
        assert!(status_route_allowed("GET", "/healthz"));
        assert!(status_route_allowed(
            "POST",
            "/hecate/v1/tasks/task_1/approvals/approval_1/resolve"
        ));
        assert!(!status_route_allowed("POST", "/hecate/v1/projects"));
        assert!(!status_route_allowed("GET", "/hecate/v1/system/stats"));
    }

    #[test]
    fn full_proxy_rejects_absolute_and_header_injected_paths() {
        assert!(proxy_path_allowed("/hecate/v1/chat/sessions?limit=20"));
        assert!(!proxy_path_allowed("//example.com/path"));
        assert!(!proxy_path_allowed("/path\r\nx-extra: value"));
    }

    #[test]
    fn memory_store_drives_signed_in_status_without_real_keychain() {
        let credentials = Arc::new(MemoryCredentialStore::default());
        credentials
            .set(SESSION_CREDENTIAL, "happ_test-session")
            .expect("save session");
        let supervisor = CloudConnectionSupervisor::with_store(
            None,
            credentials,
            "https://console.example.test",
        );
        let status = supervisor.status(Some("http://127.0.0.1:8765".to_string()));
        assert!(status.available);
        assert!(status.signed_in);
        assert!(status.gateway_ready);
        assert_eq!(status.phase, "disconnected");
    }

    #[test]
    fn cancelling_browser_authorization_removes_the_unapproved_token() {
        let credentials = Arc::new(MemoryCredentialStore::default());
        credentials
            .set(SESSION_CREDENTIAL, "happ_pending-session")
            .expect("save pending session");
        let supervisor = CloudConnectionSupervisor::with_store(
            None,
            credentials.clone(),
            "https://console.example.test",
        );
        {
            let mut state = supervisor.inner.state.lock().expect("state");
            state.signed_in = false;
            state.phase = ConnectionPhase::Authorizing;
            state.preferences.auto_start_enabled = true;
        }

        let status = supervisor.stop(Some("http://127.0.0.1:8765".to_string()));

        assert!(!status.auto_start_enabled);
        assert_eq!(status.phase, "disconnected");
        assert_eq!(
            credentials.get(SESSION_CREDENTIAL).expect("read session"),
            None
        );
    }
}
