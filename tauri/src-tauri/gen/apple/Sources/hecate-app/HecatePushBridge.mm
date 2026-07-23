#import "HecatePushBridge.h"

#import <Foundation/Foundation.h>
#import <Security/Security.h>
#import <UIKit/UIKit.h>
#import <UserNotifications/UserNotifications.h>
#import <objc/runtime.h>

extern "C" void hecate_mobile_push_authorization_changed(int32_t status, const char *error);
extern "C" void hecate_mobile_push_install_bridge(const HecatePushBridgeV1 *bridge);
extern "C" void hecate_mobile_push_registered(
    const uint8_t *bytes,
    uintptr_t length,
    int32_t environment
);
extern "C" void hecate_mobile_push_registration_failed(const char *error);

namespace {

NSString *const HecatePushRequestedKey = @"sh.hecate.mobile.push-requested";
NSString *const HecatePushRegisteredDeviceKey = @"sh.hecate.mobile.push-device-id";
NSString *const HecatePushInstallationService = @"sh.hecate.mobile.push-installation";
NSString *const HecatePushInstallationAccount = @"installation-id";
NSString *const HecatePushEnvironmentInfoKey = @"HecateAPNSEnvironment";

using DidRegisterImplementation = void (*)(id, SEL, UIApplication *, NSData *);
using DidFailImplementation = void (*)(id, SEL, UIApplication *, NSError *);

IMP previousDidRegisterImplementation = nullptr;
IMP previousDidFailImplementation = nullptr;
Class hookedAppDelegateClass = Nil;

bool IsValidInstallationIdentifier(NSString *candidate) {
    if (candidate.length != 47 || ![candidate hasPrefix:@"hpi_"]) {
        return false;
    }
    NSCharacterSet *allowed = [NSCharacterSet characterSetWithCharactersInString:
        @"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"];
    return [[candidate substringFromIndex:4] rangeOfCharacterFromSet:allowed.invertedSet].location
        == NSNotFound;
}

NSDictionary *InstallationKeychainQuery(void) {
    return @{
        (__bridge id)kSecClass: (__bridge id)kSecClassGenericPassword,
        (__bridge id)kSecAttrService: HecatePushInstallationService,
        (__bridge id)kSecAttrAccount: HecatePushInstallationAccount,
    };
}

NSString *ReadInstallationIdentifier(void) {
    NSMutableDictionary *query = [InstallationKeychainQuery() mutableCopy];
    query[(__bridge id)kSecReturnData] = @YES;
    query[(__bridge id)kSecMatchLimit] = (__bridge id)kSecMatchLimitOne;
    CFTypeRef raw = nullptr;
    OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &raw);
    if (status != errSecSuccess || raw == nullptr) {
        if (raw != nullptr) {
            CFRelease(raw);
        }
        return nil;
    }
    NSData *data = CFBridgingRelease(raw);
    NSString *candidate = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    return IsValidInstallationIdentifier(candidate) ? candidate : nil;
}

NSString *NewInstallationIdentifier(void) {
    uint8_t randomBytes[32] = {};
    if (SecRandomCopyBytes(kSecRandomDefault, sizeof(randomBytes), randomBytes) != errSecSuccess) {
        return nil;
    }
    NSData *data = [NSData dataWithBytes:randomBytes length:sizeof(randomBytes)];
    NSString *encoded = [data base64EncodedStringWithOptions:0];
    encoded = [encoded stringByReplacingOccurrencesOfString:@"+" withString:@"-"];
    encoded = [encoded stringByReplacingOccurrencesOfString:@"/" withString:@"_"];
    encoded = [encoded stringByReplacingOccurrencesOfString:@"=" withString:@""];
    NSString *candidate = [@"hpi_" stringByAppendingString:encoded];
    return IsValidInstallationIdentifier(candidate) ? candidate : nil;
}

bool StoreInstallationIdentifier(NSString *identifier) {
    NSData *data = [identifier dataUsingEncoding:NSUTF8StringEncoding];
    if (data == nil) {
        return false;
    }
    NSMutableDictionary *item = [InstallationKeychainQuery() mutableCopy];
    item[(__bridge id)kSecValueData] = data;
    item[(__bridge id)kSecAttrAccessible] = (__bridge id)kSecAttrAccessibleWhenUnlockedThisDeviceOnly;
    OSStatus status = SecItemAdd((__bridge CFDictionaryRef)item, nullptr);
    if (status == errSecDuplicateItem) {
        NSDictionary *updates = @{(__bridge id)kSecValueData: data};
        status = SecItemUpdate(
            (__bridge CFDictionaryRef)InstallationKeychainQuery(),
            (__bridge CFDictionaryRef)updates
        );
    }
    return status == errSecSuccess;
}

NSString *LoadOrCreateInstallationIdentifier(void) {
    if (NSString *existing = ReadInstallationIdentifier()) {
        return existing;
    }
    // Replace a malformed legacy value rather than ever sending it to Cloud.
    SecItemDelete((__bridge CFDictionaryRef)InstallationKeychainQuery());
    NSString *identifier = NewInstallationIdentifier();
    if (identifier == nil || !StoreInstallationIdentifier(identifier)) {
        return nil;
    }
    return identifier;
}

