//! Cloud-only mobile companion runtime.
//!
//! Mobile deliberately does not start the bundled gateway or expose desktop
//! operator commands. The app session token is kept only in this process for
//! the first mobile slice: it is never returned to the webview, written to
//! disk, or placed in logs, and is lost when the app process exits.

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use futures_util::{stream, StreamExt};
use rand::RngCore;
use reqwest::redirect::Policy;
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
#[cfg(target_os = "ios")]
use std::ffi::{c_char, CStr};
use std::fmt;
#[cfg(target_os = "ios")]
use std::sync::OnceLock;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tauri::AppHandle;
use tauri_plugin_opener::OpenerExt;

#[cfg(target_os = "android")]
use tauri::Manager as _;

const DEFAULT_CLOUD_URL: &str = "https://console.hecatehq.com";
const APP_AUTHORIZATIONS_PATH: &str = "/api/v1/app/authorizations";
const CONNECTIONS_PATH: &str = "/api/v1/app/connections";
const BROWSER_SESSIONS_PATH: &str = "/api/v1/app/browser-sessions";
const PUSH_DEVICES_PATH: &str = "/api/v1/app/push-devices";
const CURRENT_SESSION_PATH: &str = "/api/v1/sessions/current";
const LOGIN_POLL_INTERVAL: Duration = Duration::from_secs(2);
const MAX_LOGIN_TIMEOUT: Duration = Duration::from_secs(20 * 60);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(20);
const READINESS_TIMEOUT: Duration = Duration::from_secs(3);
const MAX_READINESS_CONCURRENCY: usize = 4;
const START_REQUEST_TIMEOUT: Duration = Duration::from_secs(90);
const SESSION_STORAGE: &str = "memory_only";
const PUSH_STATUS_WAIT: Duration = Duration::from_millis(800);
const PUSH_REGISTRATION_WAIT: Duration = Duration::from_secs(15);
const PUSH_AUTHORIZATION_WAIT: Duration = Duration::from_secs(45);
const PUSH_POLL_INTERVAL: Duration = Duration::from_millis(100);
const MOBILE_INSTANCES_URL: &str = "hecate-mobile://connections/";
const MOBILE_AUTH_CLIENT: &str = "mobile";
const MOBILE_AUTH_CALLBACK_URL: &str = "hecate-mobile://auth/complete";

#[cfg(target_os = "android")]
const ANDROID_COOKIE_PLUGIN_IDENTIFIER: &str = "sh.hecate.mobile";

#[cfg(target_os = "android")]
struct AndroidCookieManager<R: tauri::Runtime> {
    handle: tauri::plugin::PluginHandle<R>,
}

#[derive(Clone, Debug)]
struct MobileShellNavigation {
    home_url: tauri::Url,
}

impl MobileShellNavigation {
    fn capture(home_url: tauri::Url) -> Result<Self, String> {
        if !is_mobile_shell_url(&home_url) {
            return Err("The mobile shell did not start from a packaged app URL.".to_string());
        }
        Ok(Self { home_url })
    }
}

fn is_mobile_shell_url(url: &tauri::Url) -> bool {
    (url.scheme() == "tauri" && url.host_str() == Some("localhost"))
        || (matches!(url.scheme(), "http" | "https") && url.host_str() == Some("tauri.localhost"))
}

fn is_mobile_instances_url(url: &tauri::Url) -> bool {
    url.as_str() == MOBILE_INSTANCES_URL
}

#[cfg(target_os = "android")]
impl<R: tauri::Runtime> AndroidCookieManager<R> {
    fn clear_all(&self) -> Result<(), String> {
        self.handle
            .run_mobile_plugin::<()>("clearAll", ())
            .map_err(|error| format!("Android WebView cookies could not be cleared: {error}"))
    }
}

#[cfg(mobile)]
fn mobile_cookie_manager_plugin<R: tauri::Runtime>() -> tauri::plugin::TauriPlugin<R> {
    tauri::plugin::Builder::new("hecate-cookie-manager")
        .setup(|app, api| {
            #[cfg(target_os = "android")]
            {
                let handle = api.register_android_plugin(
                    ANDROID_COOKIE_PLUGIN_IDENTIFIER,
                    "CookieManagerPlugin",
                )?;
                app.manage(AndroidCookieManager { handle });
            }
            #[cfg(not(target_os = "android"))]
            let _ = (app, api);
            Ok(())
        })
        .build()
}

#[cfg(mobile)]
fn mobile_navigation_plugin<R: tauri::Runtime>() -> tauri::plugin::TauriPlugin<R> {
    use tauri::Manager as _;

    tauri::plugin::Builder::new("hecate-mobile-navigation")
        .on_navigation(|webview, url| {
            if !is_mobile_instances_url(url) {
                return true;
            }

            let Some(navigation) = webview.app_handle().try_state::<MobileShellNavigation>() else {
                log::warn!("Hecate mobile could not return home because the shell URL is missing");
                return false;
            };
            let target = navigation.home_url.clone();
            let webview = webview.clone();
            // Leave the navigation callback before asking the same WebView to
            // navigate again. This avoids a re-entrant policy callback on
            // WKWebView while still cancelling the internal sentinel URL.
            tauri::async_runtime::spawn(async move {
                tokio::task::yield_now().await;
                if let Err(error) = webview.navigate(target) {
                    log::warn!("Hecate mobile could not return to the instance chooser: {error}");
                }
            });
            false
        })
        .build()
}

fn clear_mobile_browsing_data<R: tauri::Runtime>(
    app: &AppHandle<R>,
    window: &tauri::WebviewWindow<R>,
) -> Result<(), String> {
    let mut failures = Vec::new();
    if let Err(error) = window.clear_all_browsing_data() {
        failures.push(format!(
            "WebView browsing data could not be cleared: {error}"
        ));
    }
    #[cfg(target_os = "android")]
    if let Err(error) = app.state::<AndroidCookieManager<R>>().clear_all() {
        failures.push(error);
    }
    #[cfg(not(target_os = "android"))]
    let _ = app;

    if failures.is_empty() {
        Ok(())
    } else {
        Err(failures.join("; "))
    }
}

#[derive(Clone)]
struct MobileCloud {
    inner: Arc<MobileCloudInner>,
}

struct MobileCloudInner {
    cloud_url: String,
    http: reqwest::Client,
    session: Mutex<MobileSession>,
    notifications: Mutex<MobileNotificationSession>,
    connection_starts: Mutex<HashSet<String>>,
}

struct MobileConnectionStartGuard {
    inner: Arc<MobileCloudInner>,
    connection_id: String,
}

impl Drop for MobileConnectionStartGuard {
    fn drop(&mut self) {
        if let Ok(mut starts) = self.inner.connection_starts.lock() {
            starts.remove(&self.connection_id);
        }
    }
}

#[derive(Debug)]
struct MobileSession {
    generation: u64,
    phase: MobilePhase,
    token: Option<String>,
    /// Kept native-only so the authorization transaction never enters the webview.
    approval_url: Option<String>,
    authorization_deadline: Option<tokio::time::Instant>,
    /// Generation-owned wake signal for the single authorization poll worker.
    authorization_wake: Option<Arc<tokio::sync::Notify>>,
    account_email: Option<String>,
    message: String,
    last_error: Option<String>,
}

