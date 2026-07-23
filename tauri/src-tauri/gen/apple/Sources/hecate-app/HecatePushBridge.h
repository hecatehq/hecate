#pragma once

#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct HecatePushBridgeV1 {
	uint32_t abi_version;
	void (*refresh_authorization)(void);
	void (*request_authorization)(void);
	void (*register_for_remote_notifications)(void);
	void (*open_settings)(void);
	void (*unregister_for_remote_notifications)(void);
	bool (*requested_enabled)(void);
	void (*set_requested_enabled)(bool enabled);
	const char *(*installation_identifier)(void);
	const char *(*registered_device_identifier)(void);
	void (*set_registered_device_identifier)(const uint8_t *bytes, uintptr_t length);
} HecatePushBridgeV1;

void HecatePushBootstrap(void);
void HecatePushRefreshAuthorization(void);
void HecatePushRequestAuthorization(void);
void HecatePushRegister(void);
void HecatePushOpenSettings(void);
void HecatePushUnregister(void);
bool HecatePushRequestedEnabled(void);
void HecatePushSetRequestedEnabled(bool enabled);
const char *HecatePushInstallationIdentifier(void);
const char *HecatePushRegisteredDeviceIdentifier(void);
void HecatePushSetRegisteredDeviceIdentifier(const uint8_t *bytes, uintptr_t length);

#ifdef __cplusplus
}
#endif
