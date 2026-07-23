# Mobile app

Hecate has source-buildable iOS and Android projects under
[`tauri/`](../../tauri/). The mobile app is a Cloud client, not a phone port of
the local runtime: agents, workspaces, provider credentials, and subprocesses
continue to run on a hosted Hecate runtime or a computer with Hecate desktop
open. The phone selects one of those Hecate instances and opens its chats,
projects, tasks, approvals, and live activity.

The product model is deliberately chat-first: the phone chooses **where a chat
runs**, then uses that runtime's normal Hecate UI. Selecting a desktop instance
creates and controls sessions on that Mac. Selecting a hosted instance creates
and controls remote sessions. The phone itself does not run agents.

## Current slice

The native shell can:

- open Hecate Cloud sign-in in the system browser for explicit approval;
- keep the resulting `happ_` app session in native process memory, never in the
  shell's JavaScript or a URL;
- list active hosted runtimes and the signed-in owner's remote-enabled desktop
  hosts, refreshing reachability every ten seconds while the app is visible;
- start a stopped Cloud-managed hosted runtime, then refresh its authenticated
  health until it becomes available;
- present those instances as the signed-in home, with account, notification,
  and security details kept in a separate Settings view;
- request iPhone notification permission only after an explicit in-app action,
  register that installation with Hecate Cloud, and alert for approval requests
  and finished or failed runs;
- ask Cloud for a short-lived, single-use browser bootstrap; and
- navigate the app WebView through that bootstrap to the selected Hecate UI,
  where a persistent **Instances** action returns to the chooser. Runtime roots
  open on Chats; the phone bar keeps Chats, Projects, and Tasks primary while
  Connections, Observability, Usage, Settings, and theme live under **More**.

The iOS and Android shells never bundle or start the Go sidecar. Desktop-only
plugins, menus, updater behavior, workspace launchers, and process controls are
compiled out of mobile builds. Once connected, Hecate's existing remote route
policy keeps local-only operations unavailable.

## Starting chats

A Hecate project is optional. Direct Hecate chats can start without a folder
when Tools are off. Coding agents such as Codex, Claude Code, Cursor Agent, and
Grok Build need a real folder on the selected runtime because the agent may
read or edit files there; Hecate chats with Tools enabled need one for the same
reason.

On mobile, the new-chat action opens a runtime-folder picker when a folder is
required. The picker offers project roots and recent in-place session folders,
and also accepts an absolute path that already exists on the selected Mac or
hosted runtime. It does not open the iPhone document picker because the agent
runs on the selected Hecate instance, not on the phone.

## Sign-in and connection security

The native client sends a random app token to Cloud once over HTTPS, and Cloud
stores only its hash. Cloud returns a ten-minute authorization ID and a
same-origin `/desktop-login` approval URL. A non-secret `request` query repeats
the authorization ID so every attempt loads a fresh browser document instead
of reusing an earlier Safari tab. Its fragment contains that authorization ID,
a bound one-time browser ticket, and the `mobile` client marker. The native
layer validates the exact origin, path, query, fragment shape, identifier,
ticket, and client marker before opening the system browser. The browser ticket
never enters the query or referrer, and there is no verification-code or
copy-code flow.

While authorization is pending, the validated approval URL remains in native
memory so **Open sign-in again** can retry the browser handoff without exposing
the URL or ticket to the local HTML/JavaScript shell. Cloud atomically consumes
the matching transaction and one-time browser ticket before enabling the app
session. The native client then polls Cloud with the bearer token, but the
browser and local HTML/JavaScript shell never receive that token.

When a hosted runtime is opened, the native layer requests a 60-second,
single-use continuation bound to the app session, actor, organization, target
path, and a durable nonce. The app navigates its own WebView to that
same-origin continuation. Cloud sets a separate HTTP-only browser cookie and
re-authorizes the runtime before redirecting. The app bearer is never put in
the WebView, runtime request, log, or response body.

The mobile WebView uses private browsing where the platform supports it, and
the native shell clears WebView browsing data on app start and sign-out. On
Android, that includes an explicit, completion-checked clear and flush of the
platform WebView cookie store. This keeps the derived runtime cookie aligned
with the memory-only app session on both platforms.

