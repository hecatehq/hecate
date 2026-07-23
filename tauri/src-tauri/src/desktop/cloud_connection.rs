use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use base64::Engine;
use futures_util::{SinkExt, StreamExt};
use rand::RngCore;
use reqwest::header::{HeaderMap, HeaderName, HeaderValue};
use reqwest::redirect::Policy;
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::sync::{mpsc, watch, Semaphore};
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::header::AUTHORIZATION;
use tokio_tungstenite::tungstenite::protocol::Message;

const DEFAULT_CLOUD_URL: &str = "https://console.hecatehq.com";
const KEYRING_SERVICE: &str = "sh.hecate.app.cloud";
const SESSION_CREDENTIAL: &str = "account-session";
const HOST_CREDENTIAL: &str = "desktop-host";
const MAX_LOGIN_TIMEOUT: Duration = Duration::from_secs(20 * 60);
const LOGIN_POLL_INTERVAL: Duration = Duration::from_secs(2);
const AUTHENTICATED_BOOTSTRAP_RETRY_INITIAL: Duration = Duration::from_secs(1);
const AUTHENTICATED_BOOTSTRAP_RETRY_MAX: Duration = Duration::from_secs(30);
const RELAY_RECONNECT_MAX: Duration = Duration::from_secs(30);
const RELAY_PING_INTERVAL: Duration = Duration::from_secs(15);
const RELAY_READ_IDLE_TIMEOUT: Duration = Duration::from_secs(45);
const RELAY_MAX_CONCURRENCY: usize = 16;
const RELAY_MAX_REQUEST_BODY: usize = 64 * 1024;
const RELAY_MAX_PROXY_BODY: usize = 16 * 1024 * 1024;
const RELAY_MAX_RESPONSE_BODY: usize = 16 * 1024 * 1024;
const RUNTIME_EVENT_POLL_INTERVAL: Duration = Duration::from_secs(1);
const RUNTIME_EVENT_ACK_TIMEOUT: Duration = Duration::from_secs(15);
const RUNTIME_EVENT_PAGE_LIMIT: usize = 500;
const RUNTIME_EVENT_MAX_RESPONSE_BODY: usize = 512 * 1024;
const CHAT_APPROVAL_POLL_INTERVAL: Duration = Duration::from_secs(3);
const CHAT_APPROVAL_ACTIVE_SESSION_LIMIT: usize = 50;
const CHAT_APPROVAL_PENDING_LIMIT: usize = 100;
const REMOTE_ACCESS_CANCELLED: &str = "Remote access was cancelled.";
const REMOTE_ACTOR_ID_HEADER: &str = "x-hecate-remote-actor-id";
const REMOTE_ORG_ID_HEADER: &str = "x-hecate-remote-org-id";
const REMOTE_RUNTIME_ID_HEADER: &str = "x-hecate-remote-runtime-id";
const REMOTE_RUNTIME_SECRET_HEADER: &str = "x-hecate-remote-runtime-secret";

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
    remote_runtime_secret: String,
}

struct ConnectionState {
    preferences: CloudConnectionPreferences,
    phase: ConnectionPhase,
    signed_in: bool,
    message: String,
    last_error: Option<String>,
    approval_url: Option<String>,
    base_url: Option<String>,
    cancel: Option<watch::Sender<bool>>,
    generation: u64,
    credential_error: Option<String>,
}

#[derive(Default)]
struct RuntimeEventCursorState {
    after_sequence: i64,
    initialized: bool,
    pending_approvals: HashMap<String, RuntimeEventEnvelope>,
    chat_approval_ids: HashSet<String>,
}

type RuntimeEventCursor = Arc<Mutex<RuntimeEventCursorState>>;

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
            new_remote_runtime_secret(),
        )
    }
}

impl CloudConnectionSupervisor {
    pub fn new(preferences_path: PathBuf, remote_runtime_secret: String) -> Self {
        let cloud_url = std::env::var("HECATE_CLOUD_URL")
            .ok()
            .map(|value| value.trim().trim_end_matches('/').to_string())
            .filter(|value| !value.is_empty())
            .unwrap_or_else(|| DEFAULT_CLOUD_URL.to_string());
        Self::with_store(
            Some(preferences_path),
            Arc::new(KeyringCredentialStore),
            &cloud_url,
            remote_runtime_secret,
        )
    }

