import Foundation

/// The Pulse SDK core: identity, event queue, sessions, push tokens and push tracking, opt-out, the app
/// inbox and in-app campaigns. Foundation only; the `Pulse` module adds `PulseClient.start(_:)` with
/// UIKit lifecycle, device details and in-app presentation.
///
/// People are anonymous (`anon-` + 24 hex characters) until `identify`. Events are queued, persisted,
/// and sent in batches every `flushInterval`, as soon as `maxBatch` events are waiting, and on `flush()`.
/// Public methods are thread-safe and never block on the network, except the `...AndWait` / `...Blocking` ones.
public final class PulseClient {
    /// The instance `start` / `configure` created last.
    public private(set) static var shared: PulseClient?

    public let config: PulseConfig
    let env: PulseEnvironment
    let keys: PulseStorageKeys
    private let lock = NSRecursiveLock()

    private var anonID = ""
    private var userID: String?
    private var userHash: String?
    private var push: (token: String, platform: PulsePlatform)?
    private var queue: [[String: JSON]] = []
    private var sid: String?
    private var lastActivity: Date?
    private var optedOut = false
    private var warnedFull = false
    private var backoff = Backoff()
    private var flushing = false
    private(set) var disposed = false
    private var timer: PulseCancellable?

    /// The current person's app inbox.
    public private(set) var inbox: PulseInbox!
    /// In-app campaigns.
    public private(set) var inApp: PulseInApp!
    private var pushTracker: PushTracker!

    /// Creates a client. Most apps call `PulseClient.start(_:)` (Pulse module) instead, which also sets `shared`.
    public init(config: PulseConfig, environment: PulseEnvironment) {
        self.config = config
        self.env = environment
        self.keys = PulseStorageKeys(siteKey: config.siteKey)
        inbox = PulseInbox(self)
        inApp = PulseInApp(self)
        pushTracker = PushTracker(self)
        load()
        inApp.load()
        pushTracker.load()
        scheduleTimer()
        log("ready as \(currentID) with \(queue.count) queued events")
        if config.inAppMessages && !optedOut { inApp.refresh() }
    }

    /// Creates a client with `environment` and makes it `shared` (disposing the previous one).
    @discardableResult
    public static func configure(_ config: PulseConfig, environment: PulseEnvironment) -> PulseClient {
        shared?.dispose()
        let c = PulseClient(config: config, environment: environment)
        shared = c
        return c
    }

    private func locked<T>(_ f: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try f()
    }

    /// The id events are recorded under: the identified user, else this install's anonymous id.
    public var currentID: String { locked { userID ?? anonID } }
    /// This install's current anonymous id.
    public var anonymousID: String { locked { anonID } }
    /// Whether a user is identified (since the last `reset`).
    public var isIdentified: Bool { locked { userID != nil } }
    /// Number of events waiting to be sent.
    public var pendingEvents: Int { locked { queue.count } }
    /// The id of the current session, or nil before the first event (and after `reset`).
    public var sessionID: String? { locked { sid } }
    /// Whether the person opted out of tracking with `optOut()`.
    public var isOptedOut: Bool { locked { optedOut } }
    /// The stored push token, if any.
    public var pushToken: String? { locked { push?.token } }
    /// The device properties sent with identify, screen_view and session_start.
    public var deviceProperties: [String: String] { PulseDevice.buildProps(env.deviceInfo(), config: config) }
    // The user hash, when a user is identified with one.
    var identifiedHash: String? { locked { userID == nil ? nil : userHash } }

    private func load() {
        let s = env.storage
        var aid = s.read(keys.anonID)
        if aid == nil || !aid!.hasPrefix("anon-") {
            aid = newAnonID()
            s.write(keys.anonID, aid!)
        }
        anonID = aid!
        userID = s.read(keys.userID)
        userHash = userID == nil ? nil : s.read(keys.userHash)
        optedOut = s.read(keys.optOut) == "1"
        if let raw = s.read(keys.queue) {
            if let list = JSON.decode(raw)?.array {
                queue = list.compactMap { $0.object }
            } else {
                log("stored queue is unreadable; starting empty", always: true)
            }
        }
        sid = s.read(keys.sessionID)
        if let ms = s.read(keys.sessionAt).flatMap(Double.init) { lastActivity = Date(timeIntervalSince1970: ms / 1000) }
        if let raw = s.read(keys.push), let p = JSON.decode(raw) {
            if let token = p["token"]?.text, let platform = p["platform"]?.string.flatMap(PulsePlatform.init(rawValue:)) {
                push = (token, platform)
            } else {
                s.delete(keys.push)
            }
        }
    }

