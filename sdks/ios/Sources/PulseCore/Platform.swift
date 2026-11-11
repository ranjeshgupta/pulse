import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

/// Key-value storage for the SDK's state. Keys are already namespaced by site key (`pulse.<siteKey>.aid`).
public protocol PulseStorage: AnyObject {
    func read(_ key: String) -> String?
    func write(_ key: String, _ value: String)
    func delete(_ key: String)
}

/// Storage in `UserDefaults` (the default on devices).
public final class UserDefaultsStorage: PulseStorage {
    private let defaults: UserDefaults
    public init(_ defaults: UserDefaults = .standard) { self.defaults = defaults }
    public func read(_ key: String) -> String? { defaults.string(forKey: key) }
    public func write(_ key: String, _ value: String) { defaults.set(value, forKey: key) }
    public func delete(_ key: String) { defaults.removeObject(forKey: key) }
}

/// Storage in memory, for tests.
public final class InMemoryStorage: PulseStorage {
    private let lock = NSLock()
    private var store: [String: String] = [:]
    public init() {}
    public var values: [String: String] { lock.lock(); defer { lock.unlock() }; return store }
    public func read(_ key: String) -> String? { lock.lock(); defer { lock.unlock() }; return store[key] }
    public func write(_ key: String, _ value: String) { lock.lock(); store[key] = value; lock.unlock() }
    public func delete(_ key: String) { lock.lock(); store[key] = nil; lock.unlock() }
}

/// Storage key names, namespaced by the workspace's site key so two workspaces never share state.
public struct PulseStorageKeys {
    public let anonID, userID, userHash, queue, push, sessionID, sessionAt, optOut, inApp, pushTracks: String
    public init(siteKey: String) {
        let p = "pulse.\(siteKey)."
        anonID = p + "aid"
        userID = p + "uid"
        userHash = p + "uh"
        queue = p + "queue"
        push = p + "push"
        sessionID = p + "sid"
        sessionAt = p + "sla" // last activity, ms since the epoch
        optOut = p + "optout"
        inApp = p + "inapp"
        pushTracks = p + "ptrack"
    }
}

/// One HTTP request. `body` is JSON, or nil for none.
public struct PulseHTTPRequest {
    public var method: String
    public var url: URL
    public var headers: [String: String]
    public var body: Data?
    public var timeout: TimeInterval
}

/// One HTTP response.
public struct PulseHTTPResponse {
    public var status: Int
    public var body: Data
    public init(status: Int, body: Data = Data()) {
        self.status = status
        self.body = body
    }
    public init(status: Int, text: String) { self.init(status: status, body: Data(text.utf8)) }
}

/// Sends HTTP requests. It blocks; the SDK calls it on its own serial queue. Throws on network errors.
public protocol PulseHTTPClient: AnyObject {
    func execute(_ request: PulseHTTPRequest) throws -> PulseHTTPResponse
}

/// The default `PulseHTTPClient`, on `URLSession` (waits for each request on the SDK's queue).
public final class URLSessionHTTPClient: PulseHTTPClient {
    private let session: URLSession
    public init(session: URLSession = URLSession(configuration: .ephemeral)) { self.session = session }

    public func execute(_ request: PulseHTTPRequest) throws -> PulseHTTPResponse {
        var r = URLRequest(url: request.url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: request.timeout)
        r.httpMethod = request.method
        for (k, v) in request.headers { r.setValue(v, forHTTPHeaderField: k) }
        r.httpBody = request.body
        let done = DispatchSemaphore(value: 0)
        var result: Result<PulseHTTPResponse, Error> = .failure(URLError(.unknown))
        let task = session.dataTask(with: r) { data, response, error in
            if let error = error {
                result = .failure(error)
            } else if let http = response as? HTTPURLResponse {
                result = .success(PulseHTTPResponse(status: http.statusCode, body: data ?? Data()))
            } else {
                result = .failure(URLError(.badServerResponse))
            }
            done.signal()
        }
        task.resume()
        if done.wait(timeout: .now() + request.timeout + 5) == .timedOut {
            task.cancel()
            throw URLError(.timedOut)
        }
        return try result.get()
    }
}

