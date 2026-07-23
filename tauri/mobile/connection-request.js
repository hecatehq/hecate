export function shouldApplyConnectionsResponse(requestEpoch, currentEpoch, status) {
  return requestEpoch === currentEpoch && status?.signed_in === true;
}