    private func randomHex(_ bytes: Int) -> String {
        (0..<bytes).map { _ in String(format: "%02x", UInt8.random(in: 0...255)) }.joined()
    }

    private func newAnonID() -> String { "anon-" + randomHex(12) }

    // MARK: - Tracking

    /// Records event `name` with optional `properties` for the current person.
    ///
    /// Throws `PulseError.invalidArgument` when the name is invalid (it must match
    /// `^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$`) or reserved. Does nothing while opted out. The event may show
    /// an in-app campaign whose trigger is `name`.
    public func track(_ name: String, properties: [String: Any]? = nil) throws {
        try PulseValidation.checkEventName(name)
        if isOptedOut { return log("opted out: \(name) not recorded") }
        enqueue(name, props: properties.map(Self.toJSON))
        if config.inAppMessages {
            let screen = name == "screen_view" ? properties?["screen"] as? String : nil
            inApp.onEvent(name, screen: screen)
        }
    }

    /// Records a `screen_view` event with `props.screen` = `name`, the session id and device props.
    public func screen(_ name: String, properties: [String: Any]? = nil) throws {
        guard !name.isEmpty else { throw PulseError.invalidArgument("screen name is empty") }
        if isOptedOut { return log("opted out: screen \(name) not recorded") }
        let sid = session()
        var p: [String: JSON] = deviceProperties.mapValues { .string($0) }
        p["session_id"] = .string(sid)
        if let props = properties { for (k, v) in Self.toJSON(props) { p[k] = v } }
        p["screen"] = .string(name)
        add("screen_view", props: p)
        if config.inAppMessages { inApp.onEvent("screen_view", screen: name) }
    }

    /// Ties this install to your user id and updates their profile.
    ///
    /// When the person was anonymous, the event carries `props.anonymous_id` so the server merges the
    /// anonymous visitor into the user. With identity verification on, pass `userHash`:
    /// hex(HMAC-SHA256(identity secret, userID)) computed on your server. Throws
    /// `PulseError.invalidArgument` for an invalid user id or attribute name.
    public func identify(_ userID: String, attributes: [String: Any]? = nil, email: String? = nil, userHash: String? = nil) throws {
        try PulseValidation.checkUserID(userID)
        try PulseValidation.checkAttributes(attributes)
        let (previous, wasAnonymous, out): (String, Bool, Bool) = locked {
            let previous = self.userID ?? anonID
            let wasAnonymous = self.userID == nil
            self.userID = userID
            self.userHash = (userHash?.isEmpty ?? true) ? nil : userHash
            env.storage.write(keys.userID, userID)
            if let h = self.userHash { env.storage.write(keys.userHash, h) } else { env.storage.delete(keys.userHash) }
            return (previous, wasAnonymous, optedOut)
        }
        if previous != userID { inbox.forget() }
        if out { return log("opted out: identify \(userID) stored locally, not sent") }
        var props: [String: JSON] = deviceProperties.mapValues { .string($0) }
        if wasAnonymous { props["anonymous_id"] = .string(previous) }
        enqueue("identify", props: props, attrs: attributes.map(Self.toJSON), email: email, flushWhenFull: false)
        flush()
        if previous != userID { env.dispatcher.execute { [weak self] in _ = self?.registerStoredToken() } }
        if config.inAppMessages { inApp.refresh() }
    }

    /// Forgets the identified user (call it on sign-out): a new anonymous id is used from now on and the
    /// session ends. Events already queued keep the id they were recorded under.
    public func reset() {
        locked {
            userID = nil
            userHash = nil
            anonID = newAnonID()
            env.storage.delete(keys.userID)
            env.storage.delete(keys.userHash)
            env.storage.write(keys.anonID, anonID)
            sid = nil
            env.storage.delete(keys.sessionID)
        }
        inbox.forget()
        if isOptedOut { return }
        env.dispatcher.execute { [weak self] in _ = self?.registerStoredToken() }
        if config.inAppMessages { inApp.refresh() }
    }

    static func toJSON(_ d: [String: Any]) -> [String: JSON] { d.mapValues { JSON(any: $0) } }

    // MARK: - Consent

    /// Stops all tracking and sending on this device: queued events and push reports are deleted, and
    /// nothing is recorded or sent (events, push tokens, push reports, inbox and in-app requests) until
    /// `optIn()`. The choice is stored. Channel unsubscribes are separate and happen on the server.
    public func optOut() {
        locked {
            optedOut = true
            env.storage.write(keys.optOut, "1")
            queue.removeAll()
            persistLocked()
        }
        pushTracker.clear()
        inApp.cancelPending()
        log("opted out")
    }