int32_t SignedPushEnvironment(void) {
    // This signed Info.plist value is sourced from the same required Xcode
    // build setting as aps-environment. No Debug/Release environment is
    // guessed in code.
    NSString *value = [[NSBundle mainBundle] objectForInfoDictionaryKey:HecatePushEnvironmentInfoKey];
    if ([value isEqualToString:@"development"]) {
        return 1;
    }
    if ([value isEqualToString:@"production"]) {
        return 2;
    }
    return 0;
}

void ReportRegistrationFailure(const char *message) {
    hecate_mobile_push_registration_failed(message);
}

void HecateDidRegister(
    id receiver,
    SEL selector,
    UIApplication *application,
    NSData *deviceToken
) {
    if (previousDidRegisterImplementation != nullptr) {
        reinterpret_cast<DidRegisterImplementation>(previousDidRegisterImplementation)(
            receiver,
            selector,
            application,
            deviceToken
        );
    }
    int32_t environment = SignedPushEnvironment();
    if (environment == 0) {
        ReportRegistrationFailure(
            "The signed app is missing a valid APNs environment. Configure Push Notifications in Xcode."
        );
        return;
    }
    hecate_mobile_push_registered(
        static_cast<const uint8_t *>(deviceToken.bytes),
        deviceToken.length,
        environment
    );
}

void HecateDidFail(
    id receiver,
    SEL selector,
    UIApplication *application,
    NSError *error
) {
    if (previousDidFailImplementation != nullptr) {
        reinterpret_cast<DidFailImplementation>(previousDidFailImplementation)(
            receiver,
            selector,
            application,
            error
        );
    }
    ReportRegistrationFailure("Apple Push Notification service registration failed.");
}

void InstallMethod(
    Class appDelegateClass,
    SEL selector,
    IMP implementation,
    IMP *previous,
    const char *fallbackTypes
) {
    Method existing = class_getInstanceMethod(appDelegateClass, selector);
    IMP inherited = existing == nullptr ? nullptr : method_getImplementation(existing);
    if (inherited == implementation) {
        return;
    }
    const char *types = existing == nullptr ? fallbackTypes : method_getTypeEncoding(existing);
    if (class_addMethod(appDelegateClass, selector, implementation, types)) {
        *previous = inherited;
    } else {
        *previous = class_replaceMethod(appDelegateClass, selector, implementation, types);
    }
}

void InstallAppDelegateHooks(void) {
    id<UIApplicationDelegate> delegate = UIApplication.sharedApplication.delegate;
    if (delegate == nil) {
        return;
    }
    Class appDelegateClass = object_getClass(delegate);
    if (hookedAppDelegateClass == appDelegateClass) {
        return;
    }
    hookedAppDelegateClass = appDelegateClass;
    InstallMethod(
        appDelegateClass,
        @selector(application:didRegisterForRemoteNotificationsWithDeviceToken:),
        reinterpret_cast<IMP>(HecateDidRegister),
        &previousDidRegisterImplementation,
        "v@:@@"
    );
    InstallMethod(
        appDelegateClass,
        @selector(application:didFailToRegisterForRemoteNotificationsWithError:),
        reinterpret_cast<IMP>(HecateDidFail),
        &previousDidFailImplementation,
        "v@:@@"
    );
}

}  // namespace

@interface HecatePushCoordinator : NSObject <UNUserNotificationCenterDelegate>
@property(nonatomic, strong, nullable) NSString *installationIdentifier;
@property(nonatomic, strong, nullable) id launchObserver;
+ (instancetype)shared;
- (void)bootstrap;
- (void)refreshAuthorization;
- (void)registerWithApple;
@end

@implementation HecatePushCoordinator

+ (instancetype)shared {
    static HecatePushCoordinator *coordinator;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        coordinator = [[HecatePushCoordinator alloc] init];
    });
    return coordinator;
}

- (void)bootstrap {
    self.installationIdentifier = LoadOrCreateInstallationIdentifier();
    UNUserNotificationCenter.currentNotificationCenter.delegate = self;
    __weak HecatePushCoordinator *weakSelf = self;
    self.launchObserver = [NSNotificationCenter.defaultCenter
        addObserverForName:UIApplicationDidFinishLaunchingNotification
                    object:nil
                     queue:NSOperationQueue.mainQueue
                usingBlock:^(__unused NSNotification *notification) {
                    HecatePushCoordinator *strongSelf = weakSelf;
                    if (strongSelf == nil) {
                        return;
                    }
                    UNUserNotificationCenter.currentNotificationCenter.delegate = strongSelf;
                    InstallAppDelegateHooks();
                }];
}

- (void)refreshAuthorization {
    [UNUserNotificationCenter.currentNotificationCenter
        getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
            hecate_mobile_push_authorization_changed(
                static_cast<int32_t>(settings.authorizationStatus),
                nullptr
            );
        }];
}