impl Default for MobileSession {
    fn default() -> Self {
        Self {
            generation: 0,
            phase: MobilePhase::SignedOut,
            token: None,
            approval_url: None,
            authorization_deadline: None,
            authorization_wake: None,
            account_email: None,
            message: "Sign in to see the Hecate runtimes available to you.".to_string(),
            last_error: None,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum MobilePhase {
    SignedOut,
    Authorizing,
    SignedIn,
    Error,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum MobileNotificationAuthorization {
    Unknown,
    NotDetermined,
    Denied,
    Authorized,
    Provisional,
    Ephemeral,
    Unavailable,
}

impl MobileNotificationAuthorization {
    fn as_str(self) -> &'static str {
        match self {
            Self::Unknown => "checking",
            Self::NotDetermined => "not_determined",
            Self::Denied => "denied",
            Self::Authorized => "authorized",
            Self::Provisional => "provisional",
            Self::Ephemeral => "ephemeral",
            Self::Unavailable => "unavailable",
        }
    }

    fn allows_notifications(self) -> bool {
        matches!(self, Self::Authorized | Self::Provisional | Self::Ephemeral)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum MobileNotificationRegistration {
    Idle,
    RegisteringWithApple,
    TokenReady,
    RegisteringWithCloud,
    Registered,
    PendingDelete,
    Failed,
}

impl MobileNotificationRegistration {
    fn as_str(self) -> &'static str {
        match self {
            Self::Idle => "idle",
            Self::RegisteringWithApple => "registering_with_apple",
            Self::TokenReady => "token_ready",
            Self::RegisteringWithCloud => "registering_with_cloud",
            Self::Registered => "registered",
            Self::PendingDelete => "pending_delete",
            Self::Failed => "failed",
        }
    }
}

#[derive(Debug)]
struct MobileNotificationSession {
    authorization: MobileNotificationAuthorization,
    registration: MobileNotificationRegistration,
    requested_enabled: bool,
    apns_token: Option<String>,
    environment: Option<String>,
    device_id: Option<String>,
    session_generation: Option<u64>,
    registration_attempt: u64,
    last_error: Option<String>,
}

impl Default for MobileNotificationSession {
    fn default() -> Self {
        let requested_enabled = native_push_requested_enabled();
        let device_id = native_push_registered_device_id();
        Self {
            authorization: if cfg!(target_os = "ios") {
                MobileNotificationAuthorization::Unknown
            } else {
                MobileNotificationAuthorization::Unavailable
            },
            registration: if device_id.is_some() && requested_enabled {
                MobileNotificationRegistration::Registered
            } else if device_id.is_some() {
                MobileNotificationRegistration::PendingDelete
            } else {
                MobileNotificationRegistration::Idle
            },
            requested_enabled,
            apns_token: None,
            environment: None,
            device_id,
            session_generation: None,
            registration_attempt: 0,
            last_error: None,
        }
    }
}

impl MobilePhase {
    fn as_str(self) -> &'static str {
        match self {
            Self::SignedOut => "signed_out",
            Self::Authorizing => "authorizing",
            Self::SignedIn => "signed_in",
            Self::Error => "error",
        }
    }
}

#[derive(Clone, Debug, Serialize)]
struct MobileStatus {
    available: bool,
    phase: String,
    signed_in: bool,
    authorizing: bool,
    approval_page_available: bool,
    account_email: Option<String>,
    cloud_url: String,
    message: String,
    last_error: Option<String>,
    /// Explicitly surfaced to the shell so the first-slice limitation cannot
    /// be mistaken for secure persistent credential storage.
    session_storage: &'static str,
    session_persists_after_exit: bool,
}

/// Public notification state deliberately excludes the APNs token, Cloud
/// device id, and stable installation id. Those values never cross IPC into
/// the webview.
#[derive(Clone, Debug, Serialize)]
struct MobileNotificationStatus {
    available: bool,
    authorization: String,
    registration: String,
    requested_enabled: bool,
    enabled: bool,
    background_active: bool,
    message: String,
    last_error: Option<String>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct MobileConnection {
    id: String,
    kind: String,
    org_id: String,
    #[serde(default)]
    project_id: Option<String>,
    name: String,
    status: String,
    reachable: bool,
    #[serde(default)]
    can_start: bool,
    #[serde(default)]
    remote_enabled: bool,
    #[serde(default)]
    version: Option<String>,
    #[serde(default)]
    capabilities: Vec<String>,
    #[serde(default)]
    last_seen_at: Option<String>,
    /// Server-authored continuation paths are deliberately not returned to
    /// JavaScript. `mobile_open_connection` re-fetches and validates them.
    #[serde(default, skip_serializing)]
    browser_open_path: Option<String>,
    #[serde(default, skip_serializing)]
    remote_session_path: Option<String>,
    #[serde(default, skip_serializing)]
    start_path: Option<String>,
    #[serde(default, skip_serializing)]
    readiness_path: Option<String>,
}

#[derive(Debug, Serialize)]
struct MobileOpenResult {
    connection_id: String,
    name: String,
    message: String,
}

#[derive(Debug, Serialize)]
struct MobileStartResult {
    connection_id: String,
    name: String,
    message: String,
}

#[derive(Debug, Deserialize)]
struct CloudActor {
    email: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct AppAuthorization {
    authorization_id: String,
    approval_url: String,
    expires_at: String,
}

#[derive(Debug)]
struct ValidatedAppAuthorization {
    approval_url: String,
    expires_in: Duration,
}

#[derive(Debug, Deserialize)]
struct BrowserSession {
    open_url: String,
    expires_at: String,
}

#[derive(Debug, Deserialize)]
struct RemoteBrowserSession {
    open_url: String,
    #[serde(default)]
    expires_at: Option<String>,
}

#[derive(Debug, Deserialize)]
struct CloudEnvelope<T> {
    data: T,
}

#[derive(Debug, Deserialize)]
struct CloudErrorEnvelope {
    error: CloudErrorBody,
}

#[derive(Debug, Deserialize)]
struct CloudErrorBody {
    message: String,
}

#[derive(Debug, Deserialize)]
struct PushDevice {
    id: String,
    platform: String,
    environment: String,
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

impl MobileCloud {
    fn from_environment() -> Result<Self, String> {
        let requested_url = std::env::var("HECATE_CLOUD_URL")
            .ok()
            .filter(|value| !value.trim().is_empty())
            .unwrap_or_else(|| DEFAULT_CLOUD_URL.to_string());
        Self::with_cloud_url(&requested_url)
    }

    fn with_cloud_url(cloud_url: &str) -> Result<Self, String> {
        let cloud_url = normalize_cloud_url(cloud_url)?;
        let http = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(10))
            // Never forward the app Bearer token through an HTTP redirect.
            .redirect(Policy::none())
            .build()
            .map_err(|_| "The Hecate Cloud HTTP client could not start.".to_string())?;
        Ok(Self {
            inner: Arc::new(MobileCloudInner {
                cloud_url,
                http,
                session: Mutex::new(MobileSession::default()),
                notifications: Mutex::new(MobileNotificationSession::default()),
                connection_starts: Mutex::new(HashSet::new()),
            }),
        })
    }

    fn status(&self) -> MobileStatus {
        let Ok(session) = self.inner.session.lock() else {
            return MobileStatus {
                available: false,
                phase: MobilePhase::Error.as_str().to_string(),
                signed_in: false,
                authorizing: false,
                approval_page_available: false,
                account_email: None,
                cloud_url: self.inner.cloud_url.clone(),
                message: "The mobile session is unavailable.".to_string(),
                last_error: Some("Restart Hecate and try again.".to_string()),
                session_storage: SESSION_STORAGE,
                session_persists_after_exit: false,
            };
        };
        status_from_session(&session, &self.inner.cloud_url)
    }

    fn notification_status(&self) -> MobileNotificationStatus {
        if !cfg!(target_os = "ios") {
            return MobileNotificationStatus {
                available: false,
                authorization: MobileNotificationAuthorization::Unavailable
                    .as_str()
                    .to_string(),
                registration: MobileNotificationRegistration::Idle.as_str().to_string(),
                requested_enabled: false,
                enabled: false,
                background_active: false,
                message: "Run notifications are currently available on iPhone only.".to_string(),
                last_error: None,
            };
        }

        let signed_in_generation = self.inner.session.lock().ok().and_then(|session| {
            (session.phase == MobilePhase::SignedIn && session.token.is_some())
                .then_some(session.generation)
        });
        let Ok(push) = self.inner.notifications.lock() else {
            return MobileNotificationStatus {
                available: true,
                authorization: MobileNotificationAuthorization::Unknown
                    .as_str()
                    .to_string(),
                registration: MobileNotificationRegistration::Failed.as_str().to_string(),
                requested_enabled: false,
                enabled: false,
                background_active: false,
                message: "Notification status is unavailable.".to_string(),
                last_error: Some("Restart Hecate and try again.".to_string()),
            };
        };
        let background_active = push.requested_enabled && push.device_id.is_some();
        let enabled = background_active
            && push.authorization.allows_notifications()
            && push.registration == MobileNotificationRegistration::Registered
            && (push.session_generation == signed_in_generation || signed_in_generation.is_none());
        let message = if signed_in_generation.is_none() && background_active {
            "Run notifications may still be active for the last account. Sign in to manage them."
        } else if signed_in_generation.is_none() && push.device_id.is_some() {
            "Notifications are off on this iPhone. Sign in to finish Cloud cleanup."
        } else if signed_in_generation.is_none() {
            "Sign in before turning on run notifications."
        } else if !push.requested_enabled {
            "Get an alert when a run needs approval or finishes."
        } else {
            match push.authorization {
                MobileNotificationAuthorization::Unknown => {
                    "Checking notification access on this iPhone…"
                }
                MobileNotificationAuthorization::NotDetermined => {
                    "Allow notifications to receive run updates."
                }
                MobileNotificationAuthorization::Denied => {
                    "Notifications are blocked in iPhone Settings."
                }
                authorization if authorization.allows_notifications() => match push.registration {
                    MobileNotificationRegistration::Registered if enabled => {
                        "Run notifications are on for this Hecate account."
                    }
                    MobileNotificationRegistration::RegisteringWithApple => {
                        "Connecting this iPhone to Apple Push Notification service…"
                    }
                    MobileNotificationRegistration::RegisteringWithCloud
                    | MobileNotificationRegistration::TokenReady => {
                        "Connecting this iPhone to Hecate Cloud…"
                    }
                    MobileNotificationRegistration::Failed => "Run notifications need attention.",
                    _ => "Run notifications are ready to connect.",
                },
                MobileNotificationAuthorization::Unavailable => {
                    "Run notifications are unavailable on this device."
                }
                _ => "Run notifications need attention.",
            }
        };
        MobileNotificationStatus {
            available: true,
            authorization: push.authorization.as_str().to_string(),
            registration: push.registration.as_str().to_string(),
            requested_enabled: push.requested_enabled,
            enabled,
            background_active,
            message: message.to_string(),
            last_error: push.last_error.clone(),
        }
    }

    fn set_notifications_requested(&self, requested: bool) -> Result<(), String> {
        native_push_set_requested_enabled(requested)?;
        let mut push = self
            .inner
            .notifications
            .lock()
            .map_err(|_| "Notification status is unavailable.".to_string())?;
        push.requested_enabled = requested;
        push.last_error = None;
        if requested {
            if push.registration == MobileNotificationRegistration::Failed {
                push.registration = if push.apns_token.is_some() && push.environment.is_some() {
                    MobileNotificationRegistration::TokenReady
                } else {
                    MobileNotificationRegistration::Idle
                };
            }
        } else {
            push.registration = if push.device_id.is_some() {
                MobileNotificationRegistration::PendingDelete
            } else {
                MobileNotificationRegistration::Idle
            };
            push.apns_token = None;
            push.environment = None;
            push.session_generation = None;
            push.registration_attempt = push.registration_attempt.wrapping_add(1);
        }
        Ok(())
    }

    fn record_notification_authorization(
        &self,
        authorization: MobileNotificationAuthorization,
        error: Option<String>,
    ) {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return;
        };
        push.authorization = authorization;
        if let Some(error) = error.filter(|value| !value.trim().is_empty()) {
            push.registration = MobileNotificationRegistration::Failed;
            push.last_error = Some(error);
        } else if authorization == MobileNotificationAuthorization::Denied {
            push.apns_token = None;
            push.environment = None;
            push.last_error = None;
        }
    }

    fn record_apns_token(&self, token: String, environment: String) {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return;
        };
        if !push.requested_enabled {
            return;
        }
        push.apns_token = Some(token);
        push.environment = Some(environment);
        if push.registration != MobileNotificationRegistration::RegisteringWithCloud {
            push.registration = MobileNotificationRegistration::TokenReady;
        }
        push.last_error = None;
    }

    fn record_apns_failure(&self, error: String) {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return;
        };
        push.apns_token = None;
        push.environment = None;
        push.registration = MobileNotificationRegistration::Failed;
        push.last_error = Some(error);
    }

    fn begin_sign_in(&self) -> Result<(u64, String), String> {
        let token = new_app_token();
        let generation = {
            let mut session = self
                .inner
                .session
                .lock()
                .map_err(|_| "The mobile session is unavailable.".to_string())?;
            session.generation = session.generation.wrapping_add(1);
            session.phase = MobilePhase::Authorizing;
            session.token = Some(token.clone());
            session.approval_url = None;
            session.authorization_deadline = None;
            session.authorization_wake = Some(Arc::new(tokio::sync::Notify::new()));
            session.account_email = None;
            session.message = "Creating a secure browser confirmation.".to_string();
            session.last_error = None;
            session.generation
        };
        Ok((generation, token))
    }

    async fn create_authorization(
        &self,
        token: &str,
    ) -> Result<ValidatedAppAuthorization, CloudAPIError> {
        let authorization = self
            .request_data_unauthenticated::<AppAuthorization>(
                reqwest::Method::POST,
                APP_AUTHORIZATIONS_PATH,
                Some(app_authorization_request_body(token)),
            )
            .await?;
        validate_app_authorization(&self.inner.cloud_url, authorization).map_err(|message| {
            CloudAPIError {
                status: Some(200),
                message,
            }
        })
    }

    fn note_authorization_callback(&self) {
        let wake = {
            let Ok(mut session) = self.inner.session.lock() else {
                return;
            };
            if session.phase != MobilePhase::Authorizing
                || session.token.is_none()
                || session.approval_url.is_none()
            {
                return;
            }
            // The callback is intentionally only a foreground hint. The
            // authenticated `/api/v1/me` poll remains the source of truth.
            session.message = "Finishing sign-in…".to_string();
            session.last_error = None;
            let Some(wake) = session.authorization_wake.clone() else {
                return;
            };
            wake
        };
        // `notify_one` retains a permit when the poll task is between its
        // request and wait, so the foreground callback cannot be lost. The
        // signal belongs to this authorization generation, and wakes its
        // existing worker rather than starting a duplicate long-lived poll.
        wake.notify_one();
    }

    fn open_authorization_in_browser(
        &self,
        generation: u64,
        authorization: ValidatedAppAuthorization,
        app: &AppHandle,
    ) -> Result<(), String> {
        // Opening the system browser is synchronous. Hold the session lock so
        // sign-out either wins first and rejects this transaction, or begins
        // only after the browser has opened for the current generation.
        let mut session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        if session.generation != generation || session.phase != MobilePhase::Authorizing {
            return Err("Sign-in was cancelled before the browser opened.".to_string());
        }
        let approval_url = stage_pending_authorization(&mut session, authorization);
        if app.opener().open_url(approval_url, None::<&str>).is_err() {
            log::warn!("Hecate mobile could not open the Cloud approval page");
            // Keep the validated transaction pending so the recovery button
            // can retry without creating a second authorization.
            return Err(record_authorization_open_failure(&mut session, false));
        }
        Ok(())
    }

    fn reopen_authorization_in_browser(&self, app: &AppHandle) -> Result<(), String> {
        // Opening the browser is synchronous. Keep the session locked so a
        // concurrent sign-out cannot reopen a transaction it just cancelled.
        let mut session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        let pending = session.phase == MobilePhase::Authorizing
            && session.token.is_some()
            && session.approval_url.is_some();
        let deadline = session.authorization_deadline;
        if !pending || deadline.is_none() || session.approval_url.is_none() {
            return Err("Start a new Hecate sign-in to open the approval page.".to_string());
        }
        if deadline.is_some_and(|value| tokio::time::Instant::now() >= value) {
            session.phase = MobilePhase::Error;
            session.token = None;
            session.approval_url = None;
            session.authorization_deadline = None;
            session.authorization_wake = None;
            session.account_email = None;
            session.message = "This sign-in request expired. Try again.".to_string();
            session.last_error = None;
            return Err("This sign-in request expired. Start again.".to_string());
        }
        let approval_url = session
            .approval_url
            .clone()
            .expect("checked pending authorization URL");
        if app.opener().open_url(approval_url, None::<&str>).is_err() {
            log::warn!("Hecate mobile could not reopen the Cloud approval page");
            return Err(record_authorization_open_failure(&mut session, true));
        }
        session.message =
            "Approval page opened. Confirm the connection in your browser.".to_string();
        session.last_error = None;
        Ok(())
    }

    async fn poll_authorization(&self, generation: u64) {
        loop {
            let Some((token, deadline, wake)) = self.pending_authorization(generation) else {
                return;
            };
            match self
                .request_data::<CloudActor>(reqwest::Method::GET, "/api/v1/me", &token, None)
                .await
            {
                Ok(actor) => {
                    self.complete_authorization(generation, actor.email);
                    return;
                }
                Err(error)
                    if error.status == Some(401) && tokio::time::Instant::now() < deadline =>
                {
                    self.update_authorization_wait(generation, None);
                }
                Err(error) if tokio::time::Instant::now() < deadline => {
                    self.update_authorization_wait(generation, Some(error.to_string()));
                }
                Err(error) => {
                    let detail = if error.status == Some(401) {
                        None
                    } else {
                        Some(error.to_string())
                    };
                    self.fail_authorization(
                        generation,
                        "Sign-in was not completed. Try again.",
                        detail,
                    );
                    return;
                }
            }
            tokio::select! {
                _ = tokio::time::sleep(LOGIN_POLL_INTERVAL) => {}
                _ = wake.notified() => {}
            }
        }
    }

    fn pending_authorization(
        &self,
        generation: u64,
    ) -> Option<(String, tokio::time::Instant, Arc<tokio::sync::Notify>)> {
        self.inner.session.lock().ok().and_then(|session| {
            if session.generation != generation || session.phase != MobilePhase::Authorizing {
                return None;
            }
            Some((
                session.token.clone()?,
                session.authorization_deadline?,
                session.authorization_wake.clone()?,
            ))
        })
    }

    fn complete_authorization(&self, generation: u64, email: String) {
        let Ok(mut session) = self.inner.session.lock() else {
            return;
        };
        if session.generation != generation || session.phase != MobilePhase::Authorizing {
            return;
        }
        session.phase = MobilePhase::SignedIn;
        session.approval_url = None;
        session.authorization_deadline = None;
        session.authorization_wake = None;
        session.account_email = Some(email);
        session.message = "Signed in. Choose a Hecate connection to continue.".to_string();
        session.last_error = None;
        log::info!("Hecate mobile Cloud session authorized (in-memory storage)");
    }

    fn update_authorization_wait(&self, generation: u64, detail: Option<String>) {
        let Ok(mut session) = self.inner.session.lock() else {
            return;
        };
        if session.generation != generation || session.phase != MobilePhase::Authorizing {
            return;
        }
        session.message =
            "Waiting for browser approval. Hecate will return here automatically.".to_string();
        session.last_error = detail;
    }

    fn fail_authorization(&self, generation: u64, message: &str, detail: Option<String>) {
        let Ok(mut session) = self.inner.session.lock() else {
            return;
        };
        if session.generation != generation {
            return;
        }
        session.phase = MobilePhase::Error;
        session.token = None;
        session.approval_url = None;
        session.authorization_deadline = None;
        session.authorization_wake = None;
        session.account_email = None;
        session.message = message.to_string();
        session.last_error = detail;
    }

    fn authorized_snapshot(&self) -> Result<(u64, String), String> {
        let session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        if session.phase != MobilePhase::SignedIn {
            return Err("Sign in to Hecate Cloud first.".to_string());
        }
        let token = session
            .token
            .clone()
            .ok_or_else(|| "Sign in to Hecate Cloud first.".to_string())?;
        Ok((session.generation, token))
    }

    fn ensure_authorized_generation(&self, generation: u64) -> Result<(), String> {
        let session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        if !session_allows_navigation(&session, generation) {
            return Err(
                "Your Hecate Cloud session changed while connections were loading.".to_string(),
            );
        }
        Ok(())
    }

    fn accept_connections_response(
        &self,
        generation: u64,
        connections: Vec<MobileConnection>,
    ) -> Result<Vec<MobileConnection>, String> {
        self.ensure_authorized_generation(generation)?;
        Ok(connections)
    }

    async fn fetch_connections(
        &self,
        generation: u64,
        token: &str,
    ) -> Result<Vec<MobileConnection>, String> {
        match self
            .request_data::<Vec<MobileConnection>>(
                reqwest::Method::GET,
                CONNECTIONS_PATH,
                token,
                None,
            )
            .await
        {
            Ok(connections) => {
                // The bearer snapshot predates the network wait. Do not let a
                // successful response cross a sign-out or account-switch
                // boundary and become data for the replacement session.
                self.accept_connections_response(generation, connections)
            }
            Err(error) => {
                self.expire_if_unauthorized(generation, &error);
                Err(error.to_string())
            }
        }
    }

    async fn connections(&self) -> Result<Vec<MobileConnection>, String> {
        let (generation, token) = self.authorized_snapshot()?;
        let connections = self.fetch_connections(generation, &token).await?;
        let token = token.as_str();
        let connections = stream::iter(connections)
            .map(|connection| async move {
                if connection.kind != "hosted_runtime"
                    || connection.status != "starting"
                    || connection.readiness_path.is_none()
                    || !valid_connection_id(&connection.id)
                {
                    return connection;
                }
                match tokio::time::timeout(
                    READINESS_TIMEOUT,
                    self.refresh_hosted_connection(generation, token, &connection),
                )
                .await
                {
                    Ok(Ok(refreshed)) => refreshed,
                    Ok(Err(error)) => {
                        log::warn!("Hecate Cloud hosted runtime readiness refresh failed: {error}");
                        connection
                    }
                    Err(_) => {
                        log::warn!("Hecate Cloud hosted runtime readiness refresh timed out");
                        connection
                    }
                }
            })
            .buffered(MAX_READINESS_CONCURRENCY)
            .collect::<Vec<_>>()
            .await;
        self.accept_connections_response(generation, connections)
    }

    async fn refresh_hosted_connection(
        &self,
        generation: u64,
        token: &str,
        connection: &MobileConnection,
    ) -> Result<MobileConnection, String> {
        if !valid_connection_id(&connection.id) {
            return Err("Hecate Cloud returned an invalid connection identifier.".to_string());
        }
        let expected_path = format!("/api/v1/app/runtimes/{}/readiness", connection.id);
        let path = validated_server_path(
            connection.readiness_path.as_deref(),
            &expected_path,
            "hosted runtime readiness",
        )?;
        let refreshed = self
            .request_data::<MobileConnection>(reqwest::Method::GET, &path, token, None)
            .await
            .map_err(|error| {
                self.expire_if_unauthorized(generation, &error);
                error.to_string()
            })?;
        validate_refreshed_connection(connection, &refreshed)?;
        self.ensure_authorized_generation(generation)?;
        Ok(refreshed)
    }

    async fn start_connection(&self, connection_id: &str) -> Result<MobileConnection, String> {
        let connection_id = connection_id.trim();
        if !valid_connection_id(connection_id) {
            return Err("Choose a valid Hecate connection.".to_string());
        }
        let (generation, token) = self.authorized_snapshot()?;
        let _start_guard = self.begin_connection_start(connection_id)?;
        let connections = self.fetch_connections(generation, &token).await?;
        self.ensure_authorized_generation(generation)?;
        let connection = connections
            .into_iter()
            .find(|candidate| candidate.id == connection_id)
            .ok_or_else(|| "That Hecate connection is no longer available.".to_string())?;
        if connection.kind != "hosted_runtime" {
            return Err(
                "A phone cannot start a desktop Hecate. Open Hecate on that computer first."
                    .to_string(),
            );
        }
        if connection.reachable {
            return Ok(connection);
        }
        if connection.status == "starting" {
            return Ok(connection);
        }
        if !connection.can_start {
            return Err(format!(
                "{} cannot be started from the mobile app.",
                connection.name
            ));
        }
        let expected_path = format!("/api/v1/app/runtimes/{}/start", connection.id);
        let path = validated_server_path(
            connection.start_path.as_deref(),
            &expected_path,
            "hosted runtime start",
        )?;
        let started = self
            .request_data_with_timeout::<MobileConnection>(
                reqwest::Method::POST,
                &path,
                &token,
                None,
                START_REQUEST_TIMEOUT,
            )
            .await
            .map_err(|error| {
                self.expire_if_unauthorized(generation, &error);
                error.to_string()
            })?;
        validate_refreshed_connection(&connection, &started)?;
        self.ensure_authorized_generation(generation)?;
        Ok(started)
    }

    async fn prepare_open(
        &self,
        connection_id: &str,
    ) -> Result<(u64, MobileConnection, String), String> {
        let connection_id = connection_id.trim();
        if !valid_connection_id(connection_id) {
            return Err("Choose a valid Hecate connection.".to_string());
        }

        let (generation, token) = self.authorized_snapshot()?;
        // Re-fetch rather than trusting paths that originated in JavaScript.
        let connections = self.fetch_connections(generation, &token).await?;
        self.ensure_authorized_generation(generation)?;
        let connection = connections
            .into_iter()
            .find(|candidate| candidate.id == connection_id)
            .ok_or_else(|| "That Hecate connection is no longer available.".to_string())?;
        if !connection.reachable {
            return Err(format!("{} is currently offline.", connection.name));
        }

        let open_url = match connection.kind.as_str() {
            "hosted_runtime" => {
                let expected_path = format!("/api/v1/app/runtimes/{}/open", connection.id);
                let next = validated_server_path(
                    connection.browser_open_path.as_deref(),
                    &expected_path,
                    "hosted runtime",
                )?;
                let browser_session = self
                    .request_data::<BrowserSession>(
                        reqwest::Method::POST,
                        BROWSER_SESSIONS_PATH,
                        &token,
                        Some(serde_json::json!({ "next": next })),
                    )
                    .await
                    .map_err(|error| {
                        self.expire_if_unauthorized(generation, &error);
                        error.to_string()
                    })?;
                // Parsing expires_at is a Cloud/UI concern for now, but require
                // it to be present in the response contract.
                let _ = browser_session.expires_at;
                resolve_same_origin_url(&self.inner.cloud_url, &browser_session.open_url)?
            }
            "desktop_host" => {
                if !connection.remote_enabled {
                    return Err(format!("Remote access is off on {}.", connection.name));
                }
                let expected_path = format!("/api/v1/hosts/{}/remote-session", connection.id);
                let path = validated_server_path(
                    connection.remote_session_path.as_deref(),
                    &expected_path,
                    "desktop host",
                )?;
                let browser_session = self
                    .request_data::<RemoteBrowserSession>(
                        reqwest::Method::POST,
                        &path,
                        &token,
                        Some(serde_json::json!({
                            "org_id": connection.org_id,
                            "mode": "full_control"
                        })),
                    )
                    .await
                    .map_err(|error| {
                        self.expire_if_unauthorized(generation, &error);
                        error.to_string()
                    })?;
                let _ = browser_session.expires_at;
                resolve_same_origin_url(&self.inner.cloud_url, &browser_session.open_url)?
            }
            _ => return Err("This connection type is not supported by the mobile app.".to_string()),
        };
        Ok((generation, connection, open_url))
    }

    fn begin_connection_start(
        &self,
        connection_id: &str,
    ) -> Result<MobileConnectionStartGuard, String> {
        let mut starts = self
            .inner
            .connection_starts
            .lock()
            .map_err(|_| "The mobile connection start state is unavailable.".to_string())?;
        if !starts.insert(connection_id.to_string()) {
            return Err("Hecate Cloud is already starting this runtime.".to_string());
        }
        Ok(MobileConnectionStartGuard {
            inner: Arc::clone(&self.inner),
            connection_id: connection_id.to_string(),
        })
    }

    fn navigate_if_authorized_generation(
        &self,
        generation: u64,
        window: &tauri::WebviewWindow,
        target: tauri::Url,
        connection_name: &str,
    ) -> Result<(), String> {
        // Navigation is synchronous, so holding the session lock here gives
        // sign-out a strict boundary: either this navigation linearizes first,
        // or sign-out advances the generation and this request is rejected.
        let session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        if !session_allows_navigation(&session, generation) {
            return Err(
                "Your Hecate Cloud session changed before the connection opened.".to_string(),
            );
        }
        window.navigate(target).map_err(|_| {
            log::warn!("Hecate mobile could not navigate to a validated connection URL");
            format!("Could not open {connection_name}. Try again.")
        })
    }

    async fn reconcile_notifications(&self) {
        if !cfg!(target_os = "ios") {
            return;
        }
        native_push_refresh_authorization();
        let deadline = tokio::time::Instant::now() + PUSH_STATUS_WAIT;
        loop {
            let settled = self
                .inner
                .notifications
                .lock()
                .map(|push| push.authorization != MobileNotificationAuthorization::Unknown)
                .unwrap_or(true);
            if settled || tokio::time::Instant::now() >= deadline {
                break;
            }
            tokio::time::sleep(PUSH_POLL_INTERVAL).await;
        }

        let Ok((_generation, token)) = self.authorized_snapshot() else {
            return;
        };
        let (requested, authorization, stale_device) = self
            .inner
            .notifications
            .lock()
            .map(|push| {
                (
                    push.requested_enabled,
                    push.authorization,
                    push.device_id.clone(),
                )
            })
            .unwrap_or((false, MobileNotificationAuthorization::Unavailable, None));

        if !requested || authorization == MobileNotificationAuthorization::Denied {
            if let Some(device_id) = stale_device {
                native_push_unregister();
                match self.delete_push_device(&token, &device_id).await {
                    Ok(()) => self.clear_notification_device(&device_id),
                    Err(error) if error.status == Some(404) => {
                        self.clear_notification_device(&device_id)
                    }
                    Err(error) => self.set_notification_error(format!(
                        "Notifications are off on this iPhone, but Cloud cleanup could not be confirmed: {error}"
                    )),
                }
            }
            return;
        }

        if !authorization.allows_notifications() {
            return;
        }

        if let Err(error) = self.ensure_notifications_registered(false).await {
            self.set_notification_error(error);
        }
    }

    async fn wait_for_notification_authorization(&self) -> MobileNotificationAuthorization {
        let deadline = tokio::time::Instant::now() + PUSH_AUTHORIZATION_WAIT;
        loop {
            let authorization = self
                .inner
                .notifications
                .lock()
                .map(|push| push.authorization)
                .unwrap_or(MobileNotificationAuthorization::Unavailable);
            if !matches!(
                authorization,
                MobileNotificationAuthorization::Unknown
                    | MobileNotificationAuthorization::NotDetermined
            ) {
                return authorization;
            }
            if tokio::time::Instant::now() >= deadline {
                self.set_notification_error(
                    "iPhone did not finish the notification permission request. Try again."
                        .to_string(),
                );
                return authorization;
            }
            tokio::time::sleep(PUSH_POLL_INTERVAL).await;
        }
    }

    async fn ensure_notifications_registered(&self, explicit_retry: bool) -> Result<(), String> {
        if !cfg!(target_os = "ios") {
            return Ok(());
        }
        let (generation, bearer) = self.authorized_snapshot()?;
        let needs_apple_registration = {
            let mut push = self
                .inner
                .notifications
                .lock()
                .map_err(|_| "Notification status is unavailable.".to_string())?;
            if !push.requested_enabled || !push.authorization.allows_notifications() {
                return Ok(());
            }
            if push.registration == MobileNotificationRegistration::Registered
                && push.device_id.is_some()
                && push.session_generation == Some(generation)
            {
                return Ok(());
            }
            if push.registration == MobileNotificationRegistration::Failed && !explicit_retry {
                return Ok(());
            }
            if explicit_retry {
                push.last_error = None;
            }
            if push.apns_token.is_none() || push.environment.is_none() {
                push.registration = MobileNotificationRegistration::RegisteringWithApple;
                true
            } else {
                false
            }
        };

        if needs_apple_registration {
            if let Err(error) = native_push_register() {
                self.record_apns_failure(error.clone());
                return Err(error);
            }
        }

        let deadline = tokio::time::Instant::now() + PUSH_REGISTRATION_WAIT;
        let (apns_token, environment) = loop {
            self.ensure_authorized_generation(generation)?;
            let (active, token, failure) = {
                let snapshot = self
                    .inner
                    .notifications
                    .lock()
                    .map_err(|_| "Notification status is unavailable.".to_string())?;
                (
                    snapshot.requested_enabled && snapshot.authorization.allows_notifications(),
                    snapshot
                        .apns_token
                        .clone()
                        .zip(snapshot.environment.clone()),
                    (snapshot.registration == MobileNotificationRegistration::Failed)
                        .then(|| snapshot.last_error.clone().unwrap_or_default()),
                )
            };
            if !active {
                return Ok(());
            }
            if let Some(token) = token {
                break token;
            }
            if let Some(failure) = failure {
                return Err(if failure.trim().is_empty() {
                    "Apple Push Notification service registration failed.".to_string()
                } else {
                    failure
                });
            }
            if tokio::time::Instant::now() >= deadline {
                return Err(
                    "Apple Push Notification service did not return a device registration."
                        .to_string(),
                );
            }
            tokio::time::sleep(PUSH_POLL_INTERVAL).await;
        };

        if !valid_apns_token(&apns_token) {
            return Err("Apple returned an invalid push registration.".to_string());
        }
        if !matches!(environment.as_str(), "sandbox" | "production") {
            return Err("The signed app has an invalid APNs environment.".to_string());
        }
        let installation_id = native_push_installation_id()?;
        if !valid_push_installation_id(&installation_id) {
            return Err("This installation could not be identified securely.".to_string());
        }

        let (attempt, wait_for_existing) = {
            let mut push = self
                .inner
                .notifications
                .lock()
                .map_err(|_| "Notification status is unavailable.".to_string())?;
            if push.registration == MobileNotificationRegistration::RegisteringWithCloud
                && push.session_generation == Some(generation)
            {
                (0, true)
            } else {
                push.registration_attempt = push.registration_attempt.wrapping_add(1);
                push.registration = MobileNotificationRegistration::RegisteringWithCloud;
                push.session_generation = Some(generation);
                push.last_error = None;
                (push.registration_attempt, false)
            }
        };
        if wait_for_existing {
            return self
                .wait_for_cloud_notification_registration(generation)
                .await;
        }

        let result = self
            .request_data::<PushDevice>(
                reqwest::Method::POST,
                PUSH_DEVICES_PATH,
                &bearer,
                Some(serde_json::json!({
                    "device_token": apns_token,
                    "environment": environment,
                    "installation_id": installation_id,
                })),
            )
            .await;

        match result {
            Ok(device) => {
                let valid_response = valid_push_device_id(&device.id)
                    && device.platform == "ios"
                    && device.environment == environment;
                if !valid_response {
                    let error =
                        "Hecate Cloud returned an invalid push-device response.".to_string();
                    self.finish_notification_registration_failure(attempt, error.clone());
                    return Err(error);
                }
                match self.accept_push_registration(
                    generation,
                    attempt,
                    &apns_token,
                    &environment,
                    &device.id,
                ) {
                    Ok(true) => Ok(()),
                    Ok(false) => {
                        // Disable/sign-out/account switches may win while the
                        // Cloud POST is in flight. Never persist that stale
                        // success; delete it immediately with the bearer that
                        // created it.
                        self.cleanup_rejected_push_registration(&bearer, &device.id)
                            .await;
                        Ok(())
                    }
                    Err(error) => {
                        self.cleanup_rejected_push_registration(&bearer, &device.id)
                            .await;
                        self.finish_notification_registration_failure(attempt, error.clone());
                        Err(error)
                    }
                }
            }
            Err(error) => {
                self.expire_if_unauthorized(generation, &error);
                let message = error.to_string();
                self.finish_notification_registration_failure(attempt, message.clone());
                Err(message)
            }
        }
    }

    fn accept_push_registration(
        &self,
        generation: u64,
        attempt: u64,
        apns_token: &str,
        environment: &str,
        device_id: &str,
    ) -> Result<bool, String> {
        let session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        let mut push = self
            .inner
            .notifications
            .lock()
            .map_err(|_| "Notification status is unavailable.".to_string())?;
        if !push_registration_response_is_current(
            &session,
            &push,
            generation,
            attempt,
            apns_token,
            environment,
        ) {
            return Ok(false);
        }
        // Keep native persistence inside the same notification-state critical
        // section. Disable snapshots the device only after taking this lock,
        // so either this acceptance wins and Disable deletes it, or Disable
        // wins and the response is rejected above.
        native_push_set_registered_device_id(Some(device_id))?;
        push.registration = MobileNotificationRegistration::Registered;
        push.device_id = Some(device_id.to_string());
        push.last_error = None;
        Ok(true)
    }

    async fn cleanup_rejected_push_registration(&self, bearer: &str, device_id: &str) {
        match self.delete_push_device(bearer, device_id).await {
            Ok(())
            | Err(CloudAPIError {
                status: Some(404), ..
            }) => {}
            Err(_) => {
                // If the operator already disabled notifications, retain only
                // the non-secret device id as a pending cleanup marker. The
                // next authenticated reconciliation retries the DELETE.
                let should_queue = self
                    .inner
                    .notifications
                    .lock()
                    .map(|push| !push.requested_enabled && push.device_id.is_none())
                    .unwrap_or(false);
                if should_queue {
                    let _ = native_push_set_registered_device_id(Some(device_id));
                    if let Ok(mut push) = self.inner.notifications.lock() {
                        if !push.requested_enabled && push.device_id.is_none() {
                            push.device_id = Some(device_id.to_string());
                            push.registration = MobileNotificationRegistration::PendingDelete;
                        }
                    }
                }
            }
        }
    }

    async fn wait_for_cloud_notification_registration(
        &self,
        generation: u64,
    ) -> Result<(), String> {
        let deadline = tokio::time::Instant::now() + REQUEST_TIMEOUT;
        loop {
            self.ensure_authorized_generation(generation)?;
            let (registration, last_error) = {
                let push = self
                    .inner
                    .notifications
                    .lock()
                    .map_err(|_| "Notification status is unavailable.".to_string())?;
                (push.registration, push.last_error.clone())
            };
            match registration {
                MobileNotificationRegistration::Registered => return Ok(()),
                MobileNotificationRegistration::Failed => {
                    return Err(last_error
                        .unwrap_or_else(|| "Hecate Cloud push registration failed.".to_string()))
                }
                MobileNotificationRegistration::RegisteringWithCloud => {}
                _ => return Ok(()),
            }
            if tokio::time::Instant::now() >= deadline {
                return Err("Hecate Cloud push registration did not finish in time.".to_string());
            }
            tokio::time::sleep(PUSH_POLL_INTERVAL).await;
        }
    }

    fn finish_notification_registration_failure(&self, attempt: u64, error: String) {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return;
        };
        if push.registration_attempt != attempt {
            return;
        }
        push.registration = MobileNotificationRegistration::Failed;
        push.device_id = None;
        push.last_error = Some(error);
    }

    fn set_notification_error(&self, error: String) {
        if let Ok(mut push) = self.inner.notifications.lock() {
            push.registration = MobileNotificationRegistration::Failed;
            push.last_error = Some(error);
        }
    }

    fn notification_device_id(&self) -> Option<String> {
        self.inner
            .notifications
            .lock()
            .ok()
            .and_then(|push| push.device_id.clone())
    }

    fn clear_notification_device(&self, expected_device_id: &str) {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return;
        };
        if push.device_id.as_deref() != Some(expected_device_id) {
            return;
        }
        push.device_id = None;
        push.session_generation = None;
        push.registration = MobileNotificationRegistration::Idle;
        push.registration_attempt = push.registration_attempt.wrapping_add(1);
        push.last_error = None;
        let _ = native_push_set_registered_device_id(None);
    }

