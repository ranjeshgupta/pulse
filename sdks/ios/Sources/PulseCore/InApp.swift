import Foundation

/// A button of an in-app message.
public struct PulseInAppButton: Equatable {
    public let text: String
    /// `url` (open `value` through `PulseConfig.onInAppAction`) or `dismiss`.
    public let action: String
    public let value: String?
    /// Whether tapping the button opens a link.
    public var isURL: Bool { action == "url" && value != nil }

    public init(text: String, action: String, value: String? = nil) {
        self.text = text
        self.action = action
        self.value = value
    }

    public init(json j: JSON) {
        self.init(text: j["text"]?.text ?? "", action: j["action"]?.text ?? "dismiss", value: j["value"]?.text)
    }
}

/// What an in-app message shows. Colours are `#rrggbb`.
public struct PulseInAppContent: Equatable {
    public let title: String
    public let body: String
    public let imageURL: String?
    public let buttons: [PulseInAppButton]
    public let background: String?
    public let textColor: String?
    public let buttonColor: String?
    /// Banner position: `top` or `bottom` (default).
    public let position: String?

    public init(json j: JSON) {
        title = j["title"]?.text ?? ""
        body = j["body"]?.text ?? ""
        imageURL = j["image_url"]?.text
        buttons = (j["buttons"]?.array ?? []).map(PulseInAppButton.init(json:))
        background = j["background"]?.text
        textColor = j["text_color"]?.text
        buttonColor = j["button_color"]?.text
        position = j["position"]?.text
    }
}

/// When an in-app message is shown.
public struct PulseInAppTrigger: Equatable {
    /// The event that shows the message (e.g. `app_open`, `screen_view`, `add_to_cart`).
    public let event: String
    /// With `screen_view`: only this screen.
    public let screen: String?
    public let delaySeconds: Int
    /// `once` (ever, on this device), `session` (once per app run), `daily` or `always`.
    public let frequency: String
    public let platforms: [String]

    public init(json j: JSON) {
        event = j["trigger_event"]?.text ?? ""
        screen = j["screen"]?.text
        delaySeconds = Int(j["delay_seconds"]?.int ?? 0)
        frequency = j["frequency"]?.text ?? "once"
        platforms = (j["platforms"]?.array ?? []).compactMap { $0.string }
    }

    /// Whether event `name` (with `screen` for `screen_view`) triggers this.
    public func matches(_ name: String, screen: String?) -> Bool {
        event == name && (self.screen == nil || self.screen == screen)
    }
}

/// An in-app campaign the server returned for the current person.
public struct PulseInAppMessage: Equatable {
    /// Campaign id (`inapp_id` in the recorded events).
    public let id: Int64
    /// `modal`, `banner` or `fullscreen`.
    public let layout: String
    public let content: PulseInAppContent
    public let trigger: PulseInAppTrigger
    public let endAt: Date?

    public init?(json j: JSON) {
        guard let id = j["id"]?.int else { return nil }
        self.id = id
        layout = j["layout"]?.text ?? "modal"
        content = PulseInAppContent(json: j["content"] ?? .object([:]))
        trigger = PulseInAppTrigger(json: j["trigger"] ?? .object([:]))
        endAt = PulseTime.parse(j["end_at"]?.text)
    }
}

/// How a shown message was closed: by a `button` (nil for the close button or a tap outside), or
/// `expired` (a banner timed out).
public struct PulseInAppOutcome {
    public var button: PulseInAppButton?
    public var expired: Bool
    public init(button: PulseInAppButton? = nil, expired: Bool = false) {
        self.button = button
        self.expired = expired
    }
}

/// In-app campaigns for the current person.
///
/// The SDK fetches the campaigns at start, after identify, reset and opt-in, and when the app comes to
/// the foreground (at most every 15 minutes). When the app calls track(name) or screen(name), the first
/// campaign whose trigger matches and whose frequency allows is shown after its delay. Showing, tapping
/// a url button and dismissing are recorded as `inapp_shown`, `inapp_clicked` and `inapp_dismissed`.
public final class PulseInApp {
    /// How often campaigns are fetched again when the app comes back to the foreground: 15 minutes.
    public static let refreshAfter: TimeInterval = 15 * 60

    private let pulse: PulseClient // strong: the client lives as long as the app; a reference kept by the app never dangles
    private let lock = NSRecursiveLock()
    private var campaigns: [PulseInAppMessage] = []
    private var fetchedAt: Date?
    private var fetching = false
    private var generation = 0
    private var waiting: [(String, String?)] = []

    private var sessionShown = Set<Int64>()
    private var pending = Set<Int64>()
    private var timers: [Int: PulseCancellable] = [:]
    private var timerSeq = 0
    private var once = Set<Int64>()
    private var daily: [Int64: String] = [:]
    private var showing = false

    init(_ pulse: PulseClient) { self.pulse = pulse }

    private func locked<T>(_ f: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try f()
    }

    /// The campaigns fetched last, in priority order.
    public var messages: [PulseInAppMessage] { locked { campaigns } }
    /// Whether an in-app message is on screen.
    public var isShowing: Bool { locked { showing } }

    func load() {
        guard let raw = pulse.env.storage.read(pulse.keys.inApp), let j = JSON.decode(raw) else { return }
        once = Set((j["once"]?.array ?? []).compactMap { $0.int })
        for (k, v) in j["daily"]?.object ?? [:] {
            if let id = Int64(k), let day = v.string { daily[id] = day }
        }
    }