    fn with_store(
        preferences_path: Option<PathBuf>,
        credentials: Arc<dyn CredentialStore>,
        cloud_url: &str,
        remote_runtime_secret: String,
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
                    approval_url: None,
                    base_url: None,
                    cancel: None,
                    generation: 0,
                    credential_error,
                }),
                preferences_path,
                credentials,
                http: reqwest::Client::builder()
                    .connect_timeout(Duration::from_secs(10))
                    .redirect(Policy::none())
                    .build()
                    .expect("native Cloud HTTP client configuration is valid"),
                cloud_url: cloud_url.trim_end_matches('/').to_string(),
                remote_runtime_secret,
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
        } else {
            log::info!("remote access reconnect started");
        }
    }

    pub async fn start(&self, base_url: Option<String>) -> Result<CloudConnectionStatus, String> {
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
                self.launch_authorization(base_url).await?;
            }
            Err(err) => return Err(err),
        }
        log::info!("remote access start requested");
        Ok(self.status(None))
    }

    pub fn pending_approval_url(&self) -> Option<String> {
        self.inner
            .state
            .lock()
            .ok()
            .and_then(|state| state.approval_url.clone())
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
        state.approval_url = None;
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
        log::info!("remote access stopped");
        status_from_state(&state, &self.inner.cloud_url)
    }

    pub async fn sign_out(&self, base_url: Option<String>) -> CloudConnectionStatus {
        let (generation, session_token, host_id, host_org_id) = {
            let mut state = match self.inner.state.lock() {
                Ok(state) => state,
                Err(_) => return unavailable_status(base_url, &self.inner.cloud_url),
            };
            cancel_current(&mut state);
            state.generation = state.generation.wrapping_add(1);
            (
                state.generation,
                self.inner
                    .credentials
                    .get(SESSION_CREDENTIAL)
                    .ok()
                    .flatten(),
                state.preferences.host_id.clone(),
                state
                    .preferences
                    .host_org_id
                    .clone()
                    .or_else(|| state.preferences.org_id.clone()),
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
            host_org_id.as_deref(),
        ) {
            if let Err(err) = client.revoke_host(session, org, host).await {
                append_warning(
                    &mut revoke_warning,
                    format!("Could not revoke this computer: {err}"),
                );
            }
        }
        if let Some(session) = session_token.as_deref() {
            if let Err(err) = client.revoke_session(session).await {
                append_warning(
                    &mut revoke_warning,
                    format!("Could not revoke the account session: {err}"),
                );
            }
        }
        let mut state = match self.inner.state.lock() {
            Ok(state) => state,
            Err(_) => return unavailable_status(base_url, &self.inner.cloud_url),
        };
        if state.generation != generation {
            return status_from_state(&state, &self.inner.cloud_url);
        }
        for (credential, label) in [
            (SESSION_CREDENTIAL, "account session"),
            (HOST_CREDENTIAL, "computer credential"),
        ] {
            if let Err(err) = self.inner.credentials.delete(credential) {
                append_warning(
                    &mut revoke_warning,
                    format!("Could not remove the local {label}: {err}"),
                );
            }
        }
        state.preferences = CloudConnectionPreferences::default();
        state.signed_in = false;
        state.phase = ConnectionPhase::Disconnected;
        state.base_url = base_url.or_else(|| state.base_url.clone());
        state.approval_url = None;
        state.last_error = revoke_warning;
        state.message = "Signed out of Hecate Cloud.".to_string();
        if let Err(err) = self.persist_preferences(&state.preferences) {
            append_warning(&mut state.last_error, err);
        }
        log::info!("Hecate Cloud account signed out");
        status_from_state(&state, &self.inner.cloud_url)
    }

    pub fn kill_for_exit(&self) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        cancel_current(&mut state);
        state.generation = state.generation.wrapping_add(1);
        state.phase = ConnectionPhase::Disconnected;
        state.approval_url = None;
    }

    async fn launch_authorization(&self, base_url: String) -> Result<(), String> {
        let token = new_app_token()?;
        let (generation, mut cancel_rx) = {
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
            state.preferences.auto_start_enabled = false;
            state.phase = ConnectionPhase::Authorizing;
            state.signed_in = false;
            state.base_url = Some(base_url.clone());
            state.approval_url = None;
            state.last_error = None;
            state.message = "Starting secure sign-in...".to_string();
            (generation, cancel_rx)
        };

        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let authorization = tokio::select! {
            result = client.create_app_authorization(&token) => result,
            result = cancel_rx.changed() => {
                let _ = result;
                return Ok(());
            }
        };
        if *cancel_rx.borrow() || !self.is_current(generation) {
            return Ok(());
        }
        let authorization = match authorization {
            Ok(authorization) => authorization,
            Err(err) => {
                self.fail_authorization_if_current(
                    generation,
                    "Secure sign-in could not be started. Try again.",
                    Some(err.to_string()),
                );
                return Err(err.to_string());
            }
        };
        let validated = match validated_app_authorization(&authorization, &self.inner.cloud_url) {
            Ok(validated) => validated,
            Err(err) => {
                self.fail_authorization_if_current(
                    generation,
                    "Hecate Cloud returned an invalid sign-in request.",
                    Some(err.clone()),
                );
                return Err(err);
            }
        };
        if !self.install_authorization_if_current(generation, &token, validated.approval_url)? {
            return Ok(());
        }

        let expires_in = validated.expires_in;
        let supervisor = self.clone();
        tauri::async_runtime::spawn(async move {
            supervisor
                .authorize_then_connect(generation, base_url, token, expires_in, cancel_rx)
                .await;
        });
        Ok(())
    }

    fn install_authorization_if_current(
        &self,
        generation: u64,
        token: &str,
        approval_url: String,
    ) -> Result<bool, String> {
        let mut state = self
            .inner
            .state
            .lock()
            .map_err(|_| "Remote access state is unavailable.".to_string())?;
        if state.generation != generation {
            return Ok(false);
        }

        if let Err(err) = self.inner.credentials.set(SESSION_CREDENTIAL, token) {
            state.phase = ConnectionPhase::Error;
            state.cancel = None;
            state.message = "Secure credential storage is unavailable.".to_string();
            state.last_error = Some(err.clone());
            return Err(err);
        }
        let mut preferences = state.preferences.clone();
        preferences.auto_start_enabled = true;
        if let Err(err) = self.persist_preferences(&preferences) {
            let mut error = Some(err.clone());
            if let Err(delete_err) = self.inner.credentials.delete(SESSION_CREDENTIAL) {
                append_warning(&mut error, delete_err);
            }
            state.phase = ConnectionPhase::Error;
            state.cancel = None;
            state.message = "Secure sign-in could not be saved.".to_string();
            state.last_error = error;
            return Err(err);
        }

        state.preferences = preferences;
        state.approval_url = Some(approval_url);
        state.last_error = None;
        state.message =
            "Approve sign-in in your browser. This window will update automatically.".to_string();
        Ok(true)
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
            state.approval_url = None;
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
        expires_in: Duration,
        mut cancel_rx: watch::Receiver<bool>,
    ) {
        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let deadline = tokio::time::Instant::now() + expires_in;
        loop {
            if *cancel_rx.borrow() {
                return;
            }
            match client.me(&token).await {
                Ok(actor) => {
                    self.apply_actor(generation, &actor);
                    self.set_phase_if_current(
                        generation,
                        ConnectionPhase::Connecting,
                        "Connecting this Hecate...",
                        None,
                    );
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
                    self.fail_authorization_if_current(
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
        if *cancel_rx.borrow() || !self.is_current(generation) {
            return;
        }
        let client = CloudClient::new(
            self.inner.cloud_url.clone(),
            self.inner.http.clone(),
            env!("CARGO_PKG_VERSION"),
        );
        let actor = match retry_cloud_bootstrap_request(
            "account",
            AUTHENTICATED_BOOTSTRAP_RETRY_INITIAL,
            AUTHENTICATED_BOOTSTRAP_RETRY_MAX,
            &mut cancel_rx,
            || self.is_current(generation),
            |err, _| {
                self.set_phase_if_current(
                    generation,
                    ConnectionPhase::Reconnecting,
                    "Hecate Cloud is temporarily unavailable. Reconnecting...",
                    Some(err.to_string()),
                );
            },
            || client.me(&session_token),
        )
        .await
        {
            Ok(actor) => actor,
            Err(CloudBootstrapRequestError::Cancelled) => return,
            Err(CloudBootstrapRequestError::Terminal(err)) if err.status == Some(401) => {
                self.expire_account_if_current(
                    generation,
                    "Your Hecate Cloud session expired. Sign in again.",
                );
                return;
            }
            Err(CloudBootstrapRequestError::Terminal(err)) => {
                self.set_error_if_current(
                    generation,
                    "Remote access could not start.",
                    Some(err.to_string()),
                );
                return;
            }
        };
        if *cancel_rx.borrow() || !self.is_current(generation) {
            return;
        }
        let (host_id, host_token) = match self
            .ensure_host(generation, &client, &session_token, &actor, &mut cancel_rx)
            .await
        {
            Ok(credentials) => credentials,
            Err(HostBootstrapError::Cancelled) => return,
            Err(HostBootstrapError::TerminalCloud(err)) if err.status == Some(401) => {
                self.expire_account_if_current(
                    generation,
                    "Your Hecate Cloud session expired. Sign in again.",
                );
                return;
            }
            Err(HostBootstrapError::TerminalCloud(err)) => {
                self.set_error_if_current(
                    generation,
                    "This computer could not be registered.",
                    Some(err.to_string()),
                );
                return;
            }
            Err(HostBootstrapError::Local(err)) => {
                self.set_error_if_current(
                    generation,
                    "This computer could not be registered.",
                    Some(err),
                );
                return;
            }
        };
        self.apply_actor(generation, &actor);
        let relay_authorization = match RelayAuthorization::new(
            &self.inner.remote_runtime_secret,
            &actor.id,
            &actor.org_id,
            &host_id,
        ) {
            Ok(authorization) => authorization,
            Err(err) => {
                self.set_error_if_current(
                    generation,
                    "Remote access identity is invalid.",
                    Some(err),
                );
                return;
            }
        };

        let mut delay = Duration::from_secs(1);
        // The cursor lives for the authenticated connector generation, rather
        // than for one WebSocket. A transient relay reconnect can therefore
        // catch up without replaying every historical event. The first sync is
        // a baseline-only drain so enabling notifications never alerts for old
        // approvals or completed runs.
        let runtime_event_cursor = Arc::new(Mutex::new(RuntimeEventCursorState::default()));
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
                relay_authorization.clone(),
                cancel_rx.clone(),
                Some(runtime_event_cursor.clone()),
                || {
                    log::info!("remote access relay connected");
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
        generation: u64,
        client: &CloudClient,
        session_token: &str,
        actor: &CloudActor,
        cancel_rx: &mut watch::Receiver<bool>,
    ) -> Result<(String, String), HostBootstrapError> {
        let (existing_id, existing_actor_id, existing_org_id) = {
            let state = self.inner.state.lock().map_err(|_| {
                HostBootstrapError::Local("Remote access state is unavailable.".to_string())
            })?;
            if state.generation != generation {
                return Err(HostBootstrapError::Cancelled);
            }
            (
                state.preferences.host_id.clone(),
                state.preferences.host_actor_id.clone(),
                state.preferences.host_org_id.clone(),
            )
        };
        let existing_token = self
            .inner
            .credentials
            .get(HOST_CREDENTIAL)
            .map_err(HostBootstrapError::Local)?;
        if saved_host_owner_matches(
            existing_actor_id.as_deref(),
            existing_org_id.as_deref(),
            actor,
        ) {
            if let (Some(id), Some(token)) = (existing_id.clone(), existing_token.clone()) {
                if self.is_current(generation) {
                    return Ok((id, token));
                }
                return Err(HostBootstrapError::Cancelled);
            }
        }
        if existing_id.is_some() || existing_token.is_some() {
            let mut state = self.inner.state.lock().map_err(|_| {
                HostBootstrapError::Local("Remote access state is unavailable.".to_string())
            })?;
            if state.generation != generation {
                return Err(HostBootstrapError::Cancelled);
            }
            self.inner
                .credentials
                .delete(HOST_CREDENTIAL)
                .map_err(HostBootstrapError::Local)?;
            state.preferences.host_id = None;
            state.preferences.host_actor_id = None;
            state.preferences.host_org_id = None;
            self.persist_preferences(&state.preferences)
                .map_err(HostBootstrapError::Local)?;
        }

        let host_name = default_host_name();
        let created = retry_cloud_bootstrap_request(
            "host_registration",
            AUTHENTICATED_BOOTSTRAP_RETRY_INITIAL,
            AUTHENTICATED_BOOTSTRAP_RETRY_MAX,
            cancel_rx,
            || self.is_current(generation),
            |err, _| {
                self.set_phase_if_current(
                    generation,
                    ConnectionPhase::Reconnecting,
                    "Hecate Cloud is temporarily unavailable. Reconnecting...",
                    Some(err.to_string()),
                );
            },
            || client.create_host(session_token, &actor.org_id, &host_name),
        )
        .await
        .map_err(|err| match err {
            CloudBootstrapRequestError::Cancelled => HostBootstrapError::Cancelled,
            CloudBootstrapRequestError::Terminal(err) => HostBootstrapError::TerminalCloud(err),
        })?;
        if created.host.id.trim().is_empty() || created.host_token.trim().is_empty() {
            return Err(HostBootstrapError::Local(
                "Cloud did not return desktop host credentials.".to_string(),
            ));
        }
        if let Err(err) = self.save_created_host_if_current(generation, actor, &created) {
            abandon_created_host(client, session_token, actor, &created.host.id).await;
            return Err(if err == REMOTE_ACCESS_CANCELLED {
                HostBootstrapError::Cancelled
            } else {
                HostBootstrapError::Local(err)
            });
        }
        Ok((created.host.id, created.host_token))
    }

    fn save_created_host_if_current(
        &self,
        generation: u64,
        actor: &CloudActor,
        created: &CreatedHost,
    ) -> Result<(), String> {
        if !self.is_current(generation) {
            return Err(REMOTE_ACCESS_CANCELLED.to_string());
        }
        self.inner
            .credentials
            .set(HOST_CREDENTIAL, &created.host_token)?;
        let result = (|| -> Result<(), String> {
            let mut state = self
                .inner
                .state
                .lock()
                .map_err(|_| "Remote access state is unavailable.".to_string())?;
            if state.generation != generation {
                Err(REMOTE_ACCESS_CANCELLED.to_string())
            } else {
                let mut preferences = state.preferences.clone();
                preferences.host_id = Some(created.host.id.clone());
                preferences.host_actor_id = Some(actor.id.clone());
                preferences.host_org_id = Some(actor.org_id.clone());
                self.persist_preferences(&preferences)?;
                state.preferences = preferences;
                Ok(())
            }
        })();
        if result.is_err() {
            let _ = self.inner.credentials.delete(HOST_CREDENTIAL);
        }
        result
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
        state.approval_url = None;
        state.last_error = None;
        if let Err(err) = self.persist_preferences(&state.preferences) {
            state.last_error = Some(err);
        }
    }

    fn expire_account_if_current(&self, generation: u64, message: &str) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        let mut errors = None;
        for credential in [SESSION_CREDENTIAL, HOST_CREDENTIAL] {
            if let Err(err) = self.inner.credentials.delete(credential) {
                append_warning(&mut errors, err);
            }
        }
        state.signed_in = false;
        state.phase = ConnectionPhase::Disconnected;
        state.preferences = CloudConnectionPreferences::default();
        state.message = message.to_string();
        state.approval_url = None;
        state.last_error = errors;
        if let Err(err) = self.persist_preferences(&state.preferences) {
            append_warning(&mut state.last_error, err);
        }
    }

    fn fail_authorization_if_current(&self, generation: u64, message: &str, error: Option<String>) {
        let Ok(mut state) = self.inner.state.lock() else {
            return;
        };
        if state.generation != generation {
            return;
        }
        let mut error = error;
        if let Err(err) = self.inner.credentials.delete(SESSION_CREDENTIAL) {
            append_warning(&mut error, err);
        }
        state.phase = ConnectionPhase::Error;
        state.preferences.auto_start_enabled = false;
        state.message = message.to_string();
        state.approval_url = None;
        state.cancel = None;
        state.last_error = error;
        if let Err(err) = self.persist_preferences(&state.preferences) {
            append_warning(&mut state.last_error, err);
        }
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
        if phase != ConnectionPhase::Authorizing {
            state.approval_url = None;
        }
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
        state.approval_url = None;
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
    host_actor_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    host_org_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    account_email: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    org_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    host_id: Option<String>,
}

fn append_warning(current: &mut Option<String>, warning: String) {
    if let Some(current) = current {
        current.push(' ');
        current.push_str(&warning);
    } else {
        *current = Some(warning);
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

pub fn new_remote_runtime_secret() -> String {
    let mut raw = [0u8; 32];
    rand::rng().fill_bytes(&mut raw);
    format!("hrelay_{}", URL_SAFE_NO_PAD.encode(raw))
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

    async fn create_app_authorization(
        &self,
        token: &str,
    ) -> Result<AppAuthorization, CloudAPIError> {
        self.send(self.app_authorization_request(token)).await
    }

    fn app_authorization_request(&self, token: &str) -> reqwest::RequestBuilder {
        self.http
            .post(format!("{}/api/v1/app/authorizations", self.base_url))
            .header("x-hecate-app-version", &self.app_version)
            .timeout(Duration::from_secs(20))
            .json(&serde_json::json!({
                "token": token,
                "client": "desktop",
            }))
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
        self.send(request).await
    }

    async fn send<T>(&self, request: reqwest::RequestBuilder) -> Result<T, CloudAPIError>
    where
        T: DeserializeOwned,
    {
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

    fn is_retryable_authenticated_bootstrap(&self) -> bool {
        matches!(self.status, None | Some(408 | 429 | 500..=599))
    }

    fn authenticated_bootstrap_retry_reason(&self) -> Option<String> {
        if !self.is_retryable_authenticated_bootstrap() {
            return None;
        }
        Some(match self.status {
            Some(status) => format!("http_{status}"),
            None => "network".to_string(),
        })
    }
}

impl fmt::Display for CloudAPIError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(&self.message)
    }
}

#[derive(Debug)]
enum CloudBootstrapRequestError {
    Cancelled,
    Terminal(CloudAPIError),
}

#[derive(Debug)]
enum HostBootstrapError {
    Cancelled,
    TerminalCloud(CloudAPIError),
    Local(String),
}

async fn retry_cloud_bootstrap_request<T, Request, RequestFuture, IsCurrent, OnRetry>(
    stage: &'static str,
    initial_delay: Duration,
    max_delay: Duration,
    cancel_rx: &mut watch::Receiver<bool>,
    is_current: IsCurrent,
    mut on_retry: OnRetry,
    mut request: Request,
) -> Result<T, CloudBootstrapRequestError>
where
    Request: FnMut() -> RequestFuture,
    RequestFuture: std::future::Future<Output = Result<T, CloudAPIError>>,
    IsCurrent: Fn() -> bool,
    OnRetry: FnMut(&CloudAPIError, Duration),
{
    let mut delay = std::cmp::min(initial_delay, max_delay);
    loop {
        if *cancel_rx.borrow() || !is_current() {
            return Err(CloudBootstrapRequestError::Cancelled);
        }
        let result = tokio::select! {
            result = request() => result,
            changed = cancel_rx.changed() => {
                let _ = changed;
                return Err(CloudBootstrapRequestError::Cancelled);
            }
        };
        if *cancel_rx.borrow() || !is_current() {
            return Err(CloudBootstrapRequestError::Cancelled);
        }
        match result {
            Ok(value) => return Ok(value),
            Err(err) if err.is_retryable_authenticated_bootstrap() => {
                let reason = err
                    .authenticated_bootstrap_retry_reason()
                    .expect("retryable errors have a sanitized reason");
                log::warn!(
                    "remote access bootstrap retry stage={stage} reason={reason} retry_ms={}",
                    delay.as_millis()
                );
                on_retry(&err, delay);
                if *cancel_rx.borrow() || !is_current() {
                    return Err(CloudBootstrapRequestError::Cancelled);
                }
                tokio::select! {
                    _ = tokio::time::sleep(delay) => {}
                    changed = cancel_rx.changed() => {
                        let _ = changed;
                        return Err(CloudBootstrapRequestError::Cancelled);
                    }
                }
                if *cancel_rx.borrow() || !is_current() {
                    return Err(CloudBootstrapRequestError::Cancelled);
                }
                delay = std::cmp::min(delay.saturating_mul(2), max_delay);
            }
            Err(err) => return Err(CloudBootstrapRequestError::Terminal(err)),
        }
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
    id: String,
    email: String,
    org_id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AppAuthorization {
    authorization_id: String,
    approval_url: String,
    expires_at: String,
}

struct ValidatedAppAuthorization {
    approval_url: String,
    expires_in: Duration,
}

fn validated_app_authorization(
    authorization: &AppAuthorization,
    cloud_url: &str,
) -> Result<ValidatedAppAuthorization, String> {
    authorization
        .authorization_id
        .strip_prefix("appauth_")
        .filter(|suffix| {
            suffix.len() == 32
                && suffix
                    .bytes()
                    .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        })
        .ok_or_else(|| "Hecate Cloud returned an invalid authorization id.".to_string())?;
    let expires_in = authorization_expires_in(&authorization.expires_at)?;

    let cloud = reqwest::Url::parse(cloud_url)
        .map_err(|_| "The configured Hecate Cloud URL is invalid.".to_string())?;
    let approval = reqwest::Url::parse(&authorization.approval_url)
        .map_err(|_| "Hecate Cloud returned an invalid approval URL.".to_string())?;
    let same_origin = cloud.scheme() == approval.scheme()
        && cloud.host_str() == approval.host_str()
        && cloud.port_or_known_default() == approval.port_or_known_default();
    // Make every approval a full browser navigation. A fragment-only change
    // can leave Safari on an already-successful login document whose React
    // state never observes the new authorization. The request id is public;
    // the one-time browser ticket remains in the fragment.
    let expected_query = format!("request={}", authorization.authorization_id);
    if !same_origin
        || !approval.username().is_empty()
        || approval.password().is_some()
        || approval.path() != "/desktop-login"
        || approval.query() != Some(expected_query.as_str())
    {
        return Err("Hecate Cloud returned an untrusted approval URL.".to_string());
    }
    let fragment = approval
        .fragment()
        .ok_or_else(|| "Hecate Cloud returned an invalid approval URL.".to_string())?;
    let fields = fragment.split('&').collect::<Vec<_>>();
    let fragment_authorization_id = fields
        .first()
        .and_then(|field| field.strip_prefix("authorization_id="));
    let browser_ticket = fields
        .get(1)
        .and_then(|field| field.strip_prefix("browser_ticket="));
    let valid_browser_ticket = browser_ticket.is_some_and(|ticket| {
        ticket.len() == 48
            && ticket.starts_with("hbat_")
            && ticket[5..]
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
    });
    if fields.len() != 3
        || fragment_authorization_id != Some(authorization.authorization_id.as_str())
        || !valid_browser_ticket
        || fields[2] != "client=desktop"
    {
        return Err("Hecate Cloud returned an invalid approval URL.".to_string());
    }

    Ok(ValidatedAppAuthorization {
        approval_url: authorization.approval_url.clone(),
        expires_in,
    })
}

fn authorization_expires_in(raw: &str) -> Result<Duration, String> {
    let expires_at = chrono::DateTime::parse_from_rfc3339(raw)
        .map_err(|_| "Hecate Cloud returned an invalid authorization expiry.".to_string())?;
    let now_millis = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "The computer clock is unavailable.".to_string())?
        .as_millis() as i128;
    let remaining_millis = i128::from(expires_at.timestamp_millis()) - now_millis;
    if remaining_millis <= 0 || remaining_millis > MAX_LOGIN_TIMEOUT.as_millis() as i128 {
        return Err("Hecate Cloud returned an invalid authorization expiry.".to_string());
    }
    Ok(Duration::from_millis(remaining_millis as u64))
}

fn saved_host_owner_matches(
    actor_id: Option<&str>,
    org_id: Option<&str>,
    actor: &CloudActor,
) -> bool {
    actor_id == Some(actor.id.as_str()) && org_id == Some(actor.org_id.as_str())
}

async fn abandon_created_host(
    client: &CloudClient,
    session_token: &str,
    actor: &CloudActor,
    host_id: &str,
) {
    if let Err(err) = client
        .revoke_host(session_token, &actor.org_id, host_id)
        .await
    {
        log::warn!("could not revoke a cancelled remote access host: {err}");
    }
}

#[derive(Clone)]
struct RelayAuthorization {
    runtime_secret: HeaderValue,
    actor_id: HeaderValue,
    org_id: HeaderValue,
    runtime_id: HeaderValue,
}

impl RelayAuthorization {
    fn new(secret: &str, actor_id: &str, org_id: &str, runtime_id: &str) -> Result<Self, String> {
        let mut runtime_secret = relay_header_value(secret, "connector secret")?;
        runtime_secret.set_sensitive(true);
        Ok(Self {
            runtime_secret,
            actor_id: relay_header_value(actor_id, "Cloud actor id")?,
            org_id: relay_header_value(org_id, "Cloud organization id")?,
            runtime_id: relay_header_value(runtime_id, "Cloud host id")?,
        })
    }

    fn apply(&self, builder: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        builder
            .header(REMOTE_RUNTIME_SECRET_HEADER, self.runtime_secret.clone())
            .header(REMOTE_ACTOR_ID_HEADER, self.actor_id.clone())
            .header(REMOTE_ORG_ID_HEADER, self.org_id.clone())
            .header(REMOTE_RUNTIME_ID_HEADER, self.runtime_id.clone())
    }
}

fn relay_header_value(value: &str, label: &str) -> Result<HeaderValue, String> {
    let value = value.trim();
    if value.is_empty() {
        return Err(format!("{label} is missing."));
    }
    HeaderValue::from_str(value).map_err(|_| format!("{label} is invalid."))
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
    authorization: RelayAuthorization,
    cancel_rx: watch::Receiver<bool>,
    runtime_event_cursor: Option<RuntimeEventCursor>,
    on_connected: F,
) -> Result<(), String>
where
    F: FnOnce(),
{
    run_relay_with_watchdog(
        client,
        host_id,
        host_token,
        local_base_url,
        authorization,
        cancel_rx,
        runtime_event_cursor,
        RelayWatchdog {
            ping_interval: RELAY_PING_INTERVAL,
            read_idle_timeout: RELAY_READ_IDLE_TIMEOUT,
        },
        on_connected,
    )
    .await
}

#[derive(Clone, Copy)]
struct RelayWatchdog {
    ping_interval: Duration,
    read_idle_timeout: Duration,
}

async fn run_relay_with_watchdog<F>(
    client: &CloudClient,
    host_id: &str,
    host_token: &str,
    local_base_url: &str,
    authorization: RelayAuthorization,
    mut cancel_rx: watch::Receiver<bool>,
    runtime_event_cursor: Option<RuntimeEventCursor>,
    watchdog: RelayWatchdog,
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
    let (runtime_event_ack_tx, runtime_event_ack_rx) = mpsc::channel::<RuntimeEventAck>(16);
    if let Some(cursor) = runtime_event_cursor {
        let http = client.http.clone();
        let local_base_url = local_base_url.to_string();
        let authorization = authorization.clone();
        let outgoing = outgoing_tx.clone();
        tauri::async_runtime::spawn(async move {
            forward_runtime_notification_events(
                &http,
                &local_base_url,
                &authorization,
                cursor,
                outgoing,
                runtime_event_ack_rx,
            )
            .await;
        });
    }
    let permits = Arc::new(Semaphore::new(RELAY_MAX_CONCURRENCY));
    let mut heartbeat = tokio::time::interval_at(
        tokio::time::Instant::now() + watchdog.ping_interval,
        watchdog.ping_interval,
    );
    heartbeat.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let read_idle = tokio::time::sleep(watchdog.read_idle_timeout);
    tokio::pin!(read_idle);
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
            _ = heartbeat.tick() => {
                sink.send(Message::Ping(Vec::new().into()))
                    .await
                    .map_err(|err| format!("Remote access heartbeat failed: {err}"))?;
            }
            _ = &mut read_idle => {
                return Err("Remote access heartbeat timed out.".to_string());
            }
            incoming = stream.next() => {
                read_idle
                    .as_mut()
                    .reset(tokio::time::Instant::now() + watchdog.read_idle_timeout);
                match incoming {
                    Some(Ok(Message::Text(payload))) => {
                        if let Some(ack) = runtime_event_ack(payload.as_str()) {
                            let _ = runtime_event_ack_tx.try_send(ack);
                            continue;
                        }
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
                        let authorization = authorization.clone();
                        let outgoing = outgoing_tx.clone();
                        tauri::async_runtime::spawn(async move {
                            let _permit = permit;
                            handle_relay_payload(
                                &http,
                                &local,
                                &authorization,
                                payload.as_str(),
                                &outgoing,
                            )
                            .await;
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

#[derive(Deserialize)]
struct RuntimeEventsResponse {
    #[serde(default)]
    data: Vec<RuntimeEventEnvelope>,
}

#[derive(Clone, Deserialize)]
struct RuntimeEventEnvelope {
    #[serde(default)]
    schema_version: String,
    #[serde(default)]
    event_id: String,
    #[serde(default)]
    task_id: String,
    #[serde(default)]
    run_id: String,
    #[serde(default)]
    sequence: i64,
    #[serde(default)]
    occurred_at: String,
    #[serde(default, rename = "type")]
    kind: String,
    #[serde(default)]
    data: RuntimeEventData,
}

#[derive(Clone, Default, Deserialize)]
struct RuntimeEventData {
    #[serde(default)]
    approval_id: String,
}

#[derive(Serialize)]
struct RelayRuntimeEventFrame {
    #[serde(rename = "type")]
    kind: &'static str,
    event: RelayRuntimeEvent,
}

#[derive(Serialize)]
struct RelayRuntimeEvent {
    schema_version: &'static str,
    event_id: String,
    #[serde(rename = "type")]
    kind: String,
    occurred_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    task_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    run_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    session_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    approval_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    outcome: Option<&'static str>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RuntimeEventAck {
    #[serde(rename = "type")]
    kind: String,
    event_id: String,
    status: String,
}

#[derive(Deserialize)]
struct ChatSessionsResponse {
    #[serde(default)]
    data: Vec<ChatSessionSummary>,
}

#[derive(Deserialize)]
struct ChatSessionSummary {
    #[serde(default)]
    id: String,
    #[serde(default)]
    status: String,
}

#[derive(Deserialize)]
struct ChatApprovalsResponse {
    #[serde(default)]
    data: Vec<ChatPendingApproval>,
}

#[derive(Clone, Deserialize)]
struct ChatPendingApproval {
    #[serde(default)]
    id: String,
    #[serde(default)]
    session_id: String,
    #[serde(default)]
    status: String,
    #[serde(default)]
    created_at: String,
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

async fn forward_runtime_notification_events(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
    cursor: RuntimeEventCursor,
    outgoing: mpsc::Sender<String>,
    mut acknowledgements: mpsc::Receiver<RuntimeEventAck>,
) {
    let mut runtime_poll = tokio::time::interval(RUNTIME_EVENT_POLL_INTERVAL);
    runtime_poll.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let mut chat_approval_poll = tokio::time::interval(CHAT_APPROVAL_POLL_INTERVAL);
    chat_approval_poll.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            _ = outgoing.closed() => return,
            _ = runtime_poll.tick() => {
                if sync_runtime_notification_event_page(
                    http,
                    local_base_url,
                    authorization,
                    &cursor,
                    &outgoing,
                    &mut acknowledgements,
                )
                .await
                .is_err()
                {
                    // This is a best-effort companion signal. The authenticated HTTP
                    // relay remains authoritative, and the cursor is left in place so
                    // the next poll can retry without logging event contents.
                    log::debug!("runtime notification event sync is waiting for the local feed");
                }
            }
            _ = chat_approval_poll.tick() => {
                if sync_chat_approval_notifications(
                    http,
                    local_base_url,
                    authorization,
                    &cursor,
                    &outgoing,
                    &mut acknowledgements,
                )
                .await
                .is_err()
                {
                    log::debug!("chat approval notification sync is waiting for the local feed");
                }
            }
        }
    }
}

async fn sync_chat_approval_notifications(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
    cursor: &RuntimeEventCursor,
    outgoing: &mpsc::Sender<String>,
    acknowledgements: &mut mpsc::Receiver<RuntimeEventAck>,
) -> Result<(), ()> {
    // External-agent approvals are durable per session but are not part of the
    // task event feed. Reconcile only currently actionable rows. We do not
    // infer Chat completion from mutable session timestamps; a future unified
    // operator-notification log should own that signal.
    let sessions = fetch_chat_sessions(http, local_base_url, authorization).await?;
    let active_sessions = sessions
        .data
        .into_iter()
        .filter(|session| {
            runtime_event_id_is_safe(&session.id)
                && matches!(session.status.as_str(), "running" | "awaiting_approval")
        })
        .take(CHAT_APPROVAL_ACTIVE_SESSION_LIMIT + 1)
        .collect::<Vec<_>>();
    if active_sessions.len() > CHAT_APPROVAL_ACTIVE_SESSION_LIMIT {
        return Err(());
    }
    let mut pending = HashMap::<String, ChatPendingApproval>::new();
    for session in active_sessions {
        let approvals =
            fetch_chat_approvals(http, local_base_url, authorization, &session.id).await?;
        for approval in approvals.data {
            if approval.status != "pending"
                || approval.session_id != session.id
                || !runtime_event_id_is_safe(&approval.id)
                || !runtime_event_id_is_safe(&approval.session_id)
                || !runtime_event_timestamp_is_safe(&approval.created_at)
            {
                continue;
            }
            if pending.len() >= CHAT_APPROVAL_PENDING_LIMIT {
                return Err(());
            }
            pending.insert(approval.id.clone(), approval);
        }
    }

    let seen = cursor
        .lock()
        .map(|state| state.chat_approval_ids.clone())
        .map_err(|_| ())?;
    let mut new_approvals = pending
        .values()
        .filter(|approval| !seen.contains(&approval.id))
        .cloned()
        .collect::<Vec<_>>();
    new_approvals.sort_by(|left, right| left.created_at.cmp(&right.created_at));
    for approval in new_approvals {
        if let Some(frame) = chat_approval_notification_frame(&approval) {
            send_runtime_notification_event(frame, outgoing, acknowledgements).await?;
            cursor
                .lock()
                .map_err(|_| ())?
                .chat_approval_ids
                .insert(approval.id);
        }
    }

    let current_ids = pending.into_keys().collect::<HashSet<_>>();
    cursor
        .lock()
        .map_err(|_| ())?
        .chat_approval_ids
        .retain(|approval_id| current_ids.contains(approval_id));
    Ok(())
}

async fn fetch_chat_sessions(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
) -> Result<ChatSessionsResponse, ()> {
    let target = local_request_url(local_base_url, "/hecate/v1/chat/sessions").map_err(|_| ())?;
    let response = authorization
        .apply(http.get(target))
        .timeout(Duration::from_secs(10))
        .send()
        .await
        .map_err(|_| ())?;
    decode_bounded_local_json(response, RUNTIME_EVENT_MAX_RESPONSE_BODY).await
}

async fn fetch_chat_approvals(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
    session_id: &str,
) -> Result<ChatApprovalsResponse, ()> {
    if !runtime_event_id_is_safe(session_id) {
        return Err(());
    }
    let path = format!("/hecate/v1/chat/sessions/{session_id}/approvals");
    let mut target = local_request_url(local_base_url, &path).map_err(|_| ())?;
    target.query_pairs_mut().append_pair("status", "pending");
    let response = authorization
        .apply(http.get(target))
        .timeout(Duration::from_secs(10))
        .send()
        .await
        .map_err(|_| ())?;
    decode_bounded_local_json(response, RUNTIME_EVENT_MAX_RESPONSE_BODY).await
}

async fn decode_bounded_local_json<T: DeserializeOwned>(
    mut response: reqwest::Response,
    max_body: usize,
) -> Result<T, ()> {
    if !response.status().is_success() {
        return Err(());
    }
    let mut body = Vec::new();
    loop {
        match response.chunk().await {
            Ok(Some(chunk)) if body.len().saturating_add(chunk.len()) <= max_body => {
                body.extend_from_slice(&chunk);
            }
            Ok(Some(_)) | Err(_) => return Err(()),
            Ok(None) => break,
        }
    }
    serde_json::from_slice(&body).map_err(|_| ())
}

fn chat_approval_notification_frame(
    approval: &ChatPendingApproval,
) -> Option<RelayRuntimeEventFrame> {
    if approval.status != "pending"
        || !runtime_event_id_is_safe(&approval.id)
        || !runtime_event_id_is_safe(&approval.session_id)
        || !runtime_event_timestamp_is_safe(&approval.created_at)
    {
        return None;
    }
    let event_id = format!("chat_{}", approval.id);
    if !runtime_event_id_is_safe(&event_id) {
        return None;
    }
    Some(RelayRuntimeEventFrame {
        kind: "runtime_event",
        event: RelayRuntimeEvent {
            schema_version: "1",
            event_id,
            kind: "approval.requested".to_string(),
            occurred_at: approval.created_at.clone(),
            task_id: None,
            run_id: None,
            session_id: Some(approval.session_id.clone()),
            approval_id: Some(approval.id.clone()),
            outcome: None,
        },
    })
}

async fn sync_runtime_notification_event_page(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
    cursor: &RuntimeEventCursor,
    outgoing: &mpsc::Sender<String>,
    acknowledgements: &mut mpsc::Receiver<RuntimeEventAck>,
) -> Result<(), ()> {
    let (after_sequence, initialized) = cursor
        .lock()
        .map(|state| (state.after_sequence, state.initialized))
        .map_err(|_| ())?;
    let page =
        fetch_runtime_notification_event_page(http, local_base_url, authorization, after_sequence)
            .await?;

    if !initialized {
        let mut pending = Vec::new();
        {
            let mut state = cursor.lock().map_err(|_| ())?;
            for event in page.data {
                if event.sequence <= state.after_sequence {
                    continue;
                }
                match event.kind.as_str() {
                    "approval.requested" if runtime_event_id_is_safe(&event.data.approval_id) => {
                        state
                            .pending_approvals
                            .insert(event.data.approval_id.clone(), event.clone());
                    }
                    "approval.resolved" if runtime_event_id_is_safe(&event.data.approval_id) => {
                        state.pending_approvals.remove(&event.data.approval_id);
                    }
                    _ => {}
                }
                state.after_sequence = event.sequence;
            }
            if page.data_len < RUNTIME_EVENT_PAGE_LIMIT {
                pending.extend(state.pending_approvals.values().cloned());
            }
        }
        if page.data_len >= RUNTIME_EVENT_PAGE_LIMIT {
            return Ok(());
        }
        pending.sort_by_key(|event| event.sequence);
        for event in pending {
            if let Some(frame) = runtime_notification_frame(event) {
                send_runtime_notification_event(frame, outgoing, acknowledgements).await?;
            }
        }
        let mut state = cursor.lock().map_err(|_| ())?;
        state.initialized = true;
        state.pending_approvals.clear();
        return Ok(());
    }

    for event in page.data {
        let sequence = event.sequence;
        if sequence <= after_sequence {
            continue;
        }
        if let Some(frame) = runtime_notification_frame(event) {
            send_runtime_notification_event(frame, outgoing, acknowledgements).await?;
        }
        let mut state = cursor.lock().map_err(|_| ())?;
        state.after_sequence = state.after_sequence.max(sequence);
    }
    Ok(())
}

async fn send_runtime_notification_event(
    frame: RelayRuntimeEventFrame,
    outgoing: &mpsc::Sender<String>,
    acknowledgements: &mut mpsc::Receiver<RuntimeEventAck>,
) -> Result<(), ()> {
    let event_id = frame.event.event_id.clone();
    let payload = serde_json::to_string(&frame).map_err(|_| ())?;
    outgoing.send(payload).await.map_err(|_| ())?;
    let timeout = tokio::time::sleep(RUNTIME_EVENT_ACK_TIMEOUT);
    tokio::pin!(timeout);
    loop {
        tokio::select! {
            _ = outgoing.closed() => return Err(()),
            _ = &mut timeout => return Err(()),
            acknowledgement = acknowledgements.recv() => {
                let acknowledgement = acknowledgement.ok_or(())?;
                if acknowledgement.event_id != event_id {
                    continue;
                }
                return match acknowledgement.status.as_str() {
                    "accepted" | "duplicate" | "permanent_rejection" => Ok(()),
                    _ => Err(()),
                };
            }
        }
    }
}

struct RuntimeEventPage {
    data: Vec<RuntimeEventEnvelope>,
    data_len: usize,
}

async fn fetch_runtime_notification_event_page(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
    after_sequence: i64,
) -> Result<RuntimeEventPage, ()> {
    let mut target = local_request_url(local_base_url, "/hecate/v1/events").map_err(|_| ())?;
    target
        .query_pairs_mut()
        .append_pair(
            "event_type",
            "approval.requested,approval.resolved,run.finished,run.failed,run.cancelled",
        )
        .append_pair("after_sequence", &after_sequence.max(0).to_string())
        .append_pair("limit", &RUNTIME_EVENT_PAGE_LIMIT.to_string());
    let mut response = authorization
        .apply(http.get(target))
        .timeout(Duration::from_secs(10))
        .send()
        .await
        .map_err(|_| ())?;
    if !response.status().is_success() {
        return Err(());
    }
    let mut body = Vec::new();
    loop {
        match response.chunk().await {
            Ok(Some(chunk))
                if body.len().saturating_add(chunk.len()) <= RUNTIME_EVENT_MAX_RESPONSE_BODY =>
            {
                body.extend_from_slice(&chunk);
            }
            Ok(Some(_)) | Err(_) => return Err(()),
            Ok(None) => break,
        }
    }
    let response: RuntimeEventsResponse = serde_json::from_slice(&body).map_err(|_| ())?;
    let data_len = response.data.len();
    Ok(RuntimeEventPage {
        data: response.data,
        data_len,
    })
}

fn runtime_notification_frame(event: RuntimeEventEnvelope) -> Option<RelayRuntimeEventFrame> {
    if event.schema_version != "1"
        || !runtime_event_id_is_safe(&event.event_id)
        || !runtime_event_id_is_safe(&event.task_id)
        || !runtime_event_id_is_safe(&event.run_id)
        || !runtime_event_timestamp_is_safe(&event.occurred_at)
    {
        return None;
    }
    let (approval_id, outcome) = match event.kind.as_str() {
        "approval.requested" if runtime_event_id_is_safe(&event.data.approval_id) => {
            (Some(event.data.approval_id), None)
        }
        "run.finished" => (None, Some("completed")),
        "run.failed" => (None, Some("failed")),
        "run.cancelled" => (None, Some("cancelled")),
        _ => return None,
    };
    Some(RelayRuntimeEventFrame {
        kind: "runtime_event",
        event: RelayRuntimeEvent {
            schema_version: "1",
            event_id: event.event_id,
            kind: event.kind,
            occurred_at: event.occurred_at,
            task_id: Some(event.task_id),
            run_id: Some(event.run_id),
            session_id: None,
            approval_id,
            outcome,
        },
    })
}

fn runtime_event_id_is_safe(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty()
        && value.len() <= 160
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.'))
}

fn runtime_event_timestamp_is_safe(value: &str) -> bool {
    let value = value.trim();
    !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|byte| {
            byte.is_ascii_digit() || matches!(byte, b'-' | b':' | b'.' | b'+' | b'T' | b'Z')
        })
}

fn runtime_event_ack(payload: &str) -> Option<RuntimeEventAck> {
    let acknowledgement = serde_json::from_str::<RuntimeEventAck>(payload).ok()?;
    if acknowledgement.kind != "runtime_event_ack"
        || !runtime_event_id_is_safe(&acknowledgement.event_id)
        || !matches!(
            acknowledgement.status.as_str(),
            "accepted" | "duplicate" | "permanent_rejection"
        )
    {
        return None;
    }
    Some(acknowledgement)
}

async fn handle_relay_payload(
    http: &reqwest::Client,
    local_base_url: &str,
    authorization: &RelayAuthorization,
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
            let response =
                execute_status_request(http, local_base_url, authorization, request).await;
            let _ = outgoing.send(response).await;
        }
        "http_proxy_request" => {
            execute_proxy_request(http, local_base_url, authorization, request, outgoing).await;
        }
        // Cloud may acknowledge a client-originated runtime event. Delivery is
        // idempotent by event_id and does not block the HTTP relay, so the
        // desktop currently treats acknowledgements as advisory.
        "runtime_event_ack" => {}
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
    authorization: &RelayAuthorization,
    request: RelayRequest,
) -> String {
    let method = request.method.trim().to_ascii_uppercase();
    let path = request.path.trim();
    if !matches!(method.as_str(), "GET" | "POST") || !proxy_path_allowed(path) {
        return relay_error_response(&request.id, 403, "This remote route is not available.");
    }
    let body = match decode_request_body(&method, &request.body_base64, RELAY_MAX_REQUEST_BODY) {
        Ok(body) => body,
        Err(response) => return relay_error_response(&request.id, response.0, response.1),
    };
    execute_buffered_request(
        http,
        local_base_url,
        authorization,
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
    authorization: &RelayAuthorization,
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
    let mut builder = authorization.apply(http.request(reqwest_method, target));
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
    authorization: &RelayAuthorization,
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
    let mut builder = apply_proxy_headers(
        authorization.apply(http.request(reqwest_method, target)),
        headers,
    )
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

    fn authorization_expiry_after(duration: Duration) -> String {
        chrono::DateTime::<chrono::Utc>::from(SystemTime::now() + duration)
            .to_rfc3339_opts(chrono::SecondsFormat::Millis, true)
    }

    fn desktop_app_authorization() -> AppAuthorization {
        AppAuthorization {
            authorization_id: "appauth_0123456789abcdef0123456789abcdef".to_string(),
            approval_url: format!(
                "https://console.example.test/desktop-login?request=appauth_0123456789abcdef0123456789abcdef#authorization_id=appauth_0123456789abcdef0123456789abcdef&browser_ticket=hbat_{}&client=desktop",
                "a".repeat(43),
            ),
            expires_at: authorization_expiry_after(Duration::from_secs(5 * 60)),
        }
    }

    fn relay_test_client(address: std::net::SocketAddr) -> CloudClient {
        CloudClient::new(
            format!("http://{address}"),
            reqwest::Client::new(),
            "0.5.0-test",
        )
    }

    fn relay_test_authorization() -> RelayAuthorization {
        RelayAuthorization::new(
            "desktop-relay-secret-123456",
            "actor_test",
            "org_test",
            "host_test",
        )
        .expect("relay authorization")
    }

    fn bootstrap_test_error(status: Option<u16>, message: &str) -> CloudAPIError {
        CloudAPIError {
            status,
            message: message.to_string(),
        }
    }

    #[test]
    fn authenticated_bootstrap_retry_classification_is_fail_closed_for_client_errors() {
        for status in [None, Some(408), Some(429), Some(500), Some(503), Some(599)] {
            assert!(
                bootstrap_test_error(status, "transient").is_retryable_authenticated_bootstrap(),
                "expected {status:?} to retry"
            );
        }
        for status in [
            Some(200),
            Some(301),
            Some(400),
            Some(401),
            Some(403),
            Some(404),
            Some(422),
        ] {
            assert!(
                !bootstrap_test_error(status, "terminal").is_retryable_authenticated_bootstrap(),
                "expected {status:?} to stop"
            );
        }
    }

    #[test]
    fn authenticated_bootstrap_retry_log_reason_never_uses_remote_error_text() {
        let network = bootstrap_test_error(None, "token=happ_secret");
        let unavailable = bootstrap_test_error(Some(503), "upstream body with credentials");

        assert_eq!(
            network.authenticated_bootstrap_retry_reason().as_deref(),
            Some("network")
        );
        assert_eq!(
            unavailable
                .authenticated_bootstrap_retry_reason()
                .as_deref(),
            Some("http_503")
        );
    }

    #[tokio::test(flavor = "current_thread")]
    async fn authenticated_bootstrap_retries_transient_failures_with_bounded_backoff() {
        let (_cancel_tx, mut cancel_rx) = watch::channel(false);
        let mut attempts = 0;
        let mut retry_delays = Vec::new();

        let result = retry_cloud_bootstrap_request(
            "test",
            Duration::from_millis(1),
            Duration::from_millis(4),
            &mut cancel_rx,
            || true,
            |_, delay| retry_delays.push(delay),
            || {
                attempts += 1;
                std::future::ready(match attempts {
                    1 => Err(bootstrap_test_error(None, "network")),
                    2 => Err(bootstrap_test_error(Some(429), "rate limited")),
                    3 => Err(bootstrap_test_error(Some(503), "unavailable")),
                    _ => Ok("connected"),
                })
            },
        )
        .await
        .expect("transient failures should recover");

        assert_eq!(result, "connected");
        assert_eq!(attempts, 4);
        assert_eq!(
            retry_delays,
            [
                Duration::from_millis(1),
                Duration::from_millis(2),
                Duration::from_millis(4),
            ]
        );
    }

    #[tokio::test(flavor = "current_thread")]
    async fn authenticated_bootstrap_keeps_401_and_403_terminal() {
        for status in [401, 403] {
            let (_cancel_tx, mut cancel_rx) = watch::channel(false);
            let mut attempts = 0;
            let mut retries = 0;

            let result: Result<(), CloudBootstrapRequestError> = retry_cloud_bootstrap_request(
                "test",
                Duration::from_secs(60),
                Duration::from_secs(60),
                &mut cancel_rx,
                || true,
                |_, _| retries += 1,
                || {
                    attempts += 1;
                    std::future::ready(Err(bootstrap_test_error(Some(status), "terminal")))
                },
            )
            .await;

            match result {
                Err(CloudBootstrapRequestError::Terminal(err)) => {
                    assert_eq!(err.status, Some(status));
                }
                other => panic!("expected terminal HTTP {status}, got {other:?}"),
            }
            assert_eq!(attempts, 1);
            assert_eq!(retries, 0);
        }
    }

    #[tokio::test(flavor = "current_thread")]
    async fn authenticated_bootstrap_cancellation_interrupts_an_inflight_request() {
        let (cancel_tx, mut cancel_rx) = watch::channel(false);
        let retry = retry_cloud_bootstrap_request(
            "test",
            Duration::from_secs(60),
            Duration::from_secs(60),
            &mut cancel_rx,
            || true,
            |_, _| panic!("pending request must not reach retry"),
            || std::future::pending::<Result<(), CloudAPIError>>(),
        );
        let cancel = async move {
            tokio::task::yield_now().await;
            cancel_tx.send(true).expect("cancel bootstrap");
        };

        let (result, ()) = tokio::join!(retry, cancel);
        assert!(matches!(result, Err(CloudBootstrapRequestError::Cancelled)));
    }

    #[tokio::test(flavor = "current_thread")]
    async fn authenticated_bootstrap_stops_before_backoff_when_generation_is_stale() {
        let (_cancel_tx, mut cancel_rx) = watch::channel(false);
        let current = std::cell::Cell::new(true);
        let mut attempts = 0;

        let result: Result<(), CloudBootstrapRequestError> = retry_cloud_bootstrap_request(
            "test",
            Duration::from_secs(60),
            Duration::from_secs(60),
            &mut cancel_rx,
            || current.get(),
            |_, _| current.set(false),
            || {
                attempts += 1;
                std::future::ready(Err(bootstrap_test_error(None, "network")))
            },
        )
        .await;

        assert!(matches!(result, Err(CloudBootstrapRequestError::Cancelled)));
        assert_eq!(attempts, 1);
    }

    #[tokio::test(flavor = "current_thread")]
    async fn relay_watchdog_sends_pings_and_keeps_a_responsive_peer_connected() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("relay listener");
        let address = listener.local_addr().expect("relay address");
        let (cancel_tx, cancel_rx) = watch::channel(false);
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.expect("relay connection");
            let mut websocket = tokio_tungstenite::accept_async(stream)
                .await
                .expect("relay handshake");
            let mut ping_count = 0;
            while let Some(message) = websocket.next().await {
                match message.expect("relay frame") {
                    Message::Ping(payload) => {
                        ping_count += 1;
                        websocket
                            .send(Message::Pong(payload))
                            .await
                            .expect("relay pong");
                        if ping_count == 6 {
                            cancel_tx.send(true).expect("cancel relay");
                        }
                    }
                    Message::Close(_) => break,
                    _ => {}
                }
            }
            ping_count
        });

        let result = tokio::time::timeout(
            Duration::from_secs(2),
            run_relay_with_watchdog(
                &relay_test_client(address),
                "host_test",
                "host-token-test",
                "http://127.0.0.1:9",
                relay_test_authorization(),
                cancel_rx,
                None,
                RelayWatchdog {
                    ping_interval: Duration::from_millis(20),
                    read_idle_timeout: Duration::from_millis(100),
                },
                || {},
            ),
        )
        .await
        .expect("responsive relay should finish promptly");

        assert_eq!(result, Ok(()));
        assert_eq!(server.await.expect("relay server"), 6);
    }

    #[tokio::test(flavor = "current_thread")]
    async fn relay_watchdog_reconnects_when_the_peer_stops_sending_frames() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("relay listener");
        let address = listener.local_addr().expect("relay address");
        let server = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.expect("relay connection");
            let _websocket = tokio_tungstenite::accept_async(stream)
                .await
                .expect("relay handshake");
            std::future::pending::<()>().await;
        });
        let (_cancel_tx, cancel_rx) = watch::channel(false);

        let result = tokio::time::timeout(
            Duration::from_secs(2),
            run_relay_with_watchdog(
                &relay_test_client(address),
                "host_test",
                "host-token-test",
                "http://127.0.0.1:9",
                relay_test_authorization(),
                cancel_rx,
                None,
                RelayWatchdog {
                    ping_interval: Duration::from_millis(20),
                    read_idle_timeout: Duration::from_millis(100),
                },
                || {},
            ),
        )
        .await
        .expect("silent relay should hit its read deadline")
        .expect_err("silent relay must reconnect");

        assert_eq!(result, "Remote access heartbeat timed out.");
        server.abort();
    }

    #[test]
    fn runtime_notification_frames_are_minimal_and_allowlisted() {
        let frame = runtime_notification_frame(RuntimeEventEnvelope {
            schema_version: "1".to_string(),
            event_id: "evt_01JSAFE".to_string(),
            task_id: "task_1".to_string(),
            run_id: "run_1".to_string(),
            sequence: 42,
            occurred_at: "2026-07-23T12:34:56.123Z".to_string(),
            kind: "approval.requested".to_string(),
            data: RuntimeEventData {
                approval_id: "approval_1".to_string(),
            },
        })
        .expect("allowlisted approval event");
        let value = serde_json::to_value(frame).expect("runtime event frame serializes");

        assert_eq!(value["type"], "runtime_event");
        assert_eq!(value["event"]["type"], "approval.requested");
        assert_eq!(value["event"]["approval_id"], "approval_1");
        assert!(value["event"].get("data").is_none());
        assert!(value["event"].get("title").is_none());
        assert!(value["event"].get("body").is_none());

        let failed = runtime_notification_frame(RuntimeEventEnvelope {
            schema_version: "1".to_string(),
            event_id: "evt_01JFAILED".to_string(),
            task_id: "task_1".to_string(),
            run_id: "run_1".to_string(),
            sequence: 43,
            occurred_at: "2026-07-23T12:35:56Z".to_string(),
            kind: "run.failed".to_string(),
            data: RuntimeEventData::default(),
        })
        .expect("allowlisted terminal run event");
        let failed = serde_json::to_value(failed).expect("terminal frame serializes");
        assert_eq!(failed["event"]["outcome"], "failed");
        assert!(failed["event"].get("approval_id").is_none());
    }

    #[test]
    fn runtime_notification_frames_reject_untrusted_identifiers_and_types() {
        let event = |event_id: &str, kind: &str| RuntimeEventEnvelope {
            schema_version: "1".to_string(),
            event_id: event_id.to_string(),
            task_id: "task_1".to_string(),
            run_id: "run_1".to_string(),
            sequence: 42,
            occurred_at: "2026-07-23T12:34:56Z".to_string(),
            kind: kind.to_string(),
            data: RuntimeEventData {
                approval_id: "approval_1".to_string(),
            },
        };

        assert!(
            runtime_notification_frame(event("evt with spaces", "approval.requested")).is_none()
        );
        assert!(runtime_notification_frame(event("evt_1", "assistant.final_answer")).is_none());
    }

    #[test]
    fn chat_approval_notification_frames_expose_only_opaque_identity() {
        let frame = chat_approval_notification_frame(&ChatPendingApproval {
            id: "approval_chat_1".to_string(),
            session_id: "chat_1".to_string(),
            status: "pending".to_string(),
            created_at: "2026-07-23T12:34:56Z".to_string(),
        })
        .expect("pending chat approval frame");
        let value = serde_json::to_value(frame).expect("chat approval frame serializes");

        assert_eq!(value["event"]["event_id"], "chat_approval_chat_1");
        assert_eq!(value["event"]["session_id"], "chat_1");
        assert_eq!(value["event"]["approval_id"], "approval_chat_1");
        assert!(value["event"].get("task_id").is_none());
        assert!(value["event"].get("run_id").is_none());
        assert!(value["event"].get("tool_name").is_none());
        assert!(value["event"].get("workspace").is_none());
    }

    #[test]
    fn runtime_event_acknowledgements_are_strictly_validated() {
        let acknowledgement = runtime_event_ack(
            r#"{"type":"runtime_event_ack","event_id":"evt_01JSAFE","status":"accepted"}"#,
        )
        .expect("valid runtime event acknowledgement");
        assert_eq!(acknowledgement.event_id, "evt_01JSAFE");
        assert_eq!(acknowledgement.status, "accepted");

        assert!(runtime_event_ack(
            r#"{"type":"runtime_event_ack","event_id":"evt_01JSAFE","status":"permanent_rejection"}"#
        )
        .is_some());

        assert!(runtime_event_ack(
            r#"{"type":"runtime_event_ack","event_id":"evt unsafe","status":"accepted"}"#
        )
        .is_none());
        assert!(runtime_event_ack(
            r#"{"type":"runtime_event_ack","event_id":"evt_01JSAFE","status":"retry"}"#
        )
        .is_none());
        assert!(runtime_event_ack(
            r#"{"type":"runtime_event_ack","event_id":"evt_01JSAFE","status":"rejected"}"#
        )
        .is_none());
    }

    #[tokio::test(flavor = "current_thread")]
    async fn runtime_event_delivery_waits_for_a_durable_cloud_acknowledgement() {
        let frame = runtime_notification_frame(RuntimeEventEnvelope {
            schema_version: "1".to_string(),
            event_id: "evt_01JACKED".to_string(),
            task_id: "task_1".to_string(),
            run_id: "run_1".to_string(),
            sequence: 44,
            occurred_at: "2026-07-23T12:36:56Z".to_string(),
            kind: "run.finished".to_string(),
            data: RuntimeEventData::default(),
        })
        .expect("terminal runtime event frame");
        let (outgoing_tx, mut outgoing_rx) = mpsc::channel(1);
        let (acknowledgement_tx, mut acknowledgement_rx) = mpsc::channel(1);
        let delivery = tokio::spawn(async move {
            send_runtime_notification_event(frame, &outgoing_tx, &mut acknowledgement_rx).await
        });

        let payload = outgoing_rx.recv().await.expect("runtime event payload");
        assert!(payload.contains("evt_01JACKED"));
        tokio::task::yield_now().await;
        assert!(
            !delivery.is_finished(),
            "delivery advanced before Cloud ack"
        );

        acknowledgement_tx
            .send(RuntimeEventAck {
                kind: "runtime_event_ack".to_string(),
                event_id: "evt_01JACKED".to_string(),
                status: "accepted".to_string(),
            })
            .await
            .expect("send durable ack");
        assert_eq!(delivery.await.expect("delivery task"), Ok(()));
    }

    #[test]
    fn app_tokens_are_random_bearer_tokens() {
        let first = new_app_token().expect("first token");
        let second = new_app_token().expect("second token");
        assert!(first.starts_with("happ_"));
        assert_eq!(first.len(), 48);
        assert!(first[5..]
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_')));
        assert_ne!(first, second);
    }

    #[test]
    fn app_authorization_accepts_only_bound_desktop_approval_urls() {
        let mut authorization = desktop_app_authorization();
        let validated = validated_app_authorization(&authorization, "https://console.example.test")
            .expect("valid authorization");
        assert_eq!(validated.approval_url, authorization.approval_url);
        assert!(validated.expires_in > Duration::from_secs(4 * 60));

        authorization.approval_url =
            authorization
                .approval_url
                .replacen("console.example.test", "attacker.example", 1);
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            "#authorization_id=appauth_0123456789abcdef0123456789abcdef",
            "#authorization_id=appauth_ffffffffffffffffffffffffffffffff",
            1,
        );
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url =
            authorization
                .approval_url
                .replacen("&client=desktop", "&client=mobile", 1);
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            &format!("hbat_{}", "a".repeat(43)),
            "hbat_too-short",
            1,
        );
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            "?request=appauth_0123456789abcdef0123456789abcdef#",
            "#",
            1,
        );
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            "?request=appauth_0123456789abcdef0123456789abcdef#",
            "?request=appauth_ffffffffffffffffffffffffffffffff#",
            1,
        );
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            "?request=appauth_0123456789abcdef0123456789abcdef#",
            "?request=appauth_0123456789abcdef0123456789abcdef&next=elsewhere#",
            1,
        );
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url.push_str("&extra=field");
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization = desktop_app_authorization();
        authorization.approval_url = authorization.approval_url.replacen(
            "#authorization_id=appauth_0123456789abcdef0123456789abcdef&browser_ticket=",
            "#browser_ticket=",
            1,
        )
            + "&authorization_id=appauth_0123456789abcdef0123456789abcdef";
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
    }

    #[test]
    fn desktop_approval_request_query_forces_a_new_browser_document() {
        let authorization = desktop_app_authorization();
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_ok()
        );

        for replacement in [
            "#",
            "?request=appauth_ffffffffffffffffffffffffffffffff#",
            "?request=appauth_0123456789abcdef0123456789abcdef&next=elsewhere#",
        ] {
            let mut invalid = desktop_app_authorization();
            invalid.approval_url = invalid.approval_url.replacen(
                "?request=appauth_0123456789abcdef0123456789abcdef#",
                replacement,
                1,
            );
            assert!(
                validated_app_authorization(&invalid, "https://console.example.test").is_err(),
                "unexpectedly accepted approval URL {}",
                invalid.approval_url
            );
        }
    }

    #[test]
    fn app_authorization_rejects_expired_or_excessive_expiry() {
        let mut authorization = desktop_app_authorization();
        authorization.expires_at = authorization_expiry_after(Duration::from_secs(0));
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
        authorization.expires_at =
            authorization_expiry_after(MAX_LOGIN_TIMEOUT + Duration::from_secs(1));
        assert!(
            validated_app_authorization(&authorization, "https://console.example.test").is_err()
        );
    }

    #[test]
    fn app_authorization_response_rejects_removed_manual_code_fields() {
        let response = serde_json::json!({
            "authorization_id": "appauth_0123456789abcdef0123456789abcdef",
            "approval_url": format!(
                "https://console.example.test/desktop-login?request=appauth_0123456789abcdef0123456789abcdef#authorization_id=appauth_0123456789abcdef0123456789abcdef&browser_ticket=hbat_{}&client=desktop",
                "a".repeat(43),
            ),
            "expires_at": authorization_expiry_after(Duration::from_secs(5 * 60)),
            "user_code": "ABCD-EFGH",
        });
        assert!(serde_json::from_value::<AppAuthorization>(response).is_err());
    }

    #[test]
    fn app_authorization_request_posts_the_token_without_an_authorization_header() {
        let client = CloudClient::new(
            "https://console.example.test".to_string(),
            reqwest::Client::new(),
            "0.5.0",
        );
        let request = client
            .app_authorization_request("happ_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
            .build()
            .expect("authorization request");

        assert_eq!(request.method(), reqwest::Method::POST);
        assert_eq!(
            request.url().as_str(),
            "https://console.example.test/api/v1/app/authorizations"
        );
        assert!(request.headers().get("authorization").is_none());
        let payload: serde_json::Value = serde_json::from_slice(
            request
                .body()
                .and_then(reqwest::Body::as_bytes)
                .expect("JSON body"),
        )
        .expect("authorization JSON");
        assert_eq!(
            payload,
            serde_json::json!({
                "token": "happ_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
                "client": "desktop",
            })
        );
    }

    #[test]
    fn authorization_install_is_generation_owned_and_keeps_approval_url_native() {
        let credentials = Arc::new(MemoryCredentialStore::default());
        let supervisor = CloudConnectionSupervisor::with_store(
            None,
            credentials.clone(),
            "https://console.example.test",
            new_remote_runtime_secret(),
        );
        supervisor.inner.state.lock().expect("state").generation = 2;

        assert!(!supervisor
            .install_authorization_if_current(
                1,
                "happ_stale",
                desktop_app_authorization().approval_url,
            )
            .expect("stale authorization"));
        assert_eq!(
            credentials.get(SESSION_CREDENTIAL).expect("session read"),
            None
        );

        assert!(supervisor
            .install_authorization_if_current(
                2,
                "happ_current",
                desktop_app_authorization().approval_url,
            )
            .expect("current authorization"));
        assert_eq!(
            credentials.get(SESSION_CREDENTIAL).expect("session read"),
            Some("happ_current".to_string())
        );
        let status = supervisor.status(None);
        assert!(status.auto_start_enabled);
        assert_eq!(
            status.message,
            "Approve sign-in in your browser. This window will update automatically."
        );
        assert!(serde_json::to_value(&status)
            .expect("serialize status")
            .get("verification_code")
            .is_none());
        assert_eq!(
            supervisor.pending_approval_url(),
            Some(desktop_app_authorization().approval_url)
        );

        supervisor.apply_actor(
            2,
            &CloudActor {
                id: "actor_1".to_string(),
                email: "operator@example.com".to_string(),
                org_id: "org_1".to_string(),
            },
        );
        let approved = supervisor.status(None);
        assert!(approved.signed_in);
        assert_eq!(supervisor.pending_approval_url(), None);
    }

    #[test]
    fn only_the_current_generation_can_clear_an_approval_url_on_error() {
        let supervisor = CloudConnectionSupervisor::default();
        let approval_url = desktop_app_authorization().approval_url;
        {
            let mut state = supervisor.inner.state.lock().expect("state");
            state.generation = 2;
            state.phase = ConnectionPhase::Authorizing;
            state.approval_url = Some(approval_url.clone());
        }

        supervisor.set_error_if_current(1, "stale error", None);
        assert_eq!(supervisor.pending_approval_url(), Some(approval_url));

        supervisor.set_error_if_current(2, "current error", None);
        assert_eq!(supervisor.pending_approval_url(), None);
    }

    #[test]
    fn remote_runtime_secrets_are_ephemeral_bearer_tokens() {
        let first = new_remote_runtime_secret();
        let second = new_remote_runtime_secret();
        assert!(first.starts_with("hrelay_"));
        assert!(first.len() >= 40);
        assert_ne!(first, second);
    }

    #[test]
    fn preferences_never_serialize_credentials() {
        let preferences = CloudConnectionPreferences {
            auto_start_enabled: true,
            host_actor_id: Some("actor_1".to_string()),
            host_org_id: Some("org_1".to_string()),
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
            host_actor_id: Some("actor_1".to_string()),
            host_org_id: Some("org_1".to_string()),
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
    fn relay_authorization_overrides_untrusted_identity_headers() {
        let authorization =
            RelayAuthorization::new("desktop-relay-secret-123456", "actor_1", "org_1", "host_1")
                .expect("relay authorization");
        let untrusted = HashMap::from([
            (REMOTE_ACTOR_ID_HEADER.to_string(), "attacker".to_string()),
            (
                REMOTE_RUNTIME_SECRET_HEADER.to_string(),
                "attacker-secret".to_string(),
            ),
            ("content-type".to_string(), "application/json".to_string()),
        ]);

        let request = apply_proxy_headers(
            authorization.apply(reqwest::Client::new().get("http://127.0.0.1:8765/healthz")),
            &untrusted,
        )
        .build()
        .expect("relay request");

        assert_eq!(
            request.headers().get(REMOTE_ACTOR_ID_HEADER),
            Some(&HeaderValue::from_static("actor_1"))
        );
        assert_eq!(
            request.headers().get(REMOTE_ORG_ID_HEADER),
            Some(&HeaderValue::from_static("org_1"))
        );
        assert_eq!(
            request.headers().get(REMOTE_RUNTIME_ID_HEADER),
            Some(&HeaderValue::from_static("host_1"))
        );
        assert_eq!(
            request
                .headers()
                .get(REMOTE_RUNTIME_SECRET_HEADER)
                .and_then(|value| value.to_str().ok()),
            Some("desktop-relay-secret-123456")
        );
    }

    #[test]
    fn saved_host_credentials_are_bound_to_the_cloud_owner() {
        let actor = CloudActor {
            id: "actor_1".to_string(),
            email: "operator@example.com".to_string(),
            org_id: "org_1".to_string(),
        };

        assert!(saved_host_owner_matches(
            Some("actor_1"),
            Some("org_1"),
            &actor
        ));
        assert!(!saved_host_owner_matches(
            Some("actor_2"),
            Some("org_1"),
            &actor
        ));
        assert!(!saved_host_owner_matches(
            Some("actor_1"),
            Some("org_2"),
            &actor
        ));
        assert!(!saved_host_owner_matches(None, Some("org_1"), &actor));
    }

    #[test]
    fn host_registration_storage_is_generation_owned() {
        let credentials = Arc::new(MemoryCredentialStore::default());
        let supervisor = CloudConnectionSupervisor::with_store(
            None,
            credentials.clone(),
            "https://console.example.test",
            new_remote_runtime_secret(),
        );
        supervisor.inner.state.lock().expect("state").generation = 2;
        let actor = CloudActor {
            id: "actor_1".to_string(),
            email: "operator@example.com".to_string(),
            org_id: "org_1".to_string(),
        };
        let created = CreatedHost {
            host: CloudHost {
                id: "host_1".to_string(),
            },
            host_token: "host-token-12345678901234567890".to_string(),
        };

        let err = supervisor
            .save_created_host_if_current(1, &actor, &created)
            .expect_err("stale registration must fail");
        assert_eq!(err, REMOTE_ACCESS_CANCELLED);
        assert_eq!(
            credentials.get(HOST_CREDENTIAL).expect("credential read"),
            None
        );

        supervisor
            .save_created_host_if_current(2, &actor, &created)
            .expect("current registration");
        assert_eq!(
            credentials.get(HOST_CREDENTIAL).expect("credential read"),
            Some(created.host_token.clone())
        );
        let state = supervisor.inner.state.lock().expect("state");
        assert_eq!(state.preferences.host_id.as_deref(), Some("host_1"));
        assert_eq!(state.preferences.host_actor_id.as_deref(), Some("actor_1"));
        assert_eq!(state.preferences.host_org_id.as_deref(), Some("org_1"));
    }

    #[test]
    fn stale_session_failure_cannot_delete_current_credentials() {
        let credentials = Arc::new(MemoryCredentialStore::default());
        credentials
            .set(SESSION_CREDENTIAL, "session-token-12345678901234567890")
            .expect("session credential");
        credentials
            .set(HOST_CREDENTIAL, "host-token-12345678901234567890")
            .expect("host credential");
        let supervisor = CloudConnectionSupervisor::with_store(
            None,
            credentials.clone(),
            "https://console.example.test",
            new_remote_runtime_secret(),
        );
        supervisor.inner.state.lock().expect("state").generation = 2;

        supervisor.expire_account_if_current(1, "expired");
        supervisor.fail_authorization_if_current(1, "failed", None);

        assert!(credentials
            .get(SESSION_CREDENTIAL)
            .expect("session read")
            .is_some());
        assert!(credentials
            .get(HOST_CREDENTIAL)
            .expect("host read")
            .is_some());
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
            new_remote_runtime_secret(),
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
            new_remote_runtime_secret(),
        );
        {
            let mut state = supervisor.inner.state.lock().expect("state");
            state.signed_in = false;
            state.phase = ConnectionPhase::Authorizing;
            state.preferences.auto_start_enabled = true;
            state.approval_url = Some(desktop_app_authorization().approval_url);
        }

        let status = supervisor.stop(Some("http://127.0.0.1:8765".to_string()));

        assert!(!status.auto_start_enabled);
        assert_eq!(status.phase, "disconnected");
        assert_eq!(supervisor.pending_approval_url(), None);
        assert_eq!(
            credentials.get(SESSION_CREDENTIAL).expect("read session"),
            None
        );
    }
}