    /// Resumes tracking after `optOut()`. Registers the stored push token again.
    public func optIn() {
        let was = locked { () -> Bool in
            let was = optedOut
            optedOut = false
            env.storage.delete(keys.optOut)
            return was
        }
        guard was else { return }
        log("opted in")
        env.dispatcher.execute { [weak self] in _ = self?.registerStoredToken() }
        if config.inAppMessages { inApp.refresh() }
    }

    // MARK: - Sessions and lifecycle

    @discardableResult
    private func session() -> String {
        let (id, started): (String, Bool) = locked {
            let now = env.clock()
            let s = PulseDevice.nextSession(now: now, last: lastActivity, sid: sid, newID: { randomHex(12) })
            lastActivity = now
            env.storage.write(keys.sessionAt, String(Int64(now.timeIntervalSince1970 * 1000)))
            if s.started {
                sid = s.id
                env.storage.write(keys.sessionID, s.id)
            }
            return (s.id, s.started)
        }
        if started {
            log("session \(id) started")
            var p: [String: JSON] = deviceProperties.mapValues { .string($0) }
            p["session_id"] = .string(id)
            add("session_start", props: p)
        }
        return id
    }

    /// Call when the app goes to the background (the Pulse module does). Flushes, and starts the
    /// 30-minute session clock.
    public func appDidEnterBackground() {
        locked {
            let now = env.clock()
            lastActivity = now
            env.storage.write(keys.sessionAt, String(Int64(now.timeIntervalSince1970 * 1000)))
        }
        if config.flushOnBackground { flush() }
    }

    /// Call when the app comes to the foreground (the Pulse module does). May start a new session and
    /// refresh in-app campaigns.
    public func appWillEnterForeground() {
        if isOptedOut { return }
        session()
        if config.inAppMessages { inApp.refreshIfStale() }
    }

    // MARK: - Queue

    func enqueue(_ name: String, props: [String: JSON]? = nil, attrs: [String: JSON]? = nil, email: String? = nil,
                 flushWhenFull: Bool = true) {
        session()
        add(name, props: props, attrs: attrs, email: email, flushWhenFull: flushWhenFull)
    }

    private func add(_ name: String, props: [String: JSON]?, attrs: [String: JSON]? = nil, email: String? = nil,
                     flushWhenFull: Bool = true) {
        let full: Bool? = locked {
            if optedOut { return nil }
            var p = props ?? [:]
            if userID != nil, let h = userHash { p["user_hash"] = .string(h) }
            var ev: [String: JSON] = [
                "id": .string(randomHex(16)),
                "external_id": .string(userID ?? anonID),
                "name": .string(name),
                "ts": .string(PulseTime.iso(env.clock())),
            ]
            if !p.isEmpty { ev["props"] = .object(p) }
            if let a = attrs, !a.isEmpty { ev["attrs"] = .object(a) }
            if let e = email, !e.isEmpty { ev["email"] = .string(e) }
            queue.append(ev)
            if queue.count > config.maxQueue {
                queue.removeFirst(queue.count - config.maxQueue)
                log("queue full (\(config.maxQueue)): dropping the oldest events", always: !warnedFull)
                warnedFull = true
            }
            persistLocked()
            return flushWhenFull && queue.count >= config.maxBatch && !backoff.isWaiting(env.clock())
        }
        guard let f = full else { return }
        log("queued \(name)")
        if f { flush() }
    }

    private func persistLocked() {
        env.storage.write(keys.queue, JSON.array(queue.map { .object($0) }).encodedString)
    }

    private func scheduleTimer() {
        if disposed { return }
        timer = env.dispatcher.schedule(after: config.flushInterval) { [weak self] in
            guard let self = self else { return }
            self.flushIfDue()
            self.scheduleTimer()
        }
    }

    private func flushIfDue() {
        let due = locked { !queue.isEmpty && !backoff.isWaiting(env.clock()) }
        if due { flushAndWait() }
        pushTracker.sendIfDue()
    }

    /// Sends every queued event (and pending push reports) now, on the SDK's queue. The Pulse module
    /// calls it when the app goes to the background.
    /// `completion` runs on the SDK's queue once the attempt is over (sent, dropped or kept for retry).
    public func flush(completion: (() -> Void)? = nil) {
        env.dispatcher.execute { [weak self] in
            self?.flushAndWait()
            completion?()
        }
    }

    /// Sends queued events now on the calling thread (not the main thread). Events the server could not
    /// take for now (429, 5xx, network errors) stay queued and are retried with backoff; events it
    /// rejected (400, 403) are dropped. Never throws.
    public func flushAndWait() {
        let start = locked { () -> Bool in
            if flushing { return false }
            flushing = true
            return true
        }
        guard start else { return }
        drain()
        locked { flushing = false }
        pushTracker.sendAndWait()
    }