/// A scheduled task that can be cancelled.
public protocol PulseCancellable {
    func cancel()
}

/// Runs the SDK's background work (HTTP, delays) one task at a time.
public protocol PulseDispatcher: AnyObject {
    func execute(_ work: @escaping () -> Void)
    func schedule(after seconds: TimeInterval, _ work: @escaping () -> Void) -> PulseCancellable
}

extension DispatchWorkItem: PulseCancellable {}

/// The default dispatcher: one serial queue.
public final class QueueDispatcher: PulseDispatcher {
    private let queue: DispatchQueue
    public init(label: String = "io.pulse.sdk") { queue = DispatchQueue(label: label, qos: .utility) }
    public func execute(_ work: @escaping () -> Void) { queue.async(execute: work) }
    public func schedule(after seconds: TimeInterval, _ work: @escaping () -> Void) -> PulseCancellable {
        let item = DispatchWorkItem(block: work)
        queue.asyncAfter(deadline: .now() + seconds, execute: item)
        return item
    }
}

/// Shows in-app messages. The Pulse module has one on UIKit.
public protocol PulseInAppPresenter: AnyObject {
    /// Whether a message can be shown now (the app is active and has a window).
    func canPresent() -> Bool
    /// Shows `message` and calls `completion` once when it is closed. Called on the SDK's queue.
    func present(_ message: PulseInAppMessage, completion: @escaping (PulseInAppOutcome) -> Void)
}

/// What the platform gives the core: storage, HTTP, a worker queue, a clock and device details.
/// `PulseClient.start` (Pulse module) builds one with UserDefaults, URLSession, UIDevice and UIKit.
/// Tests pass fakes.
public struct PulseEnvironment {
    public var storage: PulseStorage
    public var platform: PulsePlatform
    public var http: PulseHTTPClient
    public var dispatcher: PulseDispatcher
    public var clock: () -> Date
    public var deviceInfo: () -> DeviceInfo
    /// Where callbacks (inbox results, unread count, in-app actions) run. Default: the main queue.
    public var callback: (@escaping () -> Void) -> Void
    public var logger: (String) -> Void
    public var inAppPresenter: PulseInAppPresenter?

    public init(
        storage: PulseStorage = UserDefaultsStorage(),
        platform: PulsePlatform = .ios,
        http: PulseHTTPClient = URLSessionHTTPClient(),
        dispatcher: PulseDispatcher = QueueDispatcher(),
        clock: @escaping () -> Date = Date.init,
        deviceInfo: (() -> DeviceInfo)? = nil,
        callback: @escaping (@escaping () -> Void) -> Void = { work in DispatchQueue.main.async(execute: work) },
        logger: @escaping (String) -> Void = { print($0) },
        inAppPresenter: PulseInAppPresenter? = nil
    ) {
        self.storage = storage
        self.platform = platform
        self.http = http
        self.dispatcher = dispatcher
        self.clock = clock
        self.deviceInfo = deviceInfo ?? { DeviceInfo(platform: platform.rawValue) }
        self.callback = callback
        self.logger = logger
        self.inAppPresenter = inAppPresenter
    }
}

/// Exponential backoff: 2 s after the first failure, doubling up to 5 minutes, plus up to 1 s of jitter.
struct Backoff {
    private(set) var failures = 0
    private(set) var retryAt: Date?

    static func delaySeconds(_ failures: Int) -> TimeInterval {
        failures >= 9 ? 300 : min(TimeInterval(1 << failures), 300)
    }

    @discardableResult
    mutating func failed(now: Date) -> TimeInterval {
        failures += 1
        let delay = Backoff.delaySeconds(failures) + Double.random(in: 0..<1)
        retryAt = now.addingTimeInterval(delay)
        return delay
    }

    mutating func succeeded() {
        failures = 0
        retryAt = nil
    }

    func isWaiting(_ now: Date) -> Bool { retryAt.map { now < $0 } ?? false }
}
