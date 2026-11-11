import Foundation
import PulseCore

#if canImport(UserNotifications)
import UserNotifications

extension PulseClient {
    /// Reports a tap (or dismissal) of a Pulse push from your
    /// `userNotificationCenter(_:didReceive:withCompletionHandler:)`. A dismissal is
    /// `UNNotificationDismissActionIdentifier`, which iOS only reports for categories created with
    /// `.customDismissAction`; every other action (the default tap or a custom button) counts as a
    /// click. Returns whether the notification was a Pulse push.
    @discardableResult
    public func handleNotificationResponse(_ response: UNNotificationResponse) -> Bool {
        let info = response.notification.request.content.userInfo
        if response.actionIdentifier == UNNotificationDismissActionIdentifier {
            return trackPushDismissed(userInfo: info)
        }
        return trackPushClicked(userInfo: info)
    }

    /// Whether a notification is a Pulse push (it carries an http(s) `pulse_track`).
    public static func isPulseNotification(_ userInfo: [AnyHashable: Any]) -> Bool {
        PulsePush.trackURL(from: userInfo) != nil
    }
}

/// A `UNUserNotificationCenterDelegate` for apps that don't have their own: reports taps and
/// dismissals of Pulse pushes to `PulseClient.shared` and shows notifications while the app is in the
/// foreground. Pass `next` to keep calling your own delegate (or Firebase's) as well.
///
/// ```swift
/// UNUserNotificationCenter.current().delegate = PulseNotificationDelegate.shared
/// ```
public final class PulseNotificationDelegate: NSObject, UNUserNotificationCenterDelegate {
    public static let shared = PulseNotificationDelegate()

    /// Another delegate to forward every call to (after Pulse has looked at it).
    public weak var next: UNUserNotificationCenterDelegate?

    /// How notifications are shown while the app is in the foreground, when `next` doesn't decide.
    public var foregroundPresentation: UNNotificationPresentationOptions

    public init(next: UNUserNotificationCenterDelegate? = nil) {
        self.next = next
        if #available(iOS 14.0, macOS 11.0, tvOS 14.0, *) {
            foregroundPresentation = [.banner, .list, .sound, .badge]
        } else {
            foregroundPresentation = [.alert, .sound, .badge]
        }
        super.init()
    }

    public func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                       withCompletionHandler completionHandler: @escaping () -> Void) {
        PulseClient.shared?.handleNotificationResponse(response)
        if let next = next, next.responds(to: #selector(UNUserNotificationCenterDelegate.userNotificationCenter(_:didReceive:withCompletionHandler:))) {
            next.userNotificationCenter?(center, didReceive: response, withCompletionHandler: completionHandler)
        } else {
            completionHandler()
        }
    }

    public func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                       withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        if let next = next, next.responds(to: #selector(UNUserNotificationCenterDelegate.userNotificationCenter(_:willPresent:withCompletionHandler:))) {
            next.userNotificationCenter?(center, willPresent: notification, withCompletionHandler: completionHandler)
        } else {
            completionHandler(foregroundPresentation)
        }
    }
}

/// Builds a notification category whose dismissals iOS reports (`.customDismissAction`), so
/// `trackPushDismissed` can be called. Register it with
/// `UNUserNotificationCenter.current().setNotificationCategories([...])` and send pushes with that
/// category (`aps.category` / FCM `apns.payload.aps.category`).
public enum PulseNotificationCategory {
    public static func trackingDismissals(identifier: String, actions: [UNNotificationAction] = []) -> UNNotificationCategory {
        UNNotificationCategory(identifier: identifier, actions: actions, intentIdentifiers: [], options: [.customDismissAction])
    }
}
#endif
