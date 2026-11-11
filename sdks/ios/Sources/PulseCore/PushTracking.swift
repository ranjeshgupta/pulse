import Foundation

/// Push open (click) and dismissal tracking.
///
/// Every Pulse push carries `pulse_track` in its data: a URL `{pulse}/t/p/{sealed token}`. The SDK
/// POSTs (no body, no site key) to it with `?a=clicked&platform=ios` when the notification is tapped,
/// or `a=dismissed` when it is dismissed. The server answers 204.
public enum PulsePush {
    /// The data key of the tracking URL in a Pulse push.
    public static let key = "pulse_track"

    public enum Action: String {
        case clicked, dismissed
    }

    /// The tracking URL of a push, from its APNs `userInfo` (FCM puts data keys at the top level; a
    /// nested `data` dictionary is checked too) or an FCM data map. Nil when the push is not from Pulse
    /// or the URL is not http(s).
    public static func trackURL(from userInfo: [AnyHashable: Any]?) -> String? {
        guard let info = userInfo else { return nil }
        if let s = info[key] as? String { return isHTTPURL(s) ? s : nil }
        if let data = info["data"] as? [AnyHashable: Any], let s = data[key] as? String { return isHTTPURL(s) ? s : nil }
        if let data = info["data"] as? [String: Any], let s = data[key] as? String { return isHTTPURL(s) ? s : nil }
        return nil
    }

    /// Whether `url` is an absolute http or https URL with a host.
    public static func isHTTPURL(_ url: String) -> Bool {
        guard let c = URLComponents(string: url.trimmingCharacters(in: .whitespaces)),
              let scheme = c.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = c.host, !host.isEmpty else { return false }
        return true
    }

    /// The URL to POST: `trackURL` with `a=<action>&platform=<platform>` added to its query (a fragment
    /// is dropped). Nil when `trackURL` is not an http(s) URL.
    public static func reportURL(_ trackURL: String, action: Action, platform: PulsePlatform) -> String? {
        guard isHTTPURL(trackURL) else { return nil }
        var base = trackURL.trimmingCharacters(in: .whitespaces)
        if let hash = base.firstIndex(of: "#") { base = String(base[..<hash]) }
        let sep: String
        if !base.contains("?") { sep = "?" } else if base.hasSuffix("?") || base.hasSuffix("&") { sep = "" } else { sep = "&" }
        return "\(base)\(sep)a=\(action.rawValue)&platform=\(platform.rawValue)"
    }
}