Desktop-host discovery returns no host credential or pre-minted relay ticket.
Cloud creates the relay session only after the operator chooses that host.

The selected runtime exposes a normal link to the internal
`hecate-mobile://connections/` address. The native shell intercepts only that
exact address and navigates back to the packaged startup page it captured at
launch. It does not accept a caller-provided return URL, add remote Tauri
permissions, reuse an expiring bootstrap URL, or expose the app bearer. The
Cloud app session remains in native memory, so the reloaded chooser can fetch
the current instance list without another approval.

## iPhone run notifications

Run notifications are opt-in and iOS-only in this slice. The native bridge asks
for alert, sound, and badge permission, obtains the APNs device token, and hands
it directly to Rust. Rust posts the lowercase token, the signed build's APNs
environment, and a stable `hpi_` installation identifier to
`POST /api/v1/app/push-devices`. The APNs token, installation identifier, Cloud
push-device id, and app bearer never enter the WebView, a URL, or a log.

The installation identifier is 32 random bytes stored in the iOS Keychain with
`WhenUnlockedThisDeviceOnly` accessibility. The non-secret Cloud push-device id
and the operator's on/off preference are stored in native user defaults so
notifications can remain active when the app process exits. The APNs token and
account details are never persisted by the app. After a later sign-in the app
registers again, allowing Cloud to update token rotation in place.

Turning notifications off unregisters locally immediately. If the app is
signed in, it also deletes the Cloud push-device record. If Cloud is temporarily
unreachable, the native device id remains only as a pending cleanup marker and
the next authenticated reconciliation retries the delete. Explicit sign-out
attempts that delete before revoking the app session. Merely terminating the app
does not sign out or disable background alerts.

APNs payloads contain only an opaque notification id and a generic event kind.
The app never treats either as authorization or forwards either to JavaScript.
A tap foregrounds the current signed-in root, whose normal visibility refresh
loads live state; a signed-out app remains on sign-in. Resource-specific deep
links are intentionally not part of this version.

## Honest alpha limits

- App sign-in is memory-only. Restarting a terminated app process requires
  approval again; persistent sessions need a reviewed Keychain/Android
  Keystore implementation.
- Android FCM notifications and resource-specific notification deep links are
  not implemented yet.
- There is no general offline mutation queue. Chat submissions already have a
  client request ID, but other writes and attachment uploads do not yet share a
  safe idempotency contract.
- A desktop-host connection works only while Hecate desktop remains open and
  Remote access is enabled. The phone cannot wake a powered-off Mac or launch
  its desktop app.
- Only organization owners and admins can start hosted runtimes managed by
  Hecate Cloud from the app. Manually registered runtimes must be started by
  their own operator.
- Store signing is wired but still requires maintainer-owned Apple and Google
  credentials. Store metadata, privacy disclosures, release CI, and real-device
  smoke tests remain release gates.

## Build prerequisites

Install the normal Tauri/Rust dependencies, Node.js 20 or newer for the
checked-in Xcode and Gradle build hooks, plus the mobile toolchains:

- iOS: full Xcode, an installed iOS Simulator runtime, CocoaPods, XcodeGen,
  `libimobiledevice`, and Rust targets `aarch64-apple-ios` and
  `aarch64-apple-ios-sim`;
- Android: Android SDK Platform 36, Build Tools 35/36, NDK
  `29.0.14206865`, the four Android Rust targets, and Java 21. Java 26 is too
  new for the generated Gradle 8 build.

The generated Xcode and Gradle projects are checked in under
`tauri/src-tauri/gen/apple` and `tauri/src-tauri/gen/android`. Do not rerun
`tauri ios init` or `tauri android init` casually: regeneration overwrites
project-level fixes, including the explicit repository-local Tauri CLI paths
used by Xcode and Gradle.

Build the unsigned iOS Simulator app:

```bash
just tauri-ios-build-debug
```

Build an Android arm64 debug APK:

```bash
export JAVA_HOME=/path/to/jdk-21/Contents/Home
export ANDROID_HOME=/path/to/android-sdk
export NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"
just tauri-android-build-debug aarch64
```

Outputs:

- iOS Simulator:
  `tauri/src-tauri/gen/apple/build/arm64-sim/Hecate.app`
- Android:
  `tauri/src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk`

