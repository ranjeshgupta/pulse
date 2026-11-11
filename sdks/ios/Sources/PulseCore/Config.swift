import Foundation

/// The app platform: sent with push tokens, push click tracking and in-app campaign requests.
public enum PulsePlatform: String, Codable {
    /// An Android device (FCM registration token).
    case android
    /// An iOS device. Pulse sends through FCM, so this is the FCM registration token of the iOS app.
    case ios
}

/// Errors the app has to fix: an invalid name or id, or a missing/wrong user hash for the inbox.
/// Network failures, rate limiting and server errors are not thrown.
public enum PulseError: Error, Equatable, CustomStringConvertible {
    /// An invalid argument (event name, user id, attribute name, config value).
    case invalidArgument(String)
    /// The inbox of a signed-in user needs their user hash; [statusCode] is 403 when the server refused it, nil when the SDK refused before sending.
    case userHashRequired(String, statusCode: Int?)

    public var description: String {
        switch self {
        case .invalidArgument(let m): return "PulseError: \(m)"
        case .userHashRequired(let m, let code): return "PulseError\(code.map { " (\($0))" } ?? ""): \(m)"
        }
    }
}

/// Settings for the SDK.
///
/// `host` is the Pulse server's base URL (for example `https://pulse.example.com`) and `siteKey` the
/// workspace's public site key (`sk_...`). The site key is public: never put an API key or the identity
/// verification secret in an app.
public struct PulseConfig {
    /// Server base URL without a trailing slash.
    public let host: String
    public let siteKey: String
    /// How often queued events are sent (default 10 s).
    public var flushInterval: TimeInterval
    /// Events per request (1-50), and the queue length that triggers an immediate flush.
    public var maxBatch: Int
    /// Most events kept while offline; the oldest are dropped beyond this.
    public var maxQueue: Int
    /// Flush when the app goes to the background.
    public var flushOnBackground: Bool
    /// Timeout of one HTTP request (default 30 s).
    public var requestTimeout: TimeInterval
    /// Log what the SDK does.
    public var debug: Bool
    /// Fetch in-app campaigns and show them when their trigger event happens.
    public var inAppMessages: Bool
    /// Called on the main queue with the button's `value` when someone taps an in-app button whose
    /// action is `url` (a web link or deep link). The app opens it; the SDK only closes the message.
    public var onInAppAction: ((String) -> Void)?
    /// The app's version, sent as `app_version` (the Pulse module defaults it to CFBundleShortVersionString).
    public var appVersion: String?
    /// The device model, sent as `device_model` (the Pulse module defaults it to the hardware id, e.g. `iPhone15,2`).
    public var deviceModel: String?
    /// The device maker, sent as `device_brand` (default `Apple` on iOS).
    public var deviceBrand: String?
    /// The OS version, sent as `os_version`, when you know it better than the SDK.
    public var osVersion: String?

    public init(
        host: String,
        siteKey: String,
        flushInterval: TimeInterval = 10,
        maxBatch: Int = 50,
        maxQueue: Int = 1000,
        flushOnBackground: Bool = true,
        requestTimeout: TimeInterval = 30,
        debug: Bool = false,
        inAppMessages: Bool = true,
        onInAppAction: ((String) -> Void)? = nil,
        appVersion: String? = nil,
        deviceModel: String? = nil,
        deviceBrand: String? = nil,
        osVersion: String? = nil
    ) throws {
        var h = host
        while h.hasSuffix("/") { h.removeLast() }
        guard !h.isEmpty else { throw PulseError.invalidArgument("host is empty") }
        guard !siteKey.isEmpty else { throw PulseError.invalidArgument("siteKey is empty") }
        guard (1...50).contains(maxBatch) else { throw PulseError.invalidArgument("maxBatch must be 1-50, got \(maxBatch)") }
        guard maxQueue >= maxBatch else { throw PulseError.invalidArgument("maxQueue must be at least maxBatch") }
        guard flushInterval > 0, requestTimeout > 0 else {
            throw PulseError.invalidArgument("flushInterval and requestTimeout must be positive")
        }
        self.host = h
        self.siteKey = siteKey
        self.flushInterval = flushInterval
        self.maxBatch = maxBatch
        self.maxQueue = maxQueue
        self.flushOnBackground = flushOnBackground
        self.requestTimeout = requestTimeout
        self.debug = debug
        self.inAppMessages = inAppMessages
        self.onInAppAction = onInAppAction
        self.appVersion = appVersion
        self.deviceModel = deviceModel
        self.deviceBrand = deviceBrand
        self.osVersion = osVersion
    }
}

