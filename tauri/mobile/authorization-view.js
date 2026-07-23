export function authorizationView(status) {
  const authorizing = status?.authorizing === true;
  const approvalPageAvailable =
    authorizing && status?.approval_page_available === true;

  if (!authorizing) {
    return {
      showActions: false,
      openDisabled: true,
      openLabel: "Open sign-in again",
      openAriaLabel: "Open browser sign-in again",
      openDescribedBy: "statusDetail",
      statusDetail: "Your session is kept in native memory only.",
    };
  }

  if (!approvalPageAvailable) {
    return {
      showActions: false,
      openDisabled: true,
      openLabel: "Open sign-in again",
      openAriaLabel: "Open browser sign-in again",
      openDescribedBy: "statusDetail",
      statusDetail: "Preparing a secure browser confirmation…",
    };
  }

  return {
    showActions: true,
    openDisabled: false,
    openLabel: "Open sign-in again",
    openAriaLabel: "Open browser sign-in again",
    openDescribedBy: "statusDetail",
    statusDetail: "Approve in Safari. Hecate will return here automatically.",
  };
}