- (void)registerWithApple {
    dispatch_async(dispatch_get_main_queue(), ^{
        InstallAppDelegateHooks();
        if (UIApplication.sharedApplication.delegate == nil) {
            ReportRegistrationFailure("The iPhone application delegate is unavailable.");
            return;
        }
        if (self.installationIdentifier == nil) {
            ReportRegistrationFailure("This installation could not be identified securely.");
            return;
        }
        if (SignedPushEnvironment() == 0) {
            ReportRegistrationFailure(
                "The signed app is missing a valid APNs environment. Configure Push Notifications in Xcode."
            );
            return;
        }
        [UIApplication.sharedApplication registerForRemoteNotifications];
    });
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions options))completionHandler {
    // Hecate notifications remain visible while an operator is already in the
    // app. Their opaque notification id and payload never cross into Rust/JS.
    completionHandler(
        UNNotificationPresentationOptionBanner |
        UNNotificationPresentationOptionList |
        UNNotificationPresentationOptionSound |
        UNNotificationPresentationOptionBadge
    );
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completionHandler {
    // Tapping naturally foregrounds the current signed-in root. The webview's
    // visibility handler refreshes live state; no payload is used as auth or
    // forwarded to JavaScript.
    completionHandler();
}

@end

void HecatePushBootstrap(void) {
    static const HecatePushBridgeV1 bridge = {
        .abi_version = 1,
        .refresh_authorization = HecatePushRefreshAuthorization,
        .request_authorization = HecatePushRequestAuthorization,
        .register_for_remote_notifications = HecatePushRegister,
        .open_settings = HecatePushOpenSettings,
        .unregister_for_remote_notifications = HecatePushUnregister,
        .requested_enabled = HecatePushRequestedEnabled,
        .set_requested_enabled = HecatePushSetRequestedEnabled,
        .installation_identifier = HecatePushInstallationIdentifier,
        .registered_device_identifier = HecatePushRegisteredDeviceIdentifier,
        .set_registered_device_identifier = HecatePushSetRegisteredDeviceIdentifier,
    };
    hecate_mobile_push_install_bridge(&bridge);
    [[HecatePushCoordinator shared] bootstrap];
}

void HecatePushRefreshAuthorization(void) {
    [[HecatePushCoordinator shared] refreshAuthorization];
}

void HecatePushRequestAuthorization(void) {
    UNAuthorizationOptions options =
        UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge;
    [UNUserNotificationCenter.currentNotificationCenter
        requestAuthorizationWithOptions:options
                  completionHandler:^(BOOL granted, NSError *error) {
                      if (error != nil) {
                          hecate_mobile_push_authorization_changed(
                              -1,
                              "iPhone could not update notification permission."
                          );
                          return;
                      }
                      [[HecatePushCoordinator shared] refreshAuthorization];
                      if (granted) {
                          [[HecatePushCoordinator shared] registerWithApple];
                      }
                  }];
}

void HecatePushRegister(void) {
    [[HecatePushCoordinator shared] registerWithApple];
}

void HecatePushOpenSettings(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSURL *settingsURL = [NSURL URLWithString:UIApplicationOpenSettingsURLString];
        if (settingsURL != nil) {
            [UIApplication.sharedApplication openURL:settingsURL options:@{} completionHandler:nil];
        }
    });
}

void HecatePushUnregister(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [UIApplication.sharedApplication unregisterForRemoteNotifications];
    });
}

bool HecatePushRequestedEnabled(void) {
    return [NSUserDefaults.standardUserDefaults boolForKey:HecatePushRequestedKey];
}

void HecatePushSetRequestedEnabled(bool enabled) {
    [NSUserDefaults.standardUserDefaults setBool:enabled forKey:HecatePushRequestedKey];
}

const char *HecatePushInstallationIdentifier(void) {
    HecatePushCoordinator *coordinator = [HecatePushCoordinator shared];
    @synchronized(coordinator) {
        if (coordinator.installationIdentifier == nil) {
            coordinator.installationIdentifier = LoadOrCreateInstallationIdentifier();
        }
        return coordinator.installationIdentifier.UTF8String;
    }
}

const char *HecatePushRegisteredDeviceIdentifier(void) {
    NSString *deviceIdentifier =
        [NSUserDefaults.standardUserDefaults stringForKey:HecatePushRegisteredDeviceKey];
    return deviceIdentifier.UTF8String;
}

void HecatePushSetRegisteredDeviceIdentifier(const uint8_t *bytes, uintptr_t length) {
    if (bytes == nullptr || length == 0) {
        [NSUserDefaults.standardUserDefaults removeObjectForKey:HecatePushRegisteredDeviceKey];
        return;
    }
    NSData *data = [NSData dataWithBytes:bytes length:length];
    NSString *deviceIdentifier = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    if (deviceIdentifier.length == 37 && [deviceIdentifier hasPrefix:@"pdev_"]) {
        [NSUserDefaults.standardUserDefaults
            setObject:deviceIdentifier
               forKey:HecatePushRegisteredDeviceKey];
    }
}