    private enum Outcome { case sent, dropped, retry }

    private func drain() {
        while true {
            let batch: [[String: JSON]]? = locked {
                (queue.isEmpty || disposed || optedOut) ? nil : Array(queue.prefix(config.maxBatch))
            }
            guard let b = batch else { return }
            let outcome = send(b)
            let stop: Bool = locked {
                if outcome == .retry {
                    let delay = backoff.failed(now: env.clock())
                    log("will retry \(queue.count) events in \(Int(delay))s")
                    return true
                }
                backoff.succeeded()
                warnedFull = false
                let ids = Set(b.compactMap { $0["id"]?.string })
                queue.removeAll { ids.contains($0["id"]?.string ?? "") }
                persistLocked()
                return false
            }
            if stop { return }
        }
    }

    private func send(_ batch: [[String: JSON]]) -> Outcome {
        let res: PulseHTTPResponse
        do {
            res = try post("/v1/web/events", ["events": .array(batch.map { .object($0) })])
        } catch {
            log("sending \(batch.count) events failed: \(error)")
            return .retry
        }
        let code = res.status
        if (200..<300).contains(code) {
            log("sent \(batch.count) events")
            return .sent
        }
        let body = String(decoding: res.body, as: UTF8.self)
        if code == 400 || code == 403 {
            log("server rejected \(batch.count) events (\(code)): \(body); dropping them", always: true)
            return .dropped
        }
        // 429, 5xx, and anything unexpected (e.g. 401 while a site key is being rotated): keep.
        log("server answered \(code) for \(batch.count) events: \(body)", always: code == 401)
        return .retry
    }

    // MARK: - Push

    /// Registers a push token for the current person. Pulse sends mobile push through FCM, so on iOS
    /// this is the **FCM registration token** (`Messaging.messaging().fcmToken`, with APNs configured in
    /// Firebase), not the raw APNs device token. The token is stored and registered again after
    /// `identify`, `reset` and `optIn`. `completion` (main queue) gets whether the server accepted it.
    /// Not sent while opted out.
    public func registerPushToken(_ token: String, platform: PulsePlatform? = nil, completion: ((Bool) -> Void)? = nil) {
        guard !token.isEmpty else { log("registerPushToken: empty token ignored", always: true); return }
        let platform = platform ?? env.platform
        locked {
            push = (token, platform)
            env.storage.write(keys.push, JSON.object(["token": .string(token), "platform": .string(platform.rawValue)]).encodedString)
        }
        env.dispatcher.execute { [weak self] in
            guard let self = self else { return }
            let ok = self.registerStoredToken()
            if let c = completion { self.env.callback { c(ok) } }
        }
    }

    func registerStoredToken() -> Bool {
        let prepared: ([String: JSON], PulsePlatform)? = locked {
            guard !optedOut, !disposed, let p = push else { return nil }
            var body: [String: JSON] = [
                "external_id": .string(userID ?? anonID),
                "token": .string(p.token),
                "platform": .string(p.platform.rawValue),
            ]
            if userID != nil, let h = userHash { body["user_hash"] = .string(h) }
            return (body, p.platform)
        }
        guard let prepared = prepared else { return false }
        let (body, platform) = prepared
        do {
            let res = try post("/v1/web/devices", body)
            if (200..<300).contains(res.status) {
                log("registered \(platform.rawValue) push token")
                return true
            }
            log("push token rejected: \(res.status) \(String(decoding: res.body, as: UTF8.self))", always: true)
        } catch {
            log("push token registration failed: \(error)", always: true)
        }
        return false
    }

    /// Reports that a Pulse push was tapped. `userInfo` is the notification's
    /// `request.content.userInfo` (or an FCM data map); it must hold `pulse_track`. Returns false (and
    /// does nothing) when it doesn't, when the URL is not http(s), or while opted out. The report is
    /// queued, stored and retried.
    @discardableResult
    public func trackPushClicked(userInfo: [AnyHashable: Any]) -> Bool { pushTracker.track(userInfo, .clicked) }

    /// Reports that a Pulse push was dismissed (`UNNotificationDismissActionIdentifier`, which needs a
    /// category with `.customDismissAction`). See `trackPushClicked(userInfo:)`.
    @discardableResult
    public func trackPushDismissed(userInfo: [AnyHashable: Any]) -> Bool { pushTracker.track(userInfo, .dismissed) }

    /// Push reports waiting to be sent.
    public var pendingPushReports: Int { pushTracker.count }

    // MARK: - HTTP