    fn clear_notifications_for_sign_out(&self) -> Option<String> {
        let Ok(mut push) = self.inner.notifications.lock() else {
            return None;
        };
        let device_id = push.device_id.clone();
        push.requested_enabled = false;
        push.apns_token = None;
        push.environment = None;
        push.session_generation = None;
        push.registration = if device_id.is_some() {
            MobileNotificationRegistration::PendingDelete
        } else {
            MobileNotificationRegistration::Idle
        };
        push.registration_attempt = push.registration_attempt.wrapping_add(1);
        push.last_error = None;
        let _ = native_push_set_requested_enabled(false);
        device_id
    }

    async fn delete_push_device(&self, token: &str, device_id: &str) -> Result<(), CloudAPIError> {
        if !valid_push_device_id(device_id) {
            return Err(CloudAPIError {
                status: None,
                message: "The stored Cloud push-device id is invalid.".to_string(),
            });
        }
        let path = format!("{PUSH_DEVICES_PATH}/{device_id}");
        self.request_without_data(reqwest::Method::DELETE, &path, token)
            .await
    }

    fn expire_if_unauthorized(&self, generation: u64, error: &CloudAPIError) {
        if error.status != Some(401) {
            return;
        }
        let expired = {
            let Ok(mut session) = self.inner.session.lock() else {
                return;
            };
            if session.generation != generation {
                return;
            }
            session.generation = session.generation.wrapping_add(1);
            session.phase = MobilePhase::SignedOut;
            session.token = None;
            session.approval_url = None;
            session.authorization_deadline = None;
            session.authorization_wake = None;
            session.account_email = None;
            session.message = "Your Hecate Cloud session expired. Sign in again.".to_string();
            session.last_error = None;
            true
        };
        if expired {
            self.clear_notifications_for_sign_out();
            native_push_unregister();
        }
    }

