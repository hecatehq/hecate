import { createAutoRefreshLoop, shouldAutoRefresh } from "./auto-refresh.js";
import { authorizationView } from "./authorization-view.js";
import { shouldApplyConnectionsResponse } from "./connection-request.js";
import { connectionView } from "./connection-view.js";
import { notificationView } from "./notification-state.js";

(() => {
  "use strict";

  const invoke = window.__TAURI__?.core?.invoke ?? window.__TAURI_INTERNALS__?.invoke;
  const elements = {
    authView: document.querySelector("#authView"),
    homeView: document.querySelector("#homeView"),
    settingsView: document.querySelector("#settingsView"),
    settingsButton: document.querySelector("#settingsButton"),
    settingsBackButton: document.querySelector("#settingsBackButton"),
    networkState: document.querySelector("#networkState"),
    networkLabel: document.querySelector("#networkLabel"),
    statusChip: document.querySelector("#statusChip"),
    statusChipLabel: document.querySelector("#statusChipLabel"),
    statusSymbol: document.querySelector("#statusSymbol"),
    statusMessage: document.querySelector("#statusMessage"),
    statusDetail: document.querySelector("#statusDetail"),
    authorizationActions: document.querySelector("#authorizationActions"),
    openApprovalButton: document.querySelector("#openApprovalButton"),
    errorNotice: document.querySelector("#errorNotice"),
    errorMessage: document.querySelector("#errorMessage"),
    signInButton: document.querySelector("#signInButton"),
    cancelSignInButton: document.querySelector("#cancelSignInButton"),
    signOutButton: document.querySelector("#signOutButton"),
    accountEmail: document.querySelector("#accountEmail"),
    notificationSection: document.querySelector("#notificationSection"),
    notificationChip: document.querySelector("#notificationChip"),
    notificationChipLabel: document.querySelector("#notificationChipLabel"),
    notificationMessage: document.querySelector("#notificationMessage"),
    notificationError: document.querySelector("#notificationError"),
    enableNotificationsButton: document.querySelector("#enableNotificationsButton"),
    notificationSettingsButton: document.querySelector("#notificationSettingsButton"),
    disableNotificationsButton: document.querySelector("#disableNotificationsButton"),
    connectionsSection: document.querySelector("#connectionsSection"),
    connectionList: document.querySelector("#connectionList"),
    connectionTemplate: document.querySelector("#connectionTemplate"),
    connectionLoading: document.querySelector("#connectionLoading"),
    emptyState: document.querySelector("#emptyState"),
    refreshButton: document.querySelector("#refreshButton"),
  };

  let currentStatus = null;
  let currentNotificationStatus = null;
  let statusTimer = null;
  let statusRequestActive = false;
  let notificationRequestActive = false;
  let connectionsRequestEpoch = 0;
  let activeConnectionsRequestEpoch = 0;
  let activeScreen = "home";

  const connectionRefreshLoop = createAutoRefreshLoop({
    refresh: refreshSignedInData,
    intervalMs: 10_000,
  });

  const phaseLabels = {
    signed_out: "Signed out",
    authorizing: "Authorizing",
    signed_in: "Connected",
    error: "Needs attention",
    loading: "Checking",
  };

  function command(name, args) {
    if (typeof invoke !== "function") {
      return Promise.reject(new Error("The native Hecate bridge is unavailable."));
    }
    return invoke(name, args);
  }

  function errorText(error) {
    if (typeof error === "string" && error.trim()) return error;
    if (error instanceof Error && error.message) return error.message;
    return "Something went wrong. Please try again.";
  }

  function showError(error) {
    elements.errorMessage.textContent = errorText(error);
    elements.errorNotice.hidden = false;
  }

  function clearError() {
    elements.errorMessage.textContent = "";
    elements.errorNotice.hidden = true;
  }

  function setButtonBusy(button, busy, busyLabel) {
    if (!button) return;
    const label = button.querySelector("[data-button-label]") ?? button;
    if (busy) {
      button.dataset.label = label.textContent.trim();
      label.textContent = busyLabel;
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
    } else {
      if (button.dataset.label) label.textContent = button.dataset.label;
      delete button.dataset.label;
      button.disabled = false;
      button.removeAttribute("aria-busy");
    }
  }

  function updateNetworkState() {
    const online = navigator.onLine;
    elements.networkState.classList.toggle("is-offline", !online);
    elements.networkState.hidden = online;
    elements.networkLabel.textContent = "Offline";
  }

  function applyScreen(signedIn, focusTarget) {
    if (!signedIn) activeScreen = "auth";
    const showingSettings = signedIn && activeScreen === "settings";
    elements.authView.hidden = signedIn;
    elements.homeView.hidden = !signedIn || showingSettings;
    elements.settingsView.hidden = !showingSettings;
    elements.settingsButton.hidden = !signedIn || showingSettings;
    if (focusTarget) window.requestAnimationFrame(() => focusTarget.focus());
  }

  function showSettings() {
    if (!currentStatus?.signed_in) return;
    activeScreen = "settings";
    applyScreen(true, elements.settingsBackButton);
  }

  function showHome() {
    if (!currentStatus?.signed_in) return;
    activeScreen = "home";
    applyScreen(true, elements.settingsButton);
  }

  function applyStatus(status) {
    const wasSignedIn = currentStatus?.signed_in === true;
    currentStatus = status;
    const phase = status?.phase ?? "error";
    const signedIn = status?.signed_in === true;
    const authorizing = status?.authorizing === true;
    const authorization = authorizationView(status);
    if (signedIn && !wasSignedIn) activeScreen = "home";

    elements.statusChip.dataset.phase = phase;
    elements.statusChipLabel.textContent = phaseLabels[phase] ?? "Unavailable";
    elements.statusMessage.textContent = status?.message || "Hecate Cloud is unavailable.";
    elements.authorizationActions.hidden = !authorization.showActions;
    elements.openApprovalButton.disabled = authorization.openDisabled;
    elements.openApprovalButton.querySelector("[data-button-label]").textContent =
      authorization.openLabel;
    elements.openApprovalButton.setAttribute("aria-label", authorization.openAriaLabel);
    elements.openApprovalButton.setAttribute("aria-describedby", authorization.openDescribedBy);
    elements.statusDetail.textContent = authorization.statusDetail;
    elements.statusSymbol.textContent = signedIn ? "✓" : authorizing ? "···" : "H";

    elements.signInButton.hidden = signedIn || authorizing;
    elements.cancelSignInButton.hidden = !authorizing;
    elements.signOutButton.hidden = !signedIn;
    elements.accountEmail.textContent = status?.account_email || "Signed in";
    elements.connectionsSection.hidden = !signedIn;
    applyScreen(signedIn);
    applyNotificationStatus(currentNotificationStatus);
    if (status?.last_error) showError(status.last_error);
    else clearError();

    scheduleStatusPoll(authorizing);
    connectionRefreshLoop.setEnabled(shouldAutoRefresh(status, document.hidden));
    if (signedIn && !wasSignedIn && !document.hidden) {
      void loadConnections();
      void refreshNotificationStatus();
    }
    if (!signedIn) {
      invalidateConnectionsRequest();
      clearConnections();
    }
  }

  function applyNotificationStatus(status) {
    currentNotificationStatus = status;
    const view = notificationView(status, currentStatus?.signed_in === true);
    elements.notificationSection.hidden = view.hidden;
    elements.notificationSection.setAttribute("aria-busy", String(view.busy));
    elements.notificationChip.dataset.state = view.state;
    elements.notificationChipLabel.textContent = view.stateLabel;
    elements.notificationMessage.textContent = view.message;
    elements.notificationError.textContent = view.error;
    elements.notificationError.hidden = !view.error;

    const enableLabel = elements.enableNotificationsButton.querySelector("span");
    if (enableLabel) enableLabel.textContent = view.enableLabel;
    elements.enableNotificationsButton.hidden = !view.showEnable;
    elements.notificationSettingsButton.hidden = !view.showSettings;
    elements.disableNotificationsButton.hidden = !view.showDisable;
    for (const button of [
      elements.enableNotificationsButton,
      elements.notificationSettingsButton,
      elements.disableNotificationsButton,
    ]) {
      button.disabled = view.busy;
    }
  }

  async function refreshNotificationStatus() {
    if (notificationRequestActive) return;
    notificationRequestActive = true;
    try {
      applyNotificationStatus(await command("mobile_notification_status"));
    } catch (error) {
      if (currentNotificationStatus?.available) {
        currentNotificationStatus = {
          ...currentNotificationStatus,
          last_error: errorText(error),
        };
        applyNotificationStatus(currentNotificationStatus);
      }
    } finally {
      notificationRequestActive = false;
    }
  }

  async function runNotificationAction(button, busyLabel, commandName) {
    let nextStatus = null;
    setButtonBusy(button, true, busyLabel);
    elements.notificationSection.setAttribute("aria-busy", "true");
    elements.notificationError.hidden = true;
    elements.notificationError.textContent = "";
    try {
      nextStatus = await command(commandName);
    } catch (error) {
      currentNotificationStatus = {
        ...(currentNotificationStatus ?? { available: true }),
        last_error: errorText(error),
      };
    } finally {
      setButtonBusy(button, false);
      applyNotificationStatus(nextStatus ?? currentNotificationStatus);
    }
  }

  function enableNotifications() {
    return runNotificationAction(
      elements.enableNotificationsButton,
      "Connecting…",
      "mobile_enable_notifications",
    );
  }

  function openNotificationSettings() {
    return runNotificationAction(
      elements.notificationSettingsButton,
      "Opening Settings…",
      "mobile_open_notification_settings",
    );
  }

  function disableNotifications() {
    return runNotificationAction(
      elements.disableNotificationsButton,
      "Turning off…",
      "mobile_disable_notifications",
    );
  }

  function scheduleStatusPoll(needed) {
    if (statusTimer) window.clearTimeout(statusTimer);
    statusTimer = null;
    if (!needed || document.hidden) return;
    statusTimer = window.setTimeout(async () => {
      await refreshStatus();
    }, 2_000);
  }

  async function refreshStatus() {
    if (statusRequestActive) return;
    statusRequestActive = true;
    try {
      const status = await command("mobile_status");
      applyStatus(status);
    } catch (error) {
      showError(error);
      scheduleStatusPoll(currentStatus?.authorizing === true);
    } finally {
      statusRequestActive = false;
    }
  }

  async function signIn() {
    clearError();
    setButtonBusy(elements.signInButton, true, "Opening sign-in…");
    try {
      const status = await command("mobile_sign_in");
      applyStatus(status);
    } catch (error) {
      showError(error);
      await refreshStatus();
    } finally {
      setButtonBusy(elements.signInButton, false);
    }
  }

  async function signOut(button) {
    clearError();
    connectionRefreshLoop.setEnabled(false);
    invalidateConnectionsRequest();
    clearConnections();
    setButtonBusy(button, true, "Signing out…");
    try {
      const status = await command("mobile_sign_out");
      applyStatus(status);
      await refreshNotificationStatus();
    } catch (error) {
      showError(error);
    } finally {
      setButtonBusy(button, false);
      connectionRefreshLoop.setEnabled(shouldAutoRefresh(currentStatus, document.hidden));
    }
  }

  async function continueAuthorization() {
    const approvalPageAvailable =
      currentStatus?.authorizing === true &&
      currentStatus?.approval_page_available === true;
    if (!approvalPageAvailable || elements.openApprovalButton.disabled) return;

    clearError();
    setButtonBusy(elements.openApprovalButton, true, "Opening Safari…");
    try {
      const status = await command("mobile_reopen_authorization");
      applyStatus(status);
    } catch (error) {
      showError(error);
      await refreshStatus();
    } finally {
      setButtonBusy(elements.openApprovalButton, false);
    }
  }

  function clearConnections() {
    elements.connectionList.replaceChildren();
    elements.connectionLoading.hidden = true;
    elements.emptyState.hidden = true;
  }

  function invalidateConnectionsRequest() {
    connectionsRequestEpoch += 1;
    activeConnectionsRequestEpoch = 0;
    elements.connectionList.setAttribute("aria-busy", "false");
    elements.connectionLoading.hidden = true;
    elements.refreshButton.disabled = false;
    elements.refreshButton.classList.remove("is-spinning");
  }

  function renderConnections(connections) {
    clearConnections();
    elements.emptyState.hidden = connections.length !== 0;
    const fragment = document.createDocumentFragment();

    for (const connection of connections) {
      const node = elements.connectionTemplate.content.firstElementChild.cloneNode(true);
      const view = connectionView(connection);
      const openButton = node.querySelector(".connection-action");
      const health = node.querySelector(".connection-health");
      const healthLabel = node.querySelector(".connection-health-label");
      const name = node.querySelector(".connection-name");
      const meta = node.querySelector(".connection-meta");

      openButton.dataset.kind = connection.kind || "unknown";
      openButton.disabled = !view.canOpen;
      openButton.setAttribute("aria-label", view.ariaLabel);
      name.textContent = view.name;
      meta.textContent = view.detail;
      health.dataset.state = view.statusState;
      healthLabel.textContent = view.statusLabel;
      if (view.canOpen) {
        openButton.addEventListener("click", () => openConnection(connection, openButton));
      }
      fragment.append(node);
    }
    elements.connectionList.append(fragment);
  }

  async function loadConnections() {
    if (activeConnectionsRequestEpoch !== 0 || !currentStatus?.signed_in) return;
    const requestEpoch = ++connectionsRequestEpoch;
    activeConnectionsRequestEpoch = requestEpoch;
    clearError();
    elements.emptyState.hidden = true;
    elements.connectionLoading.hidden = false;
    elements.connectionList.setAttribute("aria-busy", "true");
    elements.refreshButton.disabled = true;
    elements.refreshButton.classList.add("is-spinning");
    try {
      const connections = await command("mobile_connections");
      if (!shouldApplyConnectionsResponse(requestEpoch, connectionsRequestEpoch, currentStatus)) {
        return;
      }
      renderConnections(Array.isArray(connections) ? connections : []);
    } catch (error) {
      if (!shouldApplyConnectionsResponse(requestEpoch, connectionsRequestEpoch, currentStatus)) {
        return;
      }
      showError(error);
      await refreshStatus();
    } finally {
      if (activeConnectionsRequestEpoch === requestEpoch) {
        activeConnectionsRequestEpoch = 0;
        elements.connectionLoading.hidden = true;
        elements.connectionList.setAttribute("aria-busy", "false");
        elements.refreshButton.disabled = false;
        elements.refreshButton.classList.remove("is-spinning");
      }
    }
  }

  async function refreshSignedInData() {
    await refreshStatus();
    await refreshNotificationStatus();
    if (shouldAutoRefresh(currentStatus, document.hidden)) {
      await loadConnections();
    }
  }

  async function openConnection(connection, button) {
    clearError();
    setButtonBusy(button, true, "Opening…");
    try {
      const result = await command("mobile_open_connection", { connectionId: connection.id });
      elements.statusMessage.textContent = result?.message || "Secure Hecate session opened.";
    } catch (error) {
      showError(error);
      await refreshStatus();
    } finally {
      setButtonBusy(button, false);
    }
  }

  elements.signInButton.addEventListener("click", signIn);
  elements.cancelSignInButton.addEventListener("click", (event) => signOut(event.currentTarget));
  elements.signOutButton.addEventListener("click", (event) => signOut(event.currentTarget));
  elements.settingsButton.addEventListener("click", showSettings);
  elements.settingsBackButton.addEventListener("click", showHome);
  elements.openApprovalButton.addEventListener("click", continueAuthorization);
  elements.enableNotificationsButton.addEventListener("click", enableNotifications);
  elements.notificationSettingsButton.addEventListener("click", openNotificationSettings);
  elements.disableNotificationsButton.addEventListener("click", disableNotifications);
  elements.refreshButton.addEventListener("click", loadConnections);
  window.addEventListener("online", () => {
    updateNetworkState();
    if (shouldAutoRefresh(currentStatus, document.hidden)) {
      connectionRefreshLoop.setEnabled(true, { immediate: true });
    } else {
      void refreshStatus();
      void refreshNotificationStatus();
    }
  });
  window.addEventListener("offline", updateNetworkState);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      if (shouldAutoRefresh(currentStatus, document.hidden)) {
        connectionRefreshLoop.setEnabled(true, { immediate: true });
      } else {
        void refreshStatus();
        void refreshNotificationStatus();
      }
    } else {
      scheduleStatusPoll(false);
      connectionRefreshLoop.setEnabled(false);
    }
  });

  updateNetworkState();
  void refreshStatus();
  void refreshNotificationStatus();
})();