    func post(_ path: String, _ body: [String: JSON]) throws -> PulseHTTPResponse {
        guard let url = URL(string: config.host + path) else { throw URLError(.badURL) }
        return try env.http.execute(PulseHTTPRequest(
            method: "POST", url: url,
            headers: ["Content-Type": "application/json", "X-Pulse-Site-Key": config.siteKey],
            body: JSON.object(body).encoded(), timeout: config.requestTimeout))
    }

    func get(_ path: String, query: [(String, String)]) throws -> PulseHTTPResponse {
        guard var c = URLComponents(string: config.host + path) else { throw URLError(.badURL) }
        c.queryItems = query.map { URLQueryItem(name: $0.0, value: $0.1) }
        // URLComponents leaves "+" alone; encode it so the server does not read a space.
        c.percentEncodedQuery = c.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        guard let url = c.url else { throw URLError(.badURL) }
        return try env.http.execute(PulseHTTPRequest(
            method: "GET", url: url, headers: ["X-Pulse-Site-Key": config.siteKey], body: nil, timeout: config.requestTimeout))
    }

    /// Stops the flush timer. Queued events stay in storage for the next start.
    public func dispose() {
        let first: Bool = locked {
            if disposed { return false }
            disposed = true
            return true
        }
        guard first else { return }
        timer?.cancel()
        inApp.cancelPending()
        if PulseClient.shared === self { PulseClient.shared = nil }
    }

    func log(_ msg: String, always: Bool = false) {
        if config.debug || always { env.logger("[pulse] \(msg)") }
    }
}

/// A small persisted queue of push click/dismiss report URLs, sent with backoff.
final class PushTracker {
    private let pulse: PulseClient // strong: the client lives as long as the app; a reference kept by the app never dangles
    private let lock = NSLock()
    private var urls: [String] = []
    private var backoff = Backoff()
    private var sending = false
    static let max = 100

    init(_ pulse: PulseClient) { self.pulse = pulse }

    var count: Int { lock.lock(); defer { lock.unlock() }; return urls.count }

    func load() {
        guard let raw = pulse.env.storage.read(pulse.keys.pushTracks) else { return }
        urls = JSON.decode(raw)?.array?.compactMap { $0.string } ?? []
    }

    private func persistLocked() {
        if urls.isEmpty {
            pulse.env.storage.delete(pulse.keys.pushTracks)
        } else {
            pulse.env.storage.write(pulse.keys.pushTracks, JSON.array(urls.map { .string($0) }).encodedString)
        }
    }

    func track(_ userInfo: [AnyHashable: Any], _ action: PulsePush.Action) -> Bool {
        guard let track = PulsePush.trackURL(from: userInfo), !pulse.isOptedOut,
              let url = PulsePush.reportURL(track, action: action, platform: pulse.env.platform) else { return false }
        lock.lock()
        if !urls.contains(url) {
            urls.append(url)
            if urls.count > PushTracker.max { urls.removeFirst(urls.count - PushTracker.max) }
            persistLocked()
        }
        lock.unlock()
        pulse.log("push \(action.rawValue) queued")
        pulse.env.dispatcher.execute { [weak pulse = self.pulse] in pulse?.flushPushReports() }
        return true
    }

    func clear() {
        lock.lock()
        urls.removeAll()
        persistLocked()
        lock.unlock()
    }

    func sendIfDue() {
        lock.lock()
        let due = !urls.isEmpty && !backoff.isWaiting(pulse.env.clock())
        lock.unlock()
        if due { sendAndWait() }
    }

    func sendAndWait() {
        lock.lock()
        if sending { lock.unlock(); return }
        sending = true
        lock.unlock()
        defer { lock.lock(); sending = false; lock.unlock() }
        while true {
            lock.lock()
            let next = urls.first
            lock.unlock()
            guard let url = next, !pulse.isOptedOut, let u = URL(string: url) else { return }
            var status = -1
            do {
                status = try pulse.env.http.execute(PulseHTTPRequest(
                    method: "POST", url: u, headers: [:], body: nil, timeout: pulse.config.requestTimeout)).status
            } catch {
                pulse.log("push report failed: \(error)")
            }
            lock.lock()
            if status == -1 || status == 429 || status >= 500 {
                backoff.failed(now: pulse.env.clock())
                lock.unlock()
                return
            }
            backoff.succeeded()
            if !(200..<300).contains(status) { pulse.log("push report answered \(status); dropping it", always: true) }
            urls.removeAll { $0 == url }
            persistLocked()
            lock.unlock()
        }
    }
}

extension PulseClient {
    func flushPushReports() { pushTracker.sendAndWait() }
}