    private func saveLocked() {
        var d: [String: JSON] = [:]
        for (k, v) in daily { d[String(k)] = .string(v) }
        let state = JSON.object(["once": .array(once.sorted().map { .int($0) }), "daily": .object(d)])
        pulse.env.storage.write(pulse.keys.inApp, state.encodedString)
    }

    /// Fetches the current person's campaigns now (on the SDK's queue). On failure the previous ones are kept.
    public func refresh() {
        let gen: Int = locked {
            fetching = true
            generation += 1
            return generation
        }
        pulse.env.dispatcher.execute { [weak self] in self?.fetch(gen) }
    }

    func refreshIfStale() {
        let stale: Bool = locked {
            guard !fetching else { return false }
            guard let at = fetchedAt else { return true }
            return pulse.env.clock().timeIntervalSince(at) >= PulseInApp.refreshAfter
        }
        if stale { refresh() }
    }

    private func fetch(_ gen: Int) {
        let result = (pulse.isOptedOut || pulse.disposed) ? nil : request()
        let triggers: [(String, String?)]? = locked {
            if gen != generation { return nil } // a newer fetch (e.g. after identify) wins
            fetching = false
            if let r = result {
                campaigns = r
                fetchedAt = pulse.env.clock()
            }
            let t = waiting
            waiting.removeAll()
            return t
        }
        for (name, screen) in triggers ?? [] { match(name, screen) }
    }

    private func request() -> [PulseInAppMessage]? {
        var body: [String: JSON] = ["id": .string(pulse.currentID), "platform": .string(pulse.env.platform.rawValue)]
        if let h = pulse.identifiedHash { body["user_hash"] = .string(h) }
        let res: PulseHTTPResponse
        do {
            res = try pulse.post("/v1/web/inapp", body)
        } catch {
            pulse.log("fetching in-app campaigns failed: \(error)", always: true)
            return nil
        }
        guard (200..<300).contains(res.status) else {
            pulse.log("in-app campaigns: server answered \(res.status)", always: true)
            return nil
        }
        guard let j = JSON.decode(res.body) else { return nil }
        let out = (j["messages"]?.array ?? []).compactMap(PulseInAppMessage.init(json:))
        pulse.log("\(out.count) in-app campaigns for \(pulse.currentID)")
        return out
    }

    private func today() -> String { PulseTime.day(pulse.env.clock()) }

    private func allowsLocked(_ m: PulseInAppMessage) -> Bool {
        if pending.contains(m.id) { return false }
        if let end = m.endAt, pulse.env.clock() > end { return false }
        switch m.trigger.frequency {
        case "always": return true
        case "session": return !sessionShown.contains(m.id)
        case "daily": return daily[m.id] != today()
        default: return !once.contains(m.id) && !sessionShown.contains(m.id) // once, and anything unknown
        }
    }

    /// Whether campaign `m` may be shown now under its frequency rule.
    public func allows(_ m: PulseInAppMessage) -> Bool { locked { allowsLocked(m) } }

    // Called for every tracked event and screen.
    func onEvent(_ name: String, screen: String?) {
        if pulse.disposed { return }
        let deferred: Bool = locked {
            if fetching && fetchedAt == nil {
                // The first fetch is still running (e.g. app_open right after start): decide once it's done.
                waiting.append((name, screen))
                return true
            }
            return false
        }
        if !deferred { match(name, screen) }
    }

    private func match(_ name: String, _ screen: String?) {
        locked {
            if pulse.disposed || showing { return }
            guard let m = campaigns.first(where: { $0.trigger.matches(name, screen: screen) && allowsLocked($0) }) else { return }
            pending.insert(m.id)
            timerSeq += 1
            let key = timerSeq
            timers[key] = pulse.env.dispatcher.schedule(after: TimeInterval(max(0, m.trigger.delaySeconds))) { [weak self] in
                guard let self = self else { return }
                self.locked {
                    self.timers[key] = nil
                    self.pending.remove(m.id)
                }
                self.show(m)
            }
        }
    }

    private func show(_ m: PulseInAppMessage) {
        if pulse.disposed || pulse.isOptedOut { return }
        guard let presenter = pulse.env.inAppPresenter, presenter.canPresent() else {
            pulse.log("in-app \(m.id) not shown: nothing to show it on", always: true)
            return
        }
        let go: Bool = locked {
            if showing { return false }
            showing = true
            sessionShown.insert(m.id)
            switch m.trigger.frequency {
            case "daily": daily[m.id] = today(); saveLocked()
            case "session", "always": break
            default: once.insert(m.id); saveLocked()
            }
            return true
        }
        guard go else { return }
        pulse.enqueue("inapp_shown", props: ["inapp_id": .int(m.id)])
        presenter.present(m) { [weak self] outcome in self?.closed(m, outcome) }
    }

    private func closed(_ m: PulseInAppMessage, _ outcome: PulseInAppOutcome) {
        locked { showing = false }
        if let b = outcome.button, b.isURL, let value = b.value {
            pulse.enqueue("inapp_clicked", props: ["inapp_id": .int(m.id), "button": .string(b.text), "url": .string(value)])
            if let action = pulse.config.onInAppAction { pulse.env.callback { action(value) } }
        } else if !outcome.expired {
            pulse.enqueue("inapp_dismissed", props: ["inapp_id": .int(m.id)])
        }
    }

    func cancelPending() {
        locked {
            timers.values.forEach { $0.cancel() }
            timers.removeAll()
            pending.removeAll()
            waiting.removeAll()
        }
    }
}