/// What the platform knows about the device. Values in `PulseConfig` win over these.
public struct DeviceInfo: Equatable {
    /// `android` or `ios`.
    public var platform: String?
    /// `mobile` or `tablet`.
    public var deviceType: String?
    /// `Android` or `iOS`.
    public var os: String?
    public var osVersion: String?
    /// BCP 47 tag, e.g. `en-IN`.
    public var locale: String?
    public var appVersion: String?
    public var deviceModel: String?
    public var deviceBrand: String?

    public init(platform: String? = nil, deviceType: String? = nil, os: String? = nil, osVersion: String? = nil,
                locale: String? = nil, appVersion: String? = nil, deviceModel: String? = nil, deviceBrand: String? = nil) {
        self.platform = platform
        self.deviceType = deviceType
        self.os = os
        self.osVersion = osVersion
        self.locale = locale
        self.appVersion = appVersion
        self.deviceModel = deviceModel
        self.deviceBrand = deviceBrand
    }
}

/// Checks the server applies to what an app sends with a site key.
public enum PulseValidation {
    /// Event names the site-key API refuses: consent changes and events only a server may send.
    public static let reservedEventNames: Set<String> = [
        "unsubscribed", "resubscribed", "tag_added", "tag_removed", "points_changed",
        "list_joined", "list_left", "contact_merged", "contact_merged_into",
    ]

    private static func isLetter(_ c: Character) -> Bool { c.isASCII && c.isLetter }
    private static func isDigit(_ c: Character) -> Bool { c.isASCII && c.isNumber }

    /// `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`
    public static func isValidEventName(_ name: String) -> Bool {
        guard let first = name.first, isLetter(first), name.count <= 64 else { return false }
        return name.allSatisfy { isLetter($0) || isDigit($0) || "_.:-".contains($0) }
    }

    /// `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`
    public static func isValidAttributeName(_ name: String) -> Bool {
        guard let first = name.first, isLetter(first) || first == "_", name.count <= 64 else { return false }
        return name.allSatisfy { isLetter($0) || isDigit($0) || $0 == "_" }
    }

    public static func checkEventName(_ name: String) throws {
        guard isValidEventName(name) else {
            throw PulseError.invalidArgument("event name \"\(name)\" must start with a letter and have at most 64 letters, digits, _ . : or -")
        }
        guard !reservedEventNames.contains(name) else {
            throw PulseError.invalidArgument("event name \"\(name)\" is reserved and cannot be sent from an app")
        }
    }

    public static func checkUserID(_ userID: String) throws {
        guard !userID.isEmpty else { throw PulseError.invalidArgument("userID is empty") }
        guard userID.utf8.count <= 255 else { throw PulseError.invalidArgument("userID is longer than 255 bytes") }
        guard !userID.hasPrefix("anon-") else {
            throw PulseError.invalidArgument("userID must not start with \"anon-\" (reserved for anonymous ids)")
        }
    }

    public static func checkAttributes(_ attributes: [String: Any]?) throws {
        guard let attributes = attributes else { return }
        for k in attributes.keys where !isValidAttributeName(k) {
            throw PulseError.invalidArgument("attribute name \"\(k)\" must start with a letter or _ and have at most 64 letters, digits or _")
        }
    }
}

/// Device properties and sessions.
public enum PulseDevice {
    /// How long without events (or in the background) ends a session: 30 minutes.
    public static let sessionTimeout: TimeInterval = 30 * 60

    static func clip(_ s: String?) -> String? {
        guard let t = s?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty else { return nil }
        return t.count > 100 ? String(t.prefix(100)) : t
    }

    /// The device properties sent with identify, screen_view and session_start. Values in `config` win
    /// over `info`; unknown values are left out and each is cut to 100 characters.
    public static func buildProps(_ info: DeviceInfo, config: PulseConfig?) -> [String: String] {
        var out: [String: String] = [:]
        func put(_ k: String, _ v: String?) { if let c = clip(v) { out[k] = c } }
        put("platform", info.platform)
        put("device_type", info.deviceType)
        put("os", info.os)
        put("os_version", config?.osVersion ?? info.osVersion)
        put("device_brand", config?.deviceBrand ?? info.deviceBrand)
        put("device_model", config?.deviceModel ?? info.deviceModel)
        put("app_version", config?.appVersion ?? info.appVersion)
        put("locale", info.locale)
        return out
    }

    /// Decides which session an event at `now` belongs to: the current one (`sid`) while the `last`
    /// activity was less than `timeout` ago, else a new one from `newID`. A clock that went backwards
    /// keeps the session.
    public static func nextSession(now: Date, last: Date?, sid: String?, newID: () -> String,
                                   timeout: TimeInterval = sessionTimeout) -> (id: String, started: Bool) {
        if let sid = sid, !sid.isEmpty, let last = last, now.timeIntervalSince(last) < timeout {
            return (sid, false)
        }
        return (newID(), true)
    }
}
