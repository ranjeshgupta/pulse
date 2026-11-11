@_exported import PulseCore
import Foundation

#if canImport(UIKit) && !os(watchOS)
import UIKit

extension PulseClient {
    /// Starts the SDK on iOS and makes it `PulseClient.shared`: UserDefaults storage, URLSession,
    /// device details from `UIDevice` and the bundle, app lifecycle observation (flush and the session
    /// clock in the background, new session and in-app refresh on return), and in-app messages shown
    /// with UIKit. Call it once from `application(_:didFinishLaunchingWithOptions:)`.
    ///
    /// Calling it again replaces the previous instance.
    @MainActor
    @discardableResult
    public static func start(_ config: PulseConfig) -> PulseClient {
        PulseUIKit.stop()
        let device = DeviceBox(UIKitDevice.read())
        let presenter = UIKitInAppPresenter()
        let env = PulseEnvironment(
            platform: .ios,
            deviceInfo: { device.value },
            inAppPresenter: presenter
        )
        let client = PulseClient.configure(config, environment: env)
        PulseUIKit.observe(client, device: device, presenter: presenter)
        return client
    }
}

/// Holds the device details; read from any thread, refreshed on the main thread when the locale changes.
final class DeviceBox {
    private let lock = NSLock()
    private var info: DeviceInfo
    init(_ info: DeviceInfo) { self.info = info }
    var value: DeviceInfo {
        get { lock.lock(); defer { lock.unlock() }; return info }
        set { lock.lock(); info = newValue; lock.unlock() }
    }
}

enum UIKitDevice {
    @MainActor
    static func read() -> DeviceInfo {
        let d = UIDevice.current
        let bundle = Bundle.main
        return DeviceInfo(
            platform: "ios",
            deviceType: d.userInterfaceIdiom == .pad ? "tablet" : "mobile",
            os: "iOS",
            osVersion: majorMinor(d.systemVersion),
            locale: Locale.preferredLanguages.first ?? Locale.current.identifier.replacingOccurrences(of: "_", with: "-"),
            appVersion: bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
            deviceModel: hardwareModel() ?? (d.userInterfaceIdiom == .pad ? "iPad" : "iPhone"),
            deviceBrand: "Apple"
        )
    }

    /// "17.4.1" → "17.4", "17.0" → "17".
    static func majorMinor(_ v: String) -> String {
        let parts = v.split(separator: ".")
        guard let major = parts.first else { return v }
        if parts.count > 1, parts[1] != "0" { return "\(major).\(parts[1])" }
        return String(major)
    }

    /// The hardware id, e.g. `iPhone15,2` (the simulator reports the simulated model).
    static func hardwareModel() -> String? {
        if let sim = ProcessInfo.processInfo.environment["SIMULATOR_MODEL_IDENTIFIER"] { return sim }
        var info = utsname()
        uname(&info)
        let machine = withUnsafePointer(to: &info.machine) {
            $0.withMemoryRebound(to: CChar.self, capacity: MemoryLayout.size(ofValue: info.machine)) { String(cString: $0) }
        }
        return machine.isEmpty ? nil : machine
    }
}

/// App lifecycle observation for the client `start` created.
enum PulseUIKit {
    private static var observers: [NSObjectProtocol] = []
    private static var presenter: UIKitInAppPresenter?

    @MainActor
    static func observe(_ client: PulseClient, device: DeviceBox, presenter: UIKitInAppPresenter) {
        self.presenter = presenter
        presenter.active = UIApplication.shared.applicationState == .active
        let nc = NotificationCenter.default
        observers = [
            nc.addObserver(forName: UIApplication.didEnterBackgroundNotification, object: nil, queue: .main) { [weak client] _ in
                guard let client = client else { return }
                Task { @MainActor in
                    // Ask for a little time so the flush can finish.
                    var task = UIBackgroundTaskIdentifier.invalid
                    task = UIApplication.shared.beginBackgroundTask(withName: "pulse.flush") {
                        UIApplication.shared.endBackgroundTask(task)
                    }
                    let end = task
                    client.appDidEnterBackground()
                    client.flush {
                        Task { @MainActor in if end != .invalid { UIApplication.shared.endBackgroundTask(end) } }
                    }
                }
            },
            nc.addObserver(forName: UIApplication.willEnterForegroundNotification, object: nil, queue: .main) { [weak client] _ in
                client?.appWillEnterForeground()
            },
            nc.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak presenter] _ in
                presenter?.active = true
            },
            nc.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: .main) { [weak presenter] _ in
                presenter?.active = false
            },
            nc.addObserver(forName: NSLocale.currentLocaleDidChangeNotification, object: nil, queue: .main) { _ in
                Task { @MainActor in device.value = UIKitDevice.read() }
            },
        ]
    }

    static func stop() {
        observers.forEach { NotificationCenter.default.removeObserver($0) }
        observers = []
        presenter = nil
    }
}
#endif