    fn clear_for_sign_out(&self) -> Result<Option<String>, String> {
        let mut session = self
            .inner
            .session
            .lock()
            .map_err(|_| "The mobile session is unavailable.".to_string())?;
        let token = session.token.take();
        session.generation = session.generation.wrapping_add(1);
        session.phase = MobilePhase::SignedOut;
        session.approval_url = None;
        session.authorization_deadline = None;
        session.authorization_wake = None;
        session.account_email = None;
        session.message = "Signed out of Hecate Cloud.".to_string();
        session.last_error = None;
        Ok(token)
    }

    fn set_sign_out_warning(&self, warning: String) {
        if let Ok(mut session) = self.inner.session.lock() {
            if session.phase == MobilePhase::SignedOut {
                session.last_error = Some(warning);
            }
        }
    }

    async fn request_data<T>(
        &self,
        method: reqwest::Method,
        path: &str,
        token: &str,
        body: Option<serde_json::Value>,
    ) -> Result<T, CloudAPIError>
    where
        T: DeserializeOwned,
    {
        self.request_data_with_timeout(method, path, token, body, REQUEST_TIMEOUT)
            .await
    }

    async fn request_data_with_timeout<T>(
        &self,
        method: reqwest::Method,
        path: &str,
        token: &str,
        body: Option<serde_json::Value>,
        timeout: Duration,
    ) -> Result<T, CloudAPIError>
    where
        T: DeserializeOwned,
    {
        let payload = self
            .request_bytes_with_timeout(method, path, Some(token), body, timeout)
            .await?;
        serde_json::from_slice::<CloudEnvelope<T>>(&payload)
            .map(|envelope| envelope.data)
            .map_err(|error| CloudAPIError {
                status: Some(200),
                message: format!("Hecate Cloud returned an invalid response: {error}"),
            })
    }

    async fn request_data_unauthenticated<T>(
        &self,
        method: reqwest::Method,
        path: &str,
        body: Option<serde_json::Value>,
    ) -> Result<T, CloudAPIError>
    where
        T: DeserializeOwned,
    {
        let payload = self.request_bytes(method, path, None, body).await?;
        serde_json::from_slice::<CloudEnvelope<T>>(&payload)
            .map(|envelope| envelope.data)
            .map_err(|error| CloudAPIError {
                status: Some(200),
                message: format!("Hecate Cloud returned an invalid response: {error}"),
            })
    }

    async fn request_without_data(
        &self,
        method: reqwest::Method,
        path: &str,
        token: &str,
    ) -> Result<(), CloudAPIError> {
        self.request_bytes(method, path, Some(token), None)
            .await
            .map(|_| ())
    }

    async fn request_bytes(
        &self,
        method: reqwest::Method,
        path: &str,
        token: Option<&str>,
        body: Option<serde_json::Value>,
    ) -> Result<Vec<u8>, CloudAPIError> {
        self.request_bytes_with_timeout(method, path, token, body, REQUEST_TIMEOUT)
            .await
    }

    async fn request_bytes_with_timeout(
        &self,
        method: reqwest::Method,
        path: &str,
        token: Option<&str>,
        body: Option<serde_json::Value>,
        timeout: Duration,
    ) -> Result<Vec<u8>, CloudAPIError> {
        let url = resolve_same_origin_url(&self.inner.cloud_url, path).map_err(|message| {
            CloudAPIError {
                status: None,
                message,
            }
        })?;
        let mut request = self
            .inner
            .http
            .request(method, url)
            .header("x-hecate-app-version", env!("CARGO_PKG_VERSION"))
            .timeout(timeout);
        if let Some(token) = token {
            request = request.bearer_auth(token);
        }
        if let Some(body) = body {
            request = request.json(&body);
        }
        let response = request.send().await.map_err(CloudAPIError::network)?;
        let status = response.status();
        let payload = response
            .bytes()
            .await
            .map_err(CloudAPIError::network)?
            .to_vec();
        if status.is_success() {
            return Ok(payload);
        }
        let message = serde_json::from_slice::<CloudErrorEnvelope>(&payload)
            .ok()
            .map(|body| body.error.message)
            .filter(|message| !message.trim().is_empty())
            .unwrap_or_else(|| format!("Hecate Cloud returned HTTP {}.", status.as_u16()));
        Err(CloudAPIError {
            status: Some(status.as_u16()),
            message,
        })
    }
}

fn session_allows_navigation(session: &MobileSession, generation: u64) -> bool {
    session.generation == generation
        && session.phase == MobilePhase::SignedIn
        && session.token.is_some()
}

fn push_registration_response_is_current(
    session: &MobileSession,
    push: &MobileNotificationSession,
    generation: u64,
    attempt: u64,
    apns_token: &str,
    environment: &str,
) -> bool {
    session_allows_navigation(session, generation)
        && push.requested_enabled
        && push.authorization.allows_notifications()
        && push.registration == MobileNotificationRegistration::RegisteringWithCloud
        && push.registration_attempt == attempt
        && push.session_generation == Some(generation)
        && push.apns_token.as_deref() == Some(apns_token)
        && push.environment.as_deref() == Some(environment)
}

fn status_from_session(session: &MobileSession, cloud_url: &str) -> MobileStatus {
    MobileStatus {
        available: true,
        phase: session.phase.as_str().to_string(),
        signed_in: session.phase == MobilePhase::SignedIn,
        authorizing: session.phase == MobilePhase::Authorizing,
        approval_page_available: session.phase == MobilePhase::Authorizing
            && session.approval_url.is_some(),
        account_email: session.account_email.clone(),
        cloud_url: cloud_url.to_string(),
        message: session.message.clone(),
        last_error: session.last_error.clone(),
        session_storage: SESSION_STORAGE,
        session_persists_after_exit: false,
    }
}

fn new_app_token() -> String {
    let mut raw = [0u8; 32];
    rand::rng().fill_bytes(&mut raw);
    format!("happ_{}", URL_SAFE_NO_PAD.encode(raw))
}

