//! Shared, bounded retry guidance for native Cloud clients. This module only
//! schedules read-side cooldowns; it never retries a request or owns credentials.

use reqwest::header::{HeaderMap, RETRY_AFTER};
use std::time::{Duration, SystemTime};
use tokio::time::Instant;

const DEFAULT_RETRY_SECONDS: u64 = 30;
const MAX_RETRY_SECONDS: u64 = 24 * 60 * 60;

#[derive(Clone, Copy, Debug)]
pub(crate) struct CloudRetry {
    status: u16,
    delay: Duration,
}

impl CloudRetry {
    pub(crate) fn from_response(status: u16, headers: &HeaderMap) -> Option<Self> {
        Self::from_response_at(status, headers, SystemTime::now())
    }

    fn from_response_at(status: u16, headers: &HeaderMap, now: SystemTime) -> Option<Self> {
        if !matches!(status, 429 | 503) {
            return None;
        }
        // A duplicate or malformed header is not permission to hot-loop. The
        // fallback also supports older Cloud deployments without retry hints.
        let values = headers.get_all(RETRY_AFTER);
        let mut values = values.iter();
        let delay = values
            .next()
            .filter(|_| values.next().is_none())
            .and_then(|value| value.to_str().ok())
            .and_then(|value| parse_retry_after(value, now))
            .unwrap_or(Duration::from_secs(DEFAULT_RETRY_SECONDS));
        Some(Self { status, delay })
    }

    pub(crate) fn delay(self) -> Duration {
        self.delay
    }

    pub(crate) fn message(self) -> &'static str {
        if self.status == 429 {
            "Hecate Cloud is limiting requests. Wait before trying again."
        } else {
            "Hecate Cloud is temporarily unavailable. Wait before trying again."
        }
    }
}

fn parse_retry_after(value: &str, now: SystemTime) -> Option<Duration> {
    let value = value.trim();
    if value.is_empty() || value.len() > 128 {
        return None;
    }
    let seconds = if value.bytes().all(|byte| byte.is_ascii_digit()) {
        value.bytes().fold(0u64, |seconds, byte| {
            seconds
                .saturating_mul(10)
                .saturating_add(u64::from(byte - b'0'))
        })
    } else {
        let date = chrono::DateTime::parse_from_rfc2822(value).ok()?;
        let remaining = date.signed_duration_since(chrono::DateTime::<chrono::Utc>::from(now));
        let duration = remaining.to_std().unwrap_or_default();
        duration
            .as_secs()
            .saturating_add(u64::from(duration.subsec_nanos() != 0))
    };
    // Server hints cannot create a busy loop or indefinitely suppress checks.
    Some(Duration::from_secs(seconds.clamp(1, MAX_RETRY_SECONDS)))
}

#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct CloudRetryGate {
    window: Option<(u64, Instant, CloudRetry)>,
}

impl CloudRetryGate {
    // Desktop connector operations can change without changing the account.
    // Preserve the original deadline, but reject later replies from the old
    // operation generation. New accounts must never use this transition.
    #[cfg(any(desktop, test))]
    pub(crate) fn carry_to_generation(&mut self, previous: u64, next: u64) {
        if let Some((owner, until, retry)) = self.window {
            if owner == previous {
                self.window = Some((next, until, retry));
            }
        }
    }

    pub(crate) fn record(&mut self, generation: u64, retry: CloudRetry) {
        let until = Instant::now() + retry.delay();
        // Concurrent readiness failures may extend, but never shorten, a
        // current account's cooldown. Callers fence stale replies first.
        if self
            .window
            .is_some_and(|(owner, existing, _)| owner == generation && existing >= until)
        {
            return;
        }
        self.window = Some((generation, until, retry));
    }

    pub(crate) fn remaining(&self, generation: u64) -> Option<Duration> {
        let (owner, until, _) = self.window?;
        if owner != generation {
            return None;
        }
        let remaining = until.saturating_duration_since(Instant::now());
        (!remaining.is_zero()).then_some(remaining)
    }

    pub(crate) fn retry_after_seconds(&self, generation: u64) -> Option<u64> {
        let remaining = self.remaining(generation)?;
        Some(
            remaining
                .as_secs()
                .saturating_add(u64::from(remaining.subsec_nanos() != 0)),
        )
    }

    pub(crate) fn message(&self, generation: u64) -> Option<&'static str> {
        self.remaining(generation)?;
        self.window.map(|(_, _, retry)| retry.message())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use reqwest::header::HeaderValue;
    use std::time::UNIX_EPOCH;

    fn headers(value: &str) -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert(RETRY_AFTER, HeaderValue::from_str(value).unwrap());
        headers
    }

    #[test]
    fn retry_guidance_accepts_seconds_and_http_dates() {
        let now = UNIX_EPOCH + Duration::from_secs(60);
        for status in [429, 503] {
            for value in ["30", "Thu, 01 Jan 1970 00:01:30 GMT"] {
                let retry = CloudRetry::from_response_at(status, &headers(value), now).unwrap();
                assert_eq!(retry.delay(), Duration::from_secs(30), "{value}");
            }
        }
    }

    #[test]
    fn retry_guidance_is_bounded_and_malformed_values_use_a_safe_default() {
        let now = UNIX_EPOCH + Duration::from_secs(60);
        for (value, seconds) in [
            ("0", 1),
            ("1", 1),
            ("86401", MAX_RETRY_SECONDS),
            ("999999999999999999999999999999", MAX_RETRY_SECONDS),
            ("Thu, 01 Jan 1970 00:00:00 GMT", 1),
            ("-5", 30),
            ("1.5", 30),
            ("30, 60", 30),
            ("tomorrow", 30),
            ("", 30),
        ] {
            let retry = CloudRetry::from_response_at(503, &headers(value), now).unwrap();
            assert_eq!(retry.delay(), Duration::from_secs(seconds), "{value}");
        }
        let missing = CloudRetry::from_response_at(503, &HeaderMap::new(), now).unwrap();
        assert_eq!(missing.delay(), Duration::from_secs(30));
        let mut duplicate = headers("5");
        duplicate.append(RETRY_AFTER, HeaderValue::from_static("60"));
        assert_eq!(
            CloudRetry::from_response_at(503, &duplicate, now)
                .unwrap()
                .delay(),
            Duration::from_secs(30)
        );
        for status in [200, 400, 401, 403, 404, 500] {
            assert!(CloudRetry::from_response_at(status, &headers("30"), now).is_none());
        }
    }

    #[tokio::test(start_paused = true)]
    async fn cooldown_is_monotonic_generation_scoped_and_never_shortened() {
        let mut gate = CloudRetryGate::default();
        let retry = CloudRetry::from_response(503, &headers("30")).unwrap();
        gate.record(4, retry);
        assert_eq!(gate.retry_after_seconds(4), Some(30));
        assert_eq!(gate.remaining(5), None);
        tokio::time::advance(Duration::from_millis(500)).await;
        assert_eq!(gate.retry_after_seconds(4), Some(30));
        gate.record(4, CloudRetry::from_response(503, &headers("1")).unwrap());
        tokio::time::advance(Duration::from_millis(29_500)).await;
        assert_eq!(gate.remaining(4), None);
        assert_eq!(gate.message(4), None);
        gate.record(5, retry);
        assert_eq!(gate.retry_after_seconds(5), Some(30));
        assert_eq!(gate.remaining(4), None);
        tokio::time::advance(Duration::from_secs(10)).await;
        gate.carry_to_generation(5, 6);
        assert_eq!(gate.retry_after_seconds(6), Some(20));
        assert_eq!(gate.remaining(5), None);
    }
}