These commands build only the mobile Cloud companion. They do not build or
stage the desktop Go sidecar.

## Store identity and signed builds

The mobile companion uses the permanent identifier `sh.hecate.mobile` on both
iOS and Android. Desktop keeps `sh.hecate.app`; do not reuse the desktop ID when
registering either mobile store record.

For iOS, register `sh.hecate.mobile` in the Apple Developer portal, configure
the matching App Store Connect app and provisioning profile, sign in to the
correct account in Xcode, and enable Push Notifications for that identifier.
The checked-in project declares the Push capability and binds the debug
configuration to `development` and release to `production`. Code signing still
fails closed if the selected profile does not carry the matching entitlement.
The direct commands below repeat the value as an explicit build-setting
override so the intended signing environment is visible at the call site.

For a development build installed on a connected iPhone:

```bash
export APPLE_DEVELOPMENT_TEAM=<Apple team ID>
export IPHONE_UDID=<device identifier>
cd tauri/src-tauri/gen/apple
xcodebuild \
  -project hecate-app.xcodeproj \
  -scheme hecate-app_iOS \
  -configuration debug \
  -destination "id=$IPHONE_UDID" \
  DEVELOPMENT_TEAM="$APPLE_DEVELOPMENT_TEAM" \
  HECATE_APNS_ENVIRONMENT=development \
  -allowProvisioningUpdates \
  build
```

For an App Store or TestFlight archive, pass `production` explicitly:

```bash
export APPLE_DEVELOPMENT_TEAM=<Apple team ID>
cd tauri/src-tauri/gen/apple
xcodebuild \
  -project hecate-app.xcodeproj \
  -scheme hecate-app_iOS \
  -configuration release \
  -destination 'generic/platform=iOS' \
  -archivePath build/hecate-app_iOS.xcarchive \
  DEVELOPMENT_TEAM="$APPLE_DEVELOPMENT_TEAM" \
  HECATE_APNS_ENVIRONMENT=production \
  -allowProvisioningUpdates \
  archive
```

An arbitrary exported shell or scheme environment variable is not an Xcode
build setting and must not be used to select APNs. The checked-in configuration
values, or an explicit `xcodebuild` assignment, drive both Info.plist and the
signed entitlements. Automatic signing can update the profile only when the
Apple account has authority for the Push-enabled identifier; otherwise
provisioning remains an explicit release blocker.

The existing release export places the IPA at
`tauri/src-tauri/gen/apple/build/arm64/Hecate.ipa`. The
checked-in privacy manifest records the app-container file timestamp access
used by the native shell. The generated Info.plist also declares microphone and
speech-recognition purpose strings and that the app uses no non-exempt
encryption. These bundle declarations do not replace the privacy policy or App
Store privacy answers for account information and user content handled by
Hecate Cloud.

For Android, create a Play upload key, copy
`tauri/src-tauri/gen/android/keystore.properties.example` to the ignored
`keystore.properties`, and replace every placeholder. Keep the keystore itself
outside the repository and back it up separately. Then build the signed AAB:

```bash
export JAVA_HOME=/path/to/jdk-21/Contents/Home
export ANDROID_HOME=/path/to/android-sdk
export NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"
just tauri-android-build-release
```

The AAB lands at
`tauri/src-tauri/gen/android/app/build/outputs/bundle/universalRelease/app-universal-release.aab`.
The first Play upload is intentionally manual so Google can bind the package
name and upload certificate to the new app record.

## Cloud configuration

Cloud must configure a dedicated app-browser ticket signing secret before
runtime opening is enabled:

```text
HCLOUD_APP_BROWSER_TICKET_SECRET=<at least 32 random characters>
HCLOUD_APP_BROWSER_TICKET_TTL_SECONDS=60
```

Cloud must also configure APNs provider credentials and a 32-byte device-token
encryption key before registration succeeds. Cloud returns a safe 503 response
when APNs is intentionally not configured; see the Cloud deployment runbook for
the current `HCLOUD_APNS_*` variables.

Use a key distinct from runtime and desktop-relay secrets. Keep the console URL,
cookie domain, runtime host suffix, and secure cookies configured for the
console and wildcard runtime hosts. The mobile shell defaults to
`https://console.hecatehq.com`; loopback HTTP is accepted only for development.