fn app_authorization_request_body(token: &str) -> serde_json::Value {
    serde_json::json!({
        "token": token,
        "client": MOBILE_AUTH_CLIENT,
    })
}

fn is_mobile_auth_callback_url(url: &tauri::Url) -> bool {
    url.as_str() == MOBILE_AUTH_CALLBACK_URL
}

fn validate_app_authorization(
    cloud_url: &str,
    authorization: AppAuthorization,
) -> Result<ValidatedAppAuthorization, String> {
    let valid_id = authorization.authorization_id.len() == 40
        && authorization.authorization_id.starts_with("appauth_")
        && authorization.authorization_id[8..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte));
    if !valid_id || authorization.expires_at.trim().is_empty() {
        return Err("Hecate Cloud returned an invalid sign-in authorization.".to_string());
    }
    let expires_in = authorization_expires_in(&authorization.expires_at)?;
    let approval_url = validate_approval_url(
        cloud_url,
        &authorization.approval_url,
        &authorization.authorization_id,
    )?;

    Ok(ValidatedAppAuthorization {
        approval_url,
        expires_in,
    })
}

fn validate_approval_url(
    cloud_url: &str,
    candidate: &str,
    authorization_id: &str,
) -> Result<String, String> {
    let approval_url = resolve_same_origin_url(cloud_url, candidate)?;
    let parsed = reqwest::Url::parse(&approval_url)
        .map_err(|_| "Hecate Cloud returned an invalid approval URL.".to_string())?;
    // The public request id is repeated in the query so each authorization is
    // a full browser navigation rather than a fragment-only navigation. Safari
    // may otherwise reuse an already-successful `/desktop-login` document and
    // leave the new fragment unprocessed. Keep the secret browser ticket in the
    // fragment, and require the query to match exactly so Cloud cannot append
    // another navigation parameter.
    let expected_query = format!("request={authorization_id}");
    if parsed.path() != "/desktop-login" || parsed.query() != Some(expected_query.as_str()) {
        return Err("Hecate Cloud returned an invalid approval URL.".to_string());
    }
    let fragment = parsed
        .fragment()
        .ok_or_else(|| "Hecate Cloud returned an invalid approval URL.".to_string())?;
    let authorization_prefix = format!("authorization_id={authorization_id}&browser_ticket=");
    let browser_ticket = fragment
        .strip_prefix(&authorization_prefix)
        .and_then(|value| value.strip_suffix("&client=mobile"))
        .ok_or_else(|| "Hecate Cloud returned an invalid approval URL.".to_string())?;
    let valid_ticket = {
        let ticket = browser_ticket;
        ticket.len() == 48
            && ticket.starts_with("hbat_")
            && ticket[5..]
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_')
    };
    if !valid_ticket {
        return Err("Hecate Cloud returned an invalid approval URL.".to_string());
    }
    Ok(approval_url)
}

fn stage_pending_authorization(
    session: &mut MobileSession,
    authorization: ValidatedAppAuthorization,
) -> String {
    let approval_url = authorization.approval_url.clone();
    session.approval_url = Some(authorization.approval_url);
    session.authorization_deadline = Some(tokio::time::Instant::now() + authorization.expires_in);
    session.message =
        "Continue in your browser. Hecate will return here automatically.".to_string();
    session.last_error = None;
    approval_url
}

fn record_authorization_open_failure(session: &mut MobileSession, retry: bool) -> String {
    session.message = "The approval page did not open. Try again below.".to_string();
    let detail = if retry {
        "Could not reopen Hecate Cloud sign-in. Try again."
    } else {
        "Could not open Hecate Cloud sign-in. Try again."
    };
    session.last_error = Some(detail.to_string());
    detail.to_string()
}

fn authorization_expires_in(raw: &str) -> Result<Duration, String> {
    let expires_at = chrono::DateTime::parse_from_rfc3339(raw)
        .map_err(|_| "Hecate Cloud returned an invalid sign-in expiry.".to_string())?;
    let now_millis = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "The iPhone clock is unavailable.".to_string())?
        .as_millis() as i128;
    let remaining_millis = i128::from(expires_at.timestamp_millis()) - now_millis;
    if remaining_millis <= 0 || remaining_millis > MAX_LOGIN_TIMEOUT.as_millis() as i128 {
        return Err("Hecate Cloud returned an invalid sign-in expiry.".to_string());
    }
    Ok(Duration::from_millis(remaining_millis as u64))
}

fn normalize_cloud_url(raw: &str) -> Result<String, String> {
    let mut url = reqwest::Url::parse(raw.trim())
        .map_err(|_| "The Hecate Cloud address is invalid.".to_string())?;
    let is_loopback = url
        .host_str()
        .map(|host| {
            host == "localhost"
                || host
                    .parse::<std::net::IpAddr>()
                    .is_ok_and(|ip| ip.is_loopback())
        })
        .unwrap_or(false);
    if url.scheme() != "https" && !(url.scheme() == "http" && is_loopback) {
        return Err(
            "Hecate Cloud must use HTTPS (HTTP is allowed only for loopback development)."
                .to_string(),
        );
    }
    if !url.username().is_empty() || url.password().is_some() {
        return Err("The Hecate Cloud address cannot include credentials.".to_string());
    }
    if url.path() != "/" || url.query().is_some() || url.fragment().is_some() {
        return Err(
            "The Hecate Cloud address must be an origin without a path, query, or fragment."
                .to_string(),
        );
    }
    url.set_path("");
    Ok(url.as_str().trim_end_matches('/').to_string())
}

fn resolve_same_origin_url(base: &str, candidate: &str) -> Result<String, String> {
    let base_url = reqwest::Url::parse(base)
        .map_err(|_| "The configured Hecate Cloud origin is invalid.".to_string())?;
    let resolved = base_url
        .join(candidate)
        .map_err(|_| "Hecate Cloud returned an invalid browser URL.".to_string())?;
    let same_origin = resolved.scheme() == base_url.scheme()
        && resolved.host_str() == base_url.host_str()
        && resolved.port_or_known_default() == base_url.port_or_known_default();
    if !same_origin || !resolved.username().is_empty() || resolved.password().is_some() {
        return Err(
            "Hecate Cloud returned a browser URL outside the configured origin.".to_string(),
        );
    }
    Ok(resolved.to_string())
}

fn validated_server_path(
    actual: Option<&str>,
    expected: &str,
    connection_label: &str,
) -> Result<String, String> {
    match actual {
        Some(path) if path == expected => Ok(path.to_string()),
        _ => Err(format!(
            "Hecate Cloud returned an invalid {connection_label} open path."
        )),
    }
}

fn valid_connection_id(connection_id: &str) -> bool {
    !connection_id.is_empty()
        && connection_id.len() <= 160
        && connection_id
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
}

fn validate_refreshed_connection(
    previous: &MobileConnection,
    refreshed: &MobileConnection,
) -> Result<(), String> {
    if refreshed.id != previous.id
        || refreshed.kind != previous.kind
        || refreshed.org_id != previous.org_id
        || refreshed.project_id != previous.project_id
    {
        return Err("Hecate Cloud returned a different hosted runtime.".to_string());
    }
    Ok(())
}

fn valid_apns_token(token: &str) -> bool {
    (64..=512).contains(&token.len())
        && token.len() % 2 == 0
        && token
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

fn valid_push_installation_id(installation_id: &str) -> bool {
    installation_id.len() == 47
        && installation_id.starts_with("hpi_")
        && installation_id[4..]
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_')
}

fn valid_push_device_id(device_id: &str) -> bool {
    device_id.len() == 37
        && device_id.starts_with("pdev_")
        && device_id[5..]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

#[cfg(target_os = "ios")]
static IOS_PUSH_CLOUD: OnceLock<MobileCloud> = OnceLock::new();

#[cfg(target_os = "ios")]
#[derive(Clone, Copy)]
#[repr(C)]
pub(crate) struct IOSPushBridgeV1 {
    abi_version: u32,
    refresh_authorization: Option<unsafe extern "C" fn()>,
    request_authorization: Option<unsafe extern "C" fn()>,
    register_for_remote_notifications: Option<unsafe extern "C" fn()>,
    open_settings: Option<unsafe extern "C" fn()>,
    unregister_for_remote_notifications: Option<unsafe extern "C" fn()>,
    requested_enabled: Option<unsafe extern "C" fn() -> bool>,
    set_requested_enabled: Option<unsafe extern "C" fn(bool)>,
    installation_identifier: Option<unsafe extern "C" fn() -> *const c_char>,
    registered_device_identifier: Option<unsafe extern "C" fn() -> *const c_char>,
    set_registered_device_identifier: Option<unsafe extern "C" fn(*const u8, usize)>,
}

#[cfg(target_os = "ios")]
static IOS_PUSH_BRIDGE: OnceLock<IOSPushBridgeV1> = OnceLock::new();

#[cfg(target_os = "ios")]
#[no_mangle]
pub extern "C" fn hecate_mobile_push_install_bridge(bridge: *const IOSPushBridgeV1) {
    if bridge.is_null() {
        return;
    }
    let bridge = unsafe { *bridge };
    let complete = bridge.abi_version == 1
        && bridge.refresh_authorization.is_some()
        && bridge.request_authorization.is_some()
        && bridge.register_for_remote_notifications.is_some()
        && bridge.open_settings.is_some()
        && bridge.unregister_for_remote_notifications.is_some()
        && bridge.requested_enabled.is_some()
        && bridge.set_requested_enabled.is_some()
        && bridge.installation_identifier.is_some()
        && bridge.registered_device_identifier.is_some()
        && bridge.set_registered_device_identifier.is_some();
    if complete {
        let _ = IOS_PUSH_BRIDGE.set(bridge);
    }
}

#[cfg(target_os = "ios")]
fn native_push_install_cloud(cloud: &MobileCloud) {
    let _ = IOS_PUSH_CLOUD.set(cloud.clone());
    native_push_refresh_authorization();
}

#[cfg(not(target_os = "ios"))]
fn native_push_install_cloud(_cloud: &MobileCloud) {}

#[cfg(target_os = "ios")]
fn native_push_refresh_authorization() {
    // The bridge dispatches all UIKit work to the main queue.
    if let Some(callback) = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.refresh_authorization)
    {
        unsafe { callback() };
    }
}

#[cfg(not(target_os = "ios"))]
fn native_push_refresh_authorization() {}

#[cfg(target_os = "ios")]
fn native_push_request_authorization() -> Result<(), String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.request_authorization)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    unsafe { callback() };
    Ok(())
}

#[cfg(not(target_os = "ios"))]
fn native_push_request_authorization() -> Result<(), String> {
    Err("Run notifications are currently available on iPhone only.".to_string())
}

#[cfg(target_os = "ios")]
fn native_push_register() -> Result<(), String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.register_for_remote_notifications)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    unsafe { callback() };
    Ok(())
}

#[cfg(not(target_os = "ios"))]
fn native_push_register() -> Result<(), String> {
    Err("Run notifications are currently available on iPhone only.".to_string())
}

#[cfg(target_os = "ios")]
fn native_push_open_settings() -> Result<(), String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.open_settings)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    unsafe { callback() };
    Ok(())
}

#[cfg(not(target_os = "ios"))]
fn native_push_open_settings() -> Result<(), String> {
    Err("Run notifications are currently available on iPhone only.".to_string())
}

#[cfg(target_os = "ios")]
fn native_push_unregister() {
    if let Some(callback) = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.unregister_for_remote_notifications)
    {
        unsafe { callback() };
    }
}

#[cfg(not(target_os = "ios"))]
fn native_push_unregister() {}

#[cfg(target_os = "ios")]
fn native_push_requested_enabled() -> bool {
    IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.requested_enabled)
        .map(|callback| unsafe { callback() })
        .unwrap_or(false)
}

#[cfg(not(target_os = "ios"))]
fn native_push_requested_enabled() -> bool {
    false
}

#[cfg(target_os = "ios")]
fn native_push_set_requested_enabled(enabled: bool) -> Result<(), String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.set_requested_enabled)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    unsafe { callback(enabled) };
    Ok(())
}

#[cfg(not(target_os = "ios"))]
fn native_push_set_requested_enabled(_enabled: bool) -> Result<(), String> {
    Err("Run notifications are currently available on iPhone only.".to_string())
}

#[cfg(target_os = "ios")]
fn native_push_installation_id() -> Result<String, String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.installation_identifier)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    let raw = unsafe { callback() };
    if raw.is_null() {
        return Err("This installation could not be identified securely.".to_string());
    }
    let installation_id = unsafe { CStr::from_ptr(raw) }
        .to_str()
        .map_err(|_| "This installation could not be identified securely.".to_string())?
        .to_string();
    if !valid_push_installation_id(&installation_id) {
        return Err("This installation could not be identified securely.".to_string());
    }
    Ok(installation_id)
}

#[cfg(not(target_os = "ios"))]
fn native_push_installation_id() -> Result<String, String> {
    Err("Run notifications are currently available on iPhone only.".to_string())
}

#[cfg(target_os = "ios")]
fn native_push_registered_device_id() -> Option<String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.registered_device_identifier)?;
    let raw = unsafe { callback() };
    if raw.is_null() {
        return None;
    }
    let device_id = unsafe { CStr::from_ptr(raw) }.to_str().ok()?.to_string();
    valid_push_device_id(&device_id).then_some(device_id)
}

#[cfg(not(target_os = "ios"))]
fn native_push_registered_device_id() -> Option<String> {
    None
}

#[cfg(target_os = "ios")]
fn native_push_set_registered_device_id(device_id: Option<&str>) -> Result<(), String> {
    let callback = IOS_PUSH_BRIDGE
        .get()
        .and_then(|bridge| bridge.set_registered_device_identifier)
        .ok_or_else(|| "The native notification bridge is unavailable.".to_string())?;
    match device_id {
        Some(device_id) if valid_push_device_id(device_id) => unsafe {
            callback(device_id.as_ptr(), device_id.len())
        },
        Some(_) => return Err("The Cloud push-device id is invalid.".to_string()),
        None => unsafe { callback(std::ptr::null(), 0) },
    }
    Ok(())
}

