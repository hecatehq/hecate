#[cfg(target_os = "linux")]
use super::GatewayBaseURL;

pub(crate) fn install(
    window: &tauri::WebviewWindow,
    app_handle: tauri::AppHandle,
) -> tauri::Result<()> {
    #[cfg(target_os = "linux")]
    {
        use tauri::Manager;
        use webkit2gtk::glib::prelude::Cast;
        use webkit2gtk::{
            PermissionRequestExt, UserMediaPermissionRequest, UserMediaPermissionRequestExt,
            WebViewExt,
        };

        window.with_webview(move |platform_webview| {
            platform_webview
                .inner()
                .connect_permission_request(move |webview, request| {
                    let Some(media_request) = request.downcast_ref::<UserMediaPermissionRequest>()
                    else {
                        return false;
                    };

                    let expected_base_url = app_handle
                        .try_state::<GatewayBaseURL>()
                        .and_then(|state| state.snapshot());
                    let trusted_document = matches_gateway_origin(
                        webview.uri().as_deref(),
                        expected_base_url.as_deref(),
                    );
                    let audio_only =
                        media_request.is_for_audio_device() && !media_request.is_for_video_device();

                    if trusted_document && audio_only {
                        request.allow();
                        log::info!(
                            "granted webview microphone permission to the active gateway origin"
                        );
                    } else {
                        request.deny();
                        log::warn!(
                            "denied webview media permission trusted_gateway={} audio_only={}",
                            trusted_document,
                            audio_only
                        );
                    }
                    true
                });
        })?;
    }

    #[cfg(not(target_os = "linux"))]
    {
        let _ = (window, app_handle);
    }

    Ok(())
}

#[cfg(any(test, target_os = "linux"))]
fn matches_gateway_origin(document_url: Option<&str>, expected_base_url: Option<&str>) -> bool {
    let Some(document_origin) = document_url.and_then(loopback_origin) else {
        return false;
    };
    let Some(expected_origin) = expected_base_url.and_then(loopback_origin) else {
        return false;
    };
    document_origin == expected_origin
}

#[cfg(any(test, target_os = "linux"))]
fn loopback_origin(value: &str) -> Option<(String, String, u16)> {
    let url = tauri::Url::parse(value).ok()?;
    if url.scheme() != "http"
        || url.host_str() != Some("127.0.0.1")
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return None;
    }
    Some((
        url.scheme().to_string(),
        url.host_str()?.to_string(),
        url.port()?,
    ))
}

#[cfg(test)]
mod tests {
    use super::matches_gateway_origin;

    #[test]
    fn gateway_origin_accepts_paths_on_the_exact_sidecar_origin() {
        assert!(matches_gateway_origin(
            Some("http://127.0.0.1:43123/chats/session"),
            Some("http://127.0.0.1:43123"),
        ));
    }

    #[test]
    fn gateway_origin_rejects_other_ports_hosts_and_credentials() {
        for document_url in [
            "http://127.0.0.1:43124/",
            "http://localhost:43123/",
            "https://127.0.0.1:43123/",
            "http://operator@127.0.0.1:43123/",
        ] {
            assert!(!matches_gateway_origin(
                Some(document_url),
                Some("http://127.0.0.1:43123"),
            ));
        }
    }

    #[test]
    fn gateway_origin_rejects_missing_or_invalid_state() {
        assert!(!matches_gateway_origin(
            Some("http://127.0.0.1:43123/"),
            None,
        ));
        assert!(!matches_gateway_origin(
            None,
            Some("http://127.0.0.1:43123"),
        ));
        assert!(!matches_gateway_origin(
            Some("not a url"),
            Some("http://127.0.0.1:43123"),
        ));
    }
}