#[cfg(not(target_os = "ios"))]
fn native_push_set_registered_device_id(_device_id: Option<&str>) -> Result<(), String> {
    Ok(())
}

#[cfg(target_os = "ios")]
fn native_callback_error(raw: *const c_char) -> Option<String> {
    if raw.is_null() {
        return None;
    }
    let message = unsafe { CStr::from_ptr(raw) }
        .to_string_lossy()
        .chars()
        .take(320)
        .collect::<String>();
    (!message.trim().is_empty()).then_some(message)
}

/// Receives only Apple authorization state and a bounded, non-secret error.
/// No notification payload or identifier is accepted by this bridge.
#[cfg(target_os = "ios")]
#[no_mangle]
pub extern "C" fn hecate_mobile_push_authorization_changed(status: i32, error: *const c_char) {
    let authorization = match status {
        0 => MobileNotificationAuthorization::NotDetermined,
        1 => MobileNotificationAuthorization::Denied,
        2 => MobileNotificationAuthorization::Authorized,
        3 => MobileNotificationAuthorization::Provisional,
        4 => MobileNotificationAuthorization::Ephemeral,
        _ => MobileNotificationAuthorization::Unknown,
    };
    if let Some(cloud) = IOS_PUSH_CLOUD.get() {
        cloud.record_notification_authorization(authorization, native_callback_error(error));
    }
}

#[cfg(target_os = "ios")]
#[no_mangle]
pub extern "C" fn hecate_mobile_push_registered(bytes: *const u8, length: usize, environment: i32) {
    if bytes.is_null() || !(32..=256).contains(&length) {
        if let Some(cloud) = IOS_PUSH_CLOUD.get() {
            cloud.record_apns_failure("Apple returned an invalid push registration.".to_string());
        }
        return;
    }
    let raw = unsafe { std::slice::from_raw_parts(bytes, length) };
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut token = String::with_capacity(raw.len() * 2);
    for byte in raw {
        token.push(HEX[(byte >> 4) as usize] as char);
        token.push(HEX[(byte & 0x0f) as usize] as char);
    }
    let environment = match environment {
        1 => "sandbox",
        2 => "production",
        _ => {
            if let Some(cloud) = IOS_PUSH_CLOUD.get() {
                cloud.record_apns_failure(
                    "The signed app is missing a valid APNs environment.".to_string(),
                );
            }
            return;
        }
    };
    if let Some(cloud) = IOS_PUSH_CLOUD.get() {
        cloud.record_apns_token(token, environment.to_string());
    }
}

#[cfg(target_os = "ios")]
#[no_mangle]
pub extern "C" fn hecate_mobile_push_registration_failed(error: *const c_char) {
    let error = native_callback_error(error)
        .unwrap_or_else(|| "Apple Push Notification service registration failed.".to_string());
    if let Some(cloud) = IOS_PUSH_CLOUD.get() {
        cloud.record_apns_failure(error);
    }
}

#[tauri::command]
fn mobile_status(cloud: tauri::State<'_, MobileCloud>) -> MobileStatus {
    cloud.status()
}

#[tauri::command]
async fn mobile_sign_in(
    app: AppHandle,
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileStatus, String> {
    let (generation, token) = cloud.begin_sign_in()?;
    let authorization = match cloud.create_authorization(&token).await {
        Ok(authorization) => authorization,
        Err(error) => {
            let detail = error.to_string();
            cloud.fail_authorization(
                generation,
                "Sign-in could not start. Try again.",
                Some(detail.clone()),
            );
            return Err(detail);
        }
    };
    let open_result = cloud.open_authorization_in_browser(generation, authorization, &app);
    let worker = cloud.inner().clone();
    tauri::async_runtime::spawn(async move {
        worker.poll_authorization(generation).await;
    });
    open_result?;
    Ok(cloud.status())
}

#[tauri::command]
fn mobile_reopen_authorization(
    app: AppHandle,
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileStatus, String> {
    cloud.reopen_authorization_in_browser(&app)?;
    Ok(cloud.status())
}

#[tauri::command]
async fn mobile_connections(
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<Vec<MobileConnection>, String> {
    cloud.connections().await
}

#[tauri::command]
async fn mobile_notification_status(
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileNotificationStatus, String> {
    cloud.reconcile_notifications().await;
    Ok(cloud.notification_status())
}

#[tauri::command]
async fn mobile_enable_notifications(
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileNotificationStatus, String> {
    cloud.authorized_snapshot()?;
    cloud.set_notifications_requested(true)?;
    if let Err(error) = native_push_request_authorization() {
        cloud.set_notification_error(error);
        return Ok(cloud.notification_status());
    }
    let authorization = cloud.wait_for_notification_authorization().await;
    if authorization.allows_notifications() {
        if let Err(error) = cloud.ensure_notifications_registered(true).await {
            cloud.set_notification_error(error);
        }
    }
    Ok(cloud.notification_status())
}

#[tauri::command]
fn mobile_open_notification_settings(
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileNotificationStatus, String> {
    native_push_open_settings()?;
    Ok(cloud.notification_status())
}

#[tauri::command]
async fn mobile_disable_notifications(
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileNotificationStatus, String> {
    let authorized = cloud.authorized_snapshot().ok();
    cloud.set_notifications_requested(false)?;
    let registered = cloud.notification_device_id();
    native_push_unregister();
    if let (Some((_generation, token)), Some(device_id)) = (authorized, registered) {
        match cloud.delete_push_device(&token, &device_id).await {
            Ok(()) => cloud.clear_notification_device(&device_id),
            Err(error) if error.status == Some(404) => {
                cloud.clear_notification_device(&device_id)
            }
            Err(error) => cloud.set_notification_error(format!(
                "Notifications are off on this iPhone, but Cloud cleanup could not be confirmed: {error}"
            )),
        }
    }
    Ok(cloud.notification_status())
}

#[tauri::command]
async fn mobile_open_connection(
    window: tauri::WebviewWindow,
    cloud: tauri::State<'_, MobileCloud>,
    connection_id: String,
) -> Result<MobileOpenResult, String> {
    let (generation, connection, open_url) = cloud.prepare_open(&connection_id).await?;
    let target = tauri::Url::parse(&open_url)
        .map_err(|_| "Hecate Cloud returned an invalid secure session URL.".to_string())?;
    // Keep the single-use bootstrap inside the app-controlled WebView. The
    // native layer has already constrained it to the configured Cloud origin;
    // it is never returned to JavaScript, logged, or handed to an external
    // browser. The bootstrap response installs the HttpOnly browser cookie and
    // redirects this WebView to the selected Hecate runtime.
    cloud.navigate_if_authorized_generation(generation, &window, target, &connection.name)?;
    Ok(MobileOpenResult {
        connection_id: connection.id,
        name: connection.name,
        message: "A secure Hecate session was opened in the app.".to_string(),
    })
}

#[tauri::command]
async fn mobile_start_connection(
    cloud: tauri::State<'_, MobileCloud>,
    connection_id: String,
) -> Result<MobileStartResult, String> {
    let connection = cloud.start_connection(&connection_id).await?;
    Ok(MobileStartResult {
        connection_id: connection.id,
        name: connection.name,
        message: if connection.reachable {
            "This Hecate runtime is ready to open.".to_string()
        } else {
            "Hecate Cloud is starting this runtime.".to_string()
        },
    })
}

#[tauri::command]
async fn mobile_sign_out(
    app: AppHandle,
    window: tauri::WebviewWindow,
    cloud: tauri::State<'_, MobileCloud>,
) -> Result<MobileStatus, String> {
    let push_device_id = cloud.clear_notifications_for_sign_out();
    let token = cloud.clear_for_sign_out()?;
    native_push_unregister();
    let mut warnings = Vec::new();
    if let Err(error) = clear_mobile_browsing_data(&app, &window) {
        log::warn!("Hecate mobile could not clear sign-out browsing data: {error}");
        warnings.push("The in-app browser session could not be cleared; restart Hecate before another person uses this device.".to_string());
    }
    if let Some(token) = token {
        if let Some(device_id) = push_device_id {
            match cloud.delete_push_device(&token, &device_id).await {
                Ok(()) => cloud.clear_notification_device(&device_id),
                Err(error) if error.status == Some(404) => {
                    cloud.clear_notification_device(&device_id)
                }
                Err(error) => warnings.push(format!(
                    "Notifications are off on this iPhone, but Cloud cleanup could not be confirmed: {error}"
                )),
            }
        }
        if let Err(error) = cloud
            .request_without_data(reqwest::Method::POST, CURRENT_SESSION_PATH, &token)
            .await
        {
            // Local logout always wins. A network warning is useful, but the
            // in-memory credential must stay cleared even if revocation fails.
            if error.status != Some(401) {
                warnings.push(format!(
                    "Signed out on this device, but Cloud session revocation could not be confirmed: {error}"
                ));
            }
        }
    }
    if !warnings.is_empty() {
        cloud.set_sign_out_warning(warnings.join(" "));
    }
    log::info!("Hecate mobile Cloud session cleared from memory");
    Ok(cloud.status())
}

#[cfg(mobile)]
#[tauri::mobile_entry_point]
pub fn run() {
    use tauri::Manager as _;
    use tauri_plugin_deep_link::DeepLinkExt as _;
    use tauri_plugin_log::{RotationStrategy, Target, TargetKind, TimezoneStrategy};

    let cloud = MobileCloud::from_environment().expect("valid Hecate Cloud mobile configuration");
    native_push_install_cloud(&cloud);
    tauri::Builder::default()
        .plugin(
            tauri_plugin_log::Builder::new()
                .level(log::LevelFilter::Info)
                .targets([
                    Target::new(TargetKind::Stderr),
                    Target::new(TargetKind::LogDir {
                        file_name: Some("app".into()),
                    }),
                    Target::new(TargetKind::Webview),
                ])
                .max_file_size(2 * 1024 * 1024)
                .rotation_strategy(RotationStrategy::KeepSome(3))
                .timezone_strategy(TimezoneStrategy::UseLocal)
                .build(),
        )
        .plugin(tauri_plugin_deep_link::init())
        .plugin(mobile_cookie_manager_plugin())
        .plugin(mobile_navigation_plugin())
        .plugin(tauri_plugin_opener::init())
        .manage(cloud)
        .invoke_handler(tauri::generate_handler![
            mobile_status,
            mobile_sign_in,
            mobile_reopen_authorization,
            mobile_connections,
            mobile_notification_status,
            mobile_enable_notifications,
            mobile_open_notification_settings,
            mobile_disable_notifications,
            mobile_open_connection,
            mobile_start_connection,
            mobile_sign_out
        ])
        .setup(|app| {
            let window = app.get_webview_window("main").ok_or_else(|| {
                std::io::Error::other("Hecate mobile main WebView is unavailable")
            })?;
            let navigation =
                MobileShellNavigation::capture(window.url()?).map_err(std::io::Error::other)?;
            app.manage(navigation);
            let callback_cloud = app.state::<MobileCloud>().inner().clone();
            app.deep_link().on_open_url(move |event| {
                if event.urls().iter().any(is_mobile_auth_callback_url) {
                    callback_cloud.note_authorization_callback();
                }
            });
            if app
                .deep_link()
                .get_current()
                .ok()
                .flatten()
                .is_some_and(|urls| urls.iter().any(is_mobile_auth_callback_url))
            {
                app.state::<MobileCloud>()
                    .inner()
                    .note_authorization_callback();
            }
            if let Err(error) = clear_mobile_browsing_data(app.handle(), &window) {
                log::warn!("Hecate mobile could not clear prior WebView browsing data: {error}");
            }
            log::info!(
                "Hecate mobile starting version={} credential_storage=memory_only",
                env!("CARGO_PKG_VERSION")
            );
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error running Hecate mobile app");
}

#[cfg(test)]
mod tests {
    use super::*;

    fn authorization_expiry_after(duration: Duration) -> String {
        chrono::DateTime::<chrono::Utc>::from(SystemTime::now() + duration)
            .to_rfc3339_opts(chrono::SecondsFormat::Millis, true)
    }

    #[test]
    fn app_tokens_match_the_cloud_app_session_contract() {
        let first = new_app_token();
        let second = new_app_token();
        assert!(first.starts_with("happ_"));
        assert_eq!(first.len(), 48);
        assert_ne!(first, second);
        assert!(first[5..]
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_'));
    }

    #[test]
    fn app_authorization_request_identifies_the_mobile_client() {
        let token = "happ_test";
        assert_eq!(
            app_authorization_request_body(token),
            serde_json::json!({
                "token": token,
                "client": "mobile",
            })
        );
    }

    #[test]
    fn app_authorization_response_rejects_removed_manual_code_fields() {
        let body = r#"{
          "authorization_id":"appauth_0123456789abcdef0123456789abcdef",
          "approval_url":"https://console.hecatehq.com/desktop-login?request=appauth_0123456789abcdef0123456789abcdef#authorization_id=appauth_0123456789abcdef0123456789abcdef&browser_ticket=hbat_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&client=mobile",
          "expires_at":"2026-07-23T12:00:00Z",
          "user_code":"ABCD-EFGH"
        }"#;
        assert!(serde_json::from_str::<AppAuthorization>(body).is_err());
    }

    #[test]
    fn mobile_auth_callback_is_fixed_and_secret_free() {
        assert!(is_mobile_auth_callback_url(
            &tauri::Url::parse(MOBILE_AUTH_CALLBACK_URL).unwrap()
        ));
        for candidate in [
            "hecate-mobile://auth/",
            "hecate-mobile://auth/complete/",
            "hecate-mobile://auth/complete?authorization_id=appauth_1",
            "hecate-mobile://auth/complete#browser_ticket=hbat_secret",
            "hecate-mobile://other/complete",
            "https://console.hecatehq.com/auth/complete",
        ] {
            assert!(!is_mobile_auth_callback_url(
                &tauri::Url::parse(candidate).unwrap()
            ));
        }
    }

    #[tokio::test]
    async fn mobile_auth_callback_wakes_only_the_pending_authorization_poll() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        cloud.begin_sign_in().unwrap();
        {
            let mut session = cloud.inner.session.lock().unwrap();
            session.approval_url = Some(
                "https://console.hecatehq.com/desktop-login?request=appauth_0123456789abcdef0123456789abcdef"
                    .to_string(),
            );
            session.authorization_deadline =
                Some(tokio::time::Instant::now() + Duration::from_secs(60));
        }

        let generation_wake = cloud
            .inner
            .session
            .lock()
            .unwrap()
            .authorization_wake
            .clone()
            .unwrap();
        let wake = generation_wake.notified();
        cloud.note_authorization_callback();
        tokio::time::timeout(Duration::from_millis(100), wake)
            .await
            .expect("pending poll wake");
        {
            let session = cloud.inner.session.lock().unwrap();
            assert_eq!(session.message, "Finishing sign-in…");
            assert_eq!(session.phase, MobilePhase::Authorizing);
            assert!(session.token.is_some());
        }

        cloud.clear_for_sign_out().unwrap();
        cloud.begin_sign_in().unwrap();
        {
            let mut session = cloud.inner.session.lock().unwrap();
            session.approval_url = Some(
                "https://console.hecatehq.com/desktop-login?request=appauth_ffffffffffffffffffffffffffffffff"
                    .to_string(),
            );
            session.authorization_deadline =
                Some(tokio::time::Instant::now() + Duration::from_secs(60));
        }
        let replacement_wake = cloud
            .inner
            .session
            .lock()
            .unwrap()
            .authorization_wake
            .clone()
            .unwrap();
        let stale_generation = generation_wake.notified();
        let replacement_generation = replacement_wake.notified();
        cloud.note_authorization_callback();
        tokio::time::timeout(Duration::from_millis(100), replacement_generation)
            .await
            .expect("replacement generation poll wake");
        assert!(
            tokio::time::timeout(Duration::from_millis(10), stale_generation)
                .await
                .is_err(),
            "replacement callback woke the stale generation"
        );

        cloud.clear_for_sign_out().unwrap();
        let signed_out_wake = replacement_wake.notified();
        cloud.note_authorization_callback();
        assert!(
            tokio::time::timeout(Duration::from_millis(10), signed_out_wake)
                .await
                .is_err()
        );
    }

    #[test]
    fn cloud_origin_requires_https_except_for_loopback_development() {
        assert_eq!(
            normalize_cloud_url(" https://console.hecatehq.com/ ").unwrap(),
            DEFAULT_CLOUD_URL
        );
        assert_eq!(
            normalize_cloud_url("http://127.0.0.1:8787/").unwrap(),
            "http://127.0.0.1:8787"
        );
        assert!(normalize_cloud_url("http://example.com").is_err());
        assert!(normalize_cloud_url("https://example.com/path").is_err());
        assert!(normalize_cloud_url("https://user@example.com").is_err());
        assert!(normalize_cloud_url("https://example.com?next=elsewhere").is_err());
    }

    #[test]
    fn browser_urls_are_resolved_only_within_the_cloud_origin() {
        assert_eq!(
            resolve_same_origin_url(DEFAULT_CLOUD_URL, "/api/v1/app/browser-sessions/open?t=1")
                .unwrap(),
            "https://console.hecatehq.com/api/v1/app/browser-sessions/open?t=1"
        );
        assert!(resolve_same_origin_url(DEFAULT_CLOUD_URL, "https://evil.example/open").is_err());
        assert!(resolve_same_origin_url(DEFAULT_CLOUD_URL, "//evil.example/open").is_err());
    }

    #[test]
    fn app_authorization_requires_a_strict_one_time_approval_url() {
        let authorization_id = "appauth_0123456789abcdef0123456789abcdef";
        let browser_ticket = format!("hbat_{}", "A".repeat(43));
        let approval_url = format!(
            "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
        );
        let authorization = validate_app_authorization(
            DEFAULT_CLOUD_URL,
            AppAuthorization {
                authorization_id: authorization_id.to_string(),
                approval_url: approval_url.clone(),
                expires_at: authorization_expiry_after(Duration::from_secs(10 * 60)),
            },
        )
        .unwrap();
        assert_eq!(authorization.approval_url, approval_url);
        assert!(authorization.expires_in > Duration::from_secs(9 * 60));
        assert!(authorization.expires_in <= Duration::from_secs(10 * 60));
        assert!(!authorization.approval_url.contains("happ_"));

        for invalid_approval_url in [
            format!(
                "https://evil.example/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request=appauth_ffffffffffffffffffffffffffffffff#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}&next=bad#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id=appauth_ffffffffffffffffffffffffffffffff&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket=hbat_short&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile&extra=1"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&authorization_id={authorization_id}&client=mobile"
            ),
            format!(
                "https://user@console.hecatehq.com/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#%61uthorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=desktop"
            ),
            format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#client=mobile&authorization_id={authorization_id}&browser_ticket={browser_ticket}"
            ),
        ] {
            assert!(validate_app_authorization(
                DEFAULT_CLOUD_URL,
                AppAuthorization {
                    authorization_id: authorization_id.to_string(),
                    approval_url: invalid_approval_url,
                    expires_at: authorization_expiry_after(Duration::from_secs(10 * 60)),
                }
            )
            .is_err());
        }

        for invalid_expiry in [
            authorization_expiry_after(Duration::from_secs(0)),
            authorization_expiry_after(MAX_LOGIN_TIMEOUT + Duration::from_secs(1)),
            "not-a-timestamp".to_string(),
        ] {
            assert!(validate_app_authorization(
                DEFAULT_CLOUD_URL,
                AppAuthorization {
                    authorization_id: authorization_id.to_string(),
                    approval_url: approval_url.clone(),
                    expires_at: invalid_expiry,
                }
            )
            .is_err());
        }
    }

    #[test]
    fn mobile_approval_request_query_forces_a_new_browser_document() {
        let authorization_id = "appauth_0123456789abcdef0123456789abcdef";
        let browser_ticket = format!("hbat_{}", "A".repeat(43));
        let fragment = format!(
            "authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
        );

        assert!(validate_approval_url(
            DEFAULT_CLOUD_URL,
            &format!("{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#{fragment}"),
            authorization_id,
        )
        .is_ok());

        for query in [
            String::new(),
            "?request=appauth_ffffffffffffffffffffffffffffffff".to_string(),
            format!("?request={authorization_id}&next=elsewhere"),
        ] {
            assert!(
                validate_approval_url(
                    DEFAULT_CLOUD_URL,
                    &format!("{DEFAULT_CLOUD_URL}/desktop-login{query}#{fragment}"),
                    authorization_id,
                )
                .is_err(),
                "unexpectedly accepted query {query:?}"
            );
        }
    }

    #[test]
    fn approval_url_stays_native_only_and_is_cleared_with_the_session() {
        let authorization_id = "appauth_0123456789abcdef0123456789abcdef";
        let browser_ticket = format!("hbat_{}", "A".repeat(43));
        let approval_url = format!(
            "{DEFAULT_CLOUD_URL}/desktop-login?request={authorization_id}#authorization_id={authorization_id}&browser_ticket={browser_ticket}&client=mobile"
        );
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let (generation, _) = cloud.begin_sign_in().unwrap();
        let authorization = validate_app_authorization(
            DEFAULT_CLOUD_URL,
            AppAuthorization {
                authorization_id: authorization_id.to_string(),
                approval_url: approval_url.clone(),
                expires_at: authorization_expiry_after(Duration::from_secs(10 * 60)),
            },
        )
        .unwrap();
        {
            let mut session = cloud.inner.session.lock().unwrap();
            stage_pending_authorization(&mut session, authorization);
            assert_eq!(session.approval_url.as_deref(), Some(approval_url.as_str()));
            assert!(session.authorization_deadline.is_some());
        }

        let serialized = serde_json::to_string(&cloud.status()).unwrap();
        assert!(cloud.status().approval_page_available);
        assert!(!serialized.contains(authorization_id));
        assert!(!serialized.contains(&browser_ticket));
        assert!(!serialized.contains("desktop-login"));

        cloud.complete_authorization(generation, "mobile@example.com".to_string());
        let session = cloud.inner.session.lock().unwrap();
        assert!(session.approval_url.is_none());
        assert!(session.authorization_deadline.is_none());
    }

    #[test]
    fn approval_open_failure_never_exposes_the_browser_ticket() {
        let browser_ticket = format!("hbat_{}", "A".repeat(43));
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        cloud.begin_sign_in().unwrap();
        {
            let mut session = cloud.inner.session.lock().unwrap();
            session.approval_url = Some(format!(
                "{DEFAULT_CLOUD_URL}/desktop-login?request=appauth_0123456789abcdef0123456789abcdef#authorization_id=appauth_0123456789abcdef0123456789abcdef&browser_ticket={browser_ticket}&client=mobile"
            ));
            session.authorization_deadline =
                Some(tokio::time::Instant::now() + Duration::from_secs(60));
            assert_eq!(
                record_authorization_open_failure(&mut session, false),
                "Could not open Hecate Cloud sign-in. Try again."
            );
        }

        let serialized = serde_json::to_string(&cloud.status()).unwrap();
        assert!(!serialized.contains(&browser_ticket));
        assert!(!serialized.contains("desktop-login"));
        assert!(!serialized.contains("authorization_id"));
    }

    #[test]
    fn connection_response_matches_cloud_envelope_and_hides_open_paths() {
        let body = r#"{
          "object":"list",
          "data":[{
            "id":"runtime_1",
            "kind":"hosted_runtime",
            "org_id":"org_1",
            "project_id":"project_1",
            "name":"Production",
            "status":"online",
            "reachable":true,
            "can_start":false,
            "version":"0.5.0",
            "capabilities":["chat","tasks"],
            "last_seen_at":"2026-07-21T10:00:00Z",
            "browser_open_path":"/api/v1/app/runtimes/runtime_1/open",
            "start_path":"/api/v1/app/runtimes/runtime_1/start",
            "readiness_path":"/api/v1/app/runtimes/runtime_1/readiness"
          }]
        }"#;
        let envelope: CloudEnvelope<Vec<MobileConnection>> = serde_json::from_str(body).unwrap();
        let connection = &envelope.data[0];
        assert_eq!(connection.kind, "hosted_runtime");
        assert!(connection.reachable);
        assert!(!connection.can_start);
        let serialized = serde_json::to_string(connection).unwrap();
        assert!(!serialized.contains("browser_open_path"));
        assert!(!serialized.contains("start_path"));
        assert!(!serialized.contains("readiness_path"));
        assert!(!serialized.contains("/api/v1/app/runtimes"));
    }

    #[test]
    fn server_open_paths_must_match_the_selected_connection() {
        let expected = "/api/v1/app/runtimes/runtime_1/open";
        assert_eq!(
            validated_server_path(Some(expected), expected, "hosted runtime").unwrap(),
            expected
        );
        assert!(validated_server_path(
            Some("/api/v1/app/runtimes/runtime_2/open"),
            expected,
            "hosted runtime"
        )
        .is_err());
        assert!(validated_server_path(
            Some("https://evil.example/open"),
            expected,
            "hosted runtime"
        )
        .is_err());

        for (actual, expected, label) in [
            (
                "/api/v1/app/runtimes/runtime_1/start",
                "/api/v1/app/runtimes/runtime_1/start",
                "hosted runtime start",
            ),
            (
                "/api/v1/app/runtimes/runtime_1/readiness",
                "/api/v1/app/runtimes/runtime_1/readiness",
                "hosted runtime readiness",
            ),
        ] {
            assert_eq!(
                validated_server_path(Some(actual), expected, label).unwrap(),
                expected
            );
        }
    }

    #[test]
    fn connection_ids_are_safe_single_path_segments() {
        for valid in [
            "runtime_00112233445566778899aabb",
            "host-01234567-89ab-cdef-0123-456789abcdef",
            "runtime_1",
        ] {
            assert!(valid_connection_id(valid));
        }
        for invalid in [
            "",
            " runtime_1",
            "runtime_1 ",
            "runtime/../admin",
            "runtime%2Fadmin",
            "runtime?next=admin",
            "runtime#fragment",
        ] {
            assert!(!valid_connection_id(invalid));
        }
        assert!(!valid_connection_id(&"a".repeat(161)));
    }

    #[test]
    fn connection_start_guard_admits_one_id_until_drop() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let first = cloud.begin_connection_start("runtime_1").unwrap();
        assert!(cloud.begin_connection_start("runtime_1").is_err());
        assert!(cloud.begin_connection_start("runtime_2").is_ok());
        drop(first);
        assert!(cloud.begin_connection_start("runtime_1").is_ok());
    }

    #[test]
    fn hosted_runtime_start_timeout_exceeds_the_cloud_wait_contract() {
        assert!(START_REQUEST_TIMEOUT > Duration::from_secs(60));
    }

    #[test]
    fn hosted_runtime_refresh_cannot_switch_connection_identity() {
        let previous = MobileConnection {
            id: "runtime_1".to_string(),
            kind: "hosted_runtime".to_string(),
            org_id: "org_1".to_string(),
            project_id: Some("project_1".to_string()),
            name: "Production".to_string(),
            status: "starting".to_string(),
            reachable: false,
            can_start: false,
            remote_enabled: false,
            version: None,
            capabilities: Vec::new(),
            last_seen_at: None,
            browser_open_path: None,
            remote_session_path: None,
            start_path: None,
            readiness_path: Some("/api/v1/app/runtimes/runtime_1/readiness".to_string()),
        };
        let mut refreshed = previous.clone();
        refreshed.status = "online".to_string();
        refreshed.reachable = true;
        assert!(validate_refreshed_connection(&previous, &refreshed).is_ok());

        refreshed.id = "runtime_2".to_string();
        assert!(validate_refreshed_connection(&previous, &refreshed).is_err());
        refreshed.id = previous.id.clone();
        refreshed.org_id = "org_2".to_string();
        assert!(validate_refreshed_connection(&previous, &refreshed).is_err());
    }

    #[test]
    fn status_never_serializes_the_in_memory_token() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let (_, _) = cloud.begin_sign_in().unwrap();
        let token = cloud.inner.session.lock().unwrap().token.clone().unwrap();
        let status = serde_json::to_string(&cloud.status()).unwrap();
        assert!(!status.contains(&token));
        assert!(!status.contains("happ_"));
        assert!(status.contains("memory_only"));
        assert!(status.contains("session_persists_after_exit\":false"));
    }

    #[test]
    fn signing_out_irrevocably_clears_the_process_token() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let (generation, _) = cloud.begin_sign_in().unwrap();
        cloud.complete_authorization(generation, "mobile@example.com".to_string());
        assert!(session_allows_navigation(
            &cloud.inner.session.lock().unwrap(),
            generation
        ));
        let token = cloud.clear_for_sign_out().unwrap().unwrap();
        assert!(token.starts_with("happ_"));
        assert!(cloud.inner.session.lock().unwrap().token.is_none());
        let status = cloud.status();
        assert_eq!(status.phase, "signed_out");
        assert!(!status.signed_in);
        assert!(!session_allows_navigation(
            &cloud.inner.session.lock().unwrap(),
            generation
        ));
    }

    #[test]
    fn disable_wins_over_an_in_flight_push_registration_response() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let (generation, _) = cloud.begin_sign_in().unwrap();
        cloud.complete_authorization(generation, "mobile@example.com".to_string());
        let token = "ab".repeat(32);
        let attempt = 9;
        {
            let mut push = cloud.inner.notifications.lock().unwrap();
            push.requested_enabled = true;
            push.authorization = MobileNotificationAuthorization::Authorized;
            push.registration = MobileNotificationRegistration::RegisteringWithCloud;
            push.apns_token = Some(token.clone());
            push.environment = Some("sandbox".to_string());
            push.session_generation = Some(generation);
            push.registration_attempt = attempt;
        }
        {
            let session = cloud.inner.session.lock().unwrap();
            let push = cloud.inner.notifications.lock().unwrap();
            assert!(push_registration_response_is_current(
                &session, &push, generation, attempt, &token, "sandbox"
            ));
        }

        // This is the state transition performed by Disable while the POST is
        // still in flight. Its attempt bump is the linearization boundary.
        {
            let mut push = cloud.inner.notifications.lock().unwrap();
            push.requested_enabled = false;
            push.registration = MobileNotificationRegistration::Idle;
            push.registration_attempt = push.registration_attempt.wrapping_add(1);
        }
        assert!(!cloud
            .accept_push_registration(
                generation,
                attempt,
                &token,
                "sandbox",
                "pdev_0123456789abcdef0123456789abcdef",
            )
            .unwrap());
        assert!(cloud
            .inner
            .notifications
            .lock()
            .unwrap()
            .device_id
            .is_none());
    }

    #[test]
    fn notification_status_never_serializes_native_registration_material() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let apns_token = "ab".repeat(32);
        let device_id = "pdev_0123456789abcdef0123456789abcdef";
        {
            let mut push = cloud.inner.notifications.lock().unwrap();
            push.requested_enabled = true;
            push.authorization = MobileNotificationAuthorization::Authorized;
            push.registration = MobileNotificationRegistration::Registered;
            push.apns_token = Some(apns_token.clone());
            push.environment = Some("sandbox".to_string());
            push.device_id = Some(device_id.to_string());
        }
        let serialized = serde_json::to_string(&cloud.notification_status()).unwrap();
        assert!(!serialized.contains(&apns_token));
        assert!(!serialized.contains(device_id));
        assert!(!serialized.contains("device_token"));
        assert!(!serialized.contains("installation_id"));
    }

    #[test]
    fn connections_reject_a_response_from_the_signed_out_generation() {
        let cloud = MobileCloud::with_cloud_url(DEFAULT_CLOUD_URL).unwrap();
        let (generation, _) = cloud.begin_sign_in().unwrap();
        cloud.complete_authorization(generation, "old@example.com".to_string());
        cloud.clear_for_sign_out().unwrap();

        let error = cloud
            .accept_connections_response(generation, Vec::new())
            .unwrap_err();
        assert_eq!(
            error,
            "Your Hecate Cloud session changed while connections were loading."
        );
    }

    #[test]
    fn mobile_assets_and_configs_keep_the_shell_scoped_to_mobile() {
        let html = include_str!("../../mobile/index.html");
        let script = include_str!("../../mobile/app.js");
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/mobile.json")).unwrap();
        let ios: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.ios.conf.json")).unwrap();
        let android: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.android.conf.json")).unwrap();

        assert!(html.contains("aria-live=\"polite\""));
        assert!(html.contains("Session security"));
        assert!(html.contains("id=\"openApprovalButton\""));
        assert!(!html.contains("verificationCode"));
        assert!(!html.contains("copyCodeButton"));
        assert!(!html.contains("copyCodeFeedback"));
        assert!(!script.contains("clipboard"));
        assert!(html.contains("id=\"notificationSection\""));
        assert!(html.contains("id=\"enableNotificationsButton\""));
        let permissions = capability["permissions"].as_array().unwrap();
        assert!(permissions
            .iter()
            .any(|permission| permission == "deep-link:default"));
        assert!(!permissions.iter().any(|permission| permission
            .as_str()
            .is_some_and(|value| value.contains("clipboard"))));
        for command in [
            "mobile_status",
            "mobile_sign_in",
            "mobile_reopen_authorization",
            "mobile_connections",
            "mobile_notification_status",
            "mobile_enable_notifications",
            "mobile_open_notification_settings",
            "mobile_disable_notifications",
            "mobile_open_connection",
            "mobile_start_connection",
            "mobile_sign_out",
        ] {
            assert!(
                script.contains(command),
                "missing {command} from mobile shell"
            );
            let permission = format!("allow-{}", command.replace('_', "-"));
            assert!(
                permissions
                    .iter()
                    .any(|candidate| candidate == permission.as_str()),
                "missing {permission} from mobile capability"
            );
        }
        for config in [&ios, &android] {
            assert_eq!(config["identifier"], "sh.hecate.mobile");
            assert_eq!(config["build"]["frontendDist"], "../mobile");
            assert_eq!(config["bundle"]["externalBin"], serde_json::json!([]));
            assert_eq!(config["bundle"]["createUpdaterArtifacts"], false);
            assert_eq!(config["app"]["withGlobalTauri"], true);
            assert_eq!(config["app"]["windows"][0]["incognito"], true);
            assert_eq!(config["app"]["windows"][0]["userAgent"], "HecateMobile");
            assert_eq!(
                config["plugins"]["deep-link"]["mobile"],
                serde_json::json!([{
                    "scheme": ["hecate-mobile"],
                    "appLink": false
                }])
            );
        }
        let ios_build = ios["bundle"]["iOS"]["bundleVersion"]
            .as_str()
            .expect("iOS bundle version");
        let ios_components = ios_build.split('.').collect::<Vec<_>>();
        assert_eq!(ios_components.len(), 3);
        for (component, max_digits) in ios_components.iter().zip([4, 2, 2]) {
            assert!(!component.is_empty());
            assert!(component.len() <= max_digits);
            assert!(component.bytes().all(|byte| byte.is_ascii_digit()));
        }
        assert_ne!(ios_components[0].parse::<u16>().unwrap(), 0);
        let android_build = android["bundle"]["android"]["versionCode"]
            .as_u64()
            .expect("Android version code");
        assert!((1..=2_100_000_000).contains(&android_build));
    }

    #[test]
    fn mobile_instance_navigation_matches_only_the_exact_internal_url() {
        assert!(is_mobile_instances_url(
            &tauri::Url::parse(MOBILE_INSTANCES_URL).unwrap()
        ));
        for candidate in [
            "hecate-mobile://connections",
            "hecate-mobile://connections/?next=https://evil.example",
            "hecate-mobile://connections/#runtime",
            "hecate-mobile://connection/",
            "https://connections/",
            "hecate-mobile-lookalike://connections/",
        ] {
            assert!(
                !is_mobile_instances_url(&tauri::Url::parse(candidate).unwrap()),
                "unexpected internal navigation match for {candidate}"
            );
        }
    }

    #[test]
    fn mobile_shell_return_target_must_be_a_packaged_app_url() {
        for candidate in [
            "tauri://localhost/",
            "tauri://localhost/index.html",
            "http://tauri.localhost/",
            "https://tauri.localhost/index.html",
        ] {
            let url = tauri::Url::parse(candidate).unwrap();
            assert!(is_mobile_shell_url(&url));
            assert_eq!(
                MobileShellNavigation::capture(url.clone())
                    .unwrap()
                    .home_url,
                url
            );
        }
        for candidate in [
            "https://console.hecatehq.com/",
            "https://runtime.app.hecatehq.com/",
            "tauri://evil.example/",
            "http://localhost:1420/",
        ] {
            let url = tauri::Url::parse(candidate).unwrap();
            assert!(!is_mobile_shell_url(&url));
            assert!(MobileShellNavigation::capture(url).is_err());
        }
    }

    #[test]
    fn android_cookie_plugin_clears_and_flushes_the_platform_cookie_store() {
        let source = include_str!(
            "../gen/android/app/src/main/java/sh/hecate/mobile/CookieManagerPlugin.kt"
        );
        let manifest = include_str!("../gen/android/app/src/main/AndroidManifest.xml");
        let gradle = include_str!("../gen/android/app/build.gradle.kts");
        let proguard = include_str!("../gen/android/app/proguard-rules.pro");

        assert!(source.contains("CookieManager.getInstance()"));
        assert!(source.contains("removeAllCookies"));
        assert!(source.contains("cookieManager.flush()"));
        assert!(source.contains("invoke.resolve()"));
        assert!(!manifest.contains("LEANBACK_LAUNCHER"));
        assert!(!manifest.contains("android.software.leanback"));
        assert!(gradle.contains("applicationId = \"sh.hecate.mobile\""));
        assert!(gradle.contains("keyPassword = keystoreProperties.getProperty(\"keyPassword\")"));
        assert!(
            gradle.contains("storePassword = keystoreProperties.getProperty(\"storePassword\")")
        );
        assert!(proguard.contains("-keep class sh.hecate.mobile.CookieManagerPlugin { *; }"));
    }

    #[test]
    fn ios_push_bridge_keeps_registration_material_native_and_profile_bound() {
        let bridge = include_str!("../gen/apple/Sources/hecate-app/HecatePushBridge.mm");
        let main = include_str!("../gen/apple/Sources/hecate-app/main.mm");
        let project = include_str!("../gen/apple/hecate-app.xcodeproj/project.pbxproj");
        let project_source = include_str!("../gen/apple/project.yml");
        let entitlements = include_str!("../gen/apple/hecate-app_iOS/hecate-app_iOS.entitlements");
        let info = include_str!("../gen/apple/hecate-app_iOS/Info.plist");

        assert!(main.contains("HecatePushBootstrap"));
        assert!(bridge.contains("SecRandomCopyBytes"));
        assert!(bridge.contains("kSecClassGenericPassword"));
        assert!(bridge.contains("kSecAttrAccessibleWhenUnlockedThisDeviceOnly"));
        assert!(bridge.contains("hecate_mobile_push_install_bridge"));
        assert!(bridge.contains("HecatePushRegisteredDeviceIdentifier"));
        assert!(!bridge.contains("NSLog"));
        assert!(project.contains("UserNotifications.framework in Frameworks"));
        assert!(project.contains("com.apple.Push"));
        assert_eq!(
            project
                .matches("HECATE_APNS_ENVIRONMENT = development;")
                .count(),
            1
        );
        assert_eq!(
            project
                .matches("HECATE_APNS_ENVIRONMENT = production;")
                .count(),
            1
        );
        assert_eq!(project.matches("CODE_SIGN_STYLE = Automatic;").count(), 2);
        assert!(project_source.contains("HECATE_APNS_ENVIRONMENT: development"));
        assert!(project_source.contains("HECATE_APNS_ENVIRONMENT: production"));
        assert!(project_source.contains("CODE_SIGN_STYLE: Automatic"));
        assert!(entitlements.contains("$(HECATE_APNS_ENVIRONMENT)"));
        assert!(!entitlements.contains("<string>development</string>"));
        assert!(!entitlements.contains("<string>production</string>"));
        assert!(info.contains("HecateAPNSEnvironment"));
        assert!(info.contains("$(HECATE_APNS_ENVIRONMENT)"));
    }

    #[test]
    fn mobile_overrides_merge_into_valid_tauri_configs() {
        fn merge_patch(target: &mut serde_json::Value, patch: &serde_json::Value) {
            let Some(patch) = patch.as_object() else {
                *target = patch.clone();
                return;
            };
            if !target.is_object() {
                *target = serde_json::json!({});
            }
            let target = target.as_object_mut().unwrap();
            for (key, value) in patch {
                if value.is_null() {
                    target.remove(key);
                } else {
                    merge_patch(target.entry(key).or_insert(serde_json::Value::Null), value);
                }
            }
        }

        let base: serde_json::Value =
            serde_json::from_str(include_str!("../tauri.conf.json")).unwrap();
        for overlay in [
            include_str!("../tauri.ios.conf.json"),
            include_str!("../tauri.android.conf.json"),
        ] {
            let mut merged = base.clone();
            let overlay: serde_json::Value = serde_json::from_str(overlay).unwrap();
            merge_patch(&mut merged, &overlay);
            let config: tauri::Config = serde_json::from_value(merged).unwrap();
            assert_eq!(config.app.windows.len(), 1);
            assert!(config.app.with_global_tauri);
            assert_eq!(config.bundle.external_bin, Some(Vec::new()));
            assert_eq!(config.build.frontend_dist.unwrap().to_string(), "../mobile");
        }
    }
}
