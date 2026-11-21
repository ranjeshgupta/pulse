import Foundation

/// The card of an app content slot. Every field is optional; `data` holds the slot's custom key-values.
public struct PulseSlotCard: Codable, Hashable {
    public var title: String?
    public var body: String?
    public var imageURL: String?
    public var buttonText: String?
    /// An https link or app deep link (`scheme://…`) to open on tap.
    public var deepLink: String?
    /// Custom key-values set in Pulse (empty when none).
    public var data: [String: String]

    enum CodingKeys: String, CodingKey {
        case title, body, data
        case imageURL = "image_url"
        case buttonText = "button_text"
        case deepLink = "deep_link"
    }

    public init(title: String? = nil, body: String? = nil, imageURL: String? = nil, buttonText: String? = nil,
                deepLink: String? = nil, data: [String: String] = [:]) {
        self.title = title
        self.body = body
        self.imageURL = imageURL
        self.buttonText = buttonText
        self.deepLink = deepLink
        self.data = data
    }

    /// Lenient: a field of the wrong type reads as missing; empty strings read as nil; non-string
    /// data values are kept as their JSON text.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func text(_ k: CodingKeys) -> String? {
            guard let s = (try? c.decodeIfPresent(String.self, forKey: k)) ?? nil, !s.isEmpty else { return nil }
            return s
        }
        title = text(.title)
        body = text(.body)
        imageURL = text(.imageURL)
        buttonText = text(.buttonText)
        deepLink = text(.deepLink)
        var d: [String: String] = [:]
        for (k, v) in ((try? c.decodeIfPresent([String: JSON].self, forKey: .data)) ?? nil) ?? [:] {
            switch v {
            case .string(let s): d[k] = s
            case .null: d[k] = ""
            default: d[k] = v.encodedString
            }
        }
        data = d
    }
}

/// What an app content slot shows: a `card` (drawn by `PulseSlotView` / `PulseSlot`), and/or `html`
/// (for the app to draw itself, e.g. in a web view). `variant` is the variant shown (nil or "" for the
/// slot's default content).
public struct PulseSlotContent: Codable, Hashable {
    public var card: PulseSlotCard?
    public var html: String?
    public var variant: String?

    public init(card: PulseSlotCard? = nil, html: String? = nil, variant: String? = nil) {
        self.card = card
        self.html = html
        self.variant = variant
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        card = (try? c.decodeIfPresent(PulseSlotCard.self, forKey: .card)) ?? nil
        html = ((try? c.decodeIfPresent(String.self, forKey: .html)) ?? nil).flatMap { $0.isEmpty ? nil : $0 }
        variant = ((try? c.decodeIfPresent(String.self, forKey: .variant)) ?? nil).flatMap { $0.isEmpty ? nil : $0 }
    }

    /// Whether there is nothing to show (no card and no html).
    public var isEmpty: Bool { card == nil && html == nil }
}

/// App content slots: dynamic content for named places in the app (the app twin of a website's
/// dynamic web content), `PulseClient.shared?.content`.
///
/// Slots are created in Pulse under Website & app messages → Content slots with "Where: App". The SDK
/// asks `POST /v1/web/content` for the slots the app shows and keeps the answers in memory. The
/// content of every slot asked for is fetched again after `identify` (a different user's content is
/// dropped at once), after `reset`, after `optIn`, and when the app comes to the foreground more than
/// 15 minutes after the last fetch. Failures never throw: the cached content is kept.
///
/// `PulseSlotView` (UIKit) and `PulseSlot` (SwiftUI) in the Pulse module use this and record
/// `app_content_viewed` and `app_content_clicked`; apps drawing slots themselves call `viewed` and
/// `clicked`.
public final class PulseContent {
    /// How old fetched content may get before the foreground refresh and `load` fetch it again: 15 minutes.
    public static let refreshAfter: TimeInterval = 15 * 60
    /// Slots per request (the server's limit); longer lists are sent in several requests.
    public static let maxSlotsPerRequest = 50

    private let pulse: PulseClient // strong: the client lives as long as the app; a reference kept by the app never dangles
    private let lock = NSRecursiveLock()
    private var cache: [String: PulseSlotContent] = [:]
    private var fetchedAt: [String: Date] = [:]
    private var known = Set<String>()
    private var inFlight = Set<String>()
    private var wanted = Set<String>()
    private var loadQueued = false
    private var generation = 0
    private var listeners: [UUID: (Set<String>) -> Void] = [:]

    init(_ pulse: PulseClient) { self.pulse = pulse }

    private func locked<T>(_ f: () throws -> T) rethrows -> T {
        lock.lock()
        defer { lock.unlock() }
        return try f()
    }

    /// Whether `slot` is a valid slot id: lowercase letters, digits, `-` and `_`, starting with a
    /// letter or digit, at most 64 characters.
    public static func isValidSlot(_ slot: String) -> Bool {
        guard let first = slot.unicodeScalars.first, slot.unicodeScalars.count <= 64 else { return false }
        func ok(_ c: Unicode.Scalar) -> Bool { ("a"..."z").contains(c) || ("0"..."9").contains(c) }
        return ok(first) && slot.unicodeScalars.allSatisfy { ok($0) || $0 == "-" || $0 == "_" }
    }

    private func valid(_ slots: [String]) -> [String] {
        var seen = Set<String>()
        var out: [String] = []
        for s in slots {
            guard PulseContent.isValidSlot(s) else {
                pulse.log("content slot id \"\(s)\" ignored: use lowercase letters, digits, - or _", always: true)
                continue
            }
            if seen.insert(s).inserted { out.append(s) }
        }
        return out
    }

    // MARK: - Reading

    /// The content of `slot` fetched last, or nil (nothing to show, or not fetched yet).
    public func cached(_ slot: String) -> PulseSlotContent? { locked { cache[slot] } }

    /// Fetches `slots` now, on the calling thread (not the main thread), and returns the content of
    /// those that have some. A slot missing from the answer shows nothing. On a network error or an
    /// error answer the cached content is returned. While opted out nothing is requested.
    @discardableResult
    public func fetchBlocking(_ slots: [String]) -> [String: PulseSlotContent] {
        let ids = valid(slots)
        if ids.isEmpty { return [:] }
        let gen: Int = locked {
            known.formUnion(ids)
            inFlight.formUnion(ids)
            return generation
        }
        perform(ids, gen)
        return locked {
            var out: [String: PulseSlotContent] = [:]
            for s in ids { if let c = cache[s] { out[s] = c } }
            return out
        }
    }

    /// `fetchBlocking` on the SDK's queue.
    public func fetch(_ slots: [String]) async -> [String: PulseSlotContent] {
        await withCheckedContinuation { (c: CheckedContinuation<[String: PulseSlotContent], Never>) in
            pulse.env.dispatcher.execute { c.resume(returning: self.fetchBlocking(slots)) }
        }
    }

    /// `fetchBlocking` on the SDK's queue; `completion` runs on the main queue.
    public func fetch(_ slots: [String], completion: @escaping ([String: PulseSlotContent]) -> Void) {
        pulse.env.dispatcher.execute { [weak self] in
            guard let self = self else { return }
            let res = self.fetchBlocking(slots)
            self.pulse.env.callback { completion(res) }
        }
    }

    /// Fetches, in the background, those of `slots` that were never fetched or were fetched more than
    /// 15 minutes ago; calls made close together share one request. Listeners hear about changes.
    /// The slot views call this when they appear.
    public func load(_ slots: [String]) {
        let ids = valid(slots)
        if ids.isEmpty { return }
        let go: Bool = locked {
            known.formUnion(ids)
            let now = pulse.env.clock()
            let need = ids.filter { s in
                if inFlight.contains(s) { return false }
                guard let at = fetchedAt[s] else { return true }
                return now.timeIntervalSince(at) >= PulseContent.refreshAfter
            }
            if need.isEmpty { return false }
            wanted.formUnion(need)
            if loadQueued { return false }
            loadQueued = true
            return true
        }
        if go { pulse.env.dispatcher.execute { [weak self] in self?.drain() } }
    }

    /// Fetches every slot asked for so far again, in the background.
    public func refresh() {
        let ids: [String] = locked {
            wanted.formUnion(known.subtracting(inFlight))
            if loadQueued || wanted.isEmpty { return [] }
            loadQueued = true
            return Array(wanted)
        }
        if !ids.isEmpty { pulse.env.dispatcher.execute { [weak self] in self?.drain() } }
    }

    /// Calls `listener` (on the main queue) with the ids of the slots whose content changed. Cancel
    /// the returned handle to stop.
    @discardableResult
    public func addListener(_ listener: @escaping (Set<String>) -> Void) -> PulseCancellable {
        let id = UUID()
        locked { listeners[id] = listener }
        return ListenerHandle { [weak self] in self?.locked { self?.listeners[id] = nil } }
    }

    private struct ListenerHandle: PulseCancellable {
        let remove: () -> Void
        func cancel() { remove() }
    }

    // MARK: - Fetching

    private func drain() {
        let (ids, gen): ([String], Int) = locked {
            loadQueued = false
            let ids = wanted.sorted()
            wanted.removeAll()
            inFlight.formUnion(ids)
            return (ids, generation)
        }
        if !ids.isEmpty { perform(ids, gen) }
    }

    // Requests `ids` (already in inFlight) in chunks and applies the answers unless the identity changed.
    private func perform(_ ids: [String], _ gen: Int) {
        var start = 0
        while start < ids.count {
            let chunk = Array(ids[start..<min(start + PulseContent.maxSlotsPerRequest, ids.count)])
            start += chunk.count
            let result = (pulse.isOptedOut || pulse.disposed) ? nil : request(chunk)
            let (changed, ls): (Set<String>, [(Set<String>) -> Void]) = locked {
                if gen != generation { return ([], []) } // identify / reset since: a newer fetch wins
                inFlight.subtract(chunk)
                guard let r = result else { return ([], []) }
                let now = pulse.env.clock()
                var changed = Set<String>()
                for s in chunk {
                    let new = r[s]
                    if cache[s] != new { changed.insert(s) }
                    cache[s] = new
                    fetchedAt[s] = now
                }
                return (changed, Array(listeners.values))
            }
            notify(changed, ls)
        }
    }

    private func notify(_ changed: Set<String>, _ ls: [(Set<String>) -> Void]) {
        if changed.isEmpty || ls.isEmpty { return }
        pulse.env.callback { ls.forEach { $0(changed) } }
    }

    private func request(_ slots: [String]) -> [String: PulseSlotContent]? {
        var body: [String: JSON] = [
            "id": .string(pulse.currentID),
            "platform": .string(pulse.env.platform.rawValue),
            "slots": .array(slots.map { .string($0) }),
        ]
        if let h = pulse.identifiedHash { body["user_hash"] = .string(h) }
        let res: PulseHTTPResponse
        do {
            res = try pulse.post("/v1/web/content", body)
        } catch {
            pulse.log("fetching app content failed: \(error)", always: true)
            return nil
        }
        guard (200..<300).contains(res.status) else {
            pulse.log("app content: server answered \(res.status)", always: true)
            return nil
        }
        guard let j = JSON.decode(res.body), j.object != nil else {
            pulse.log("app content answer is unreadable", always: true)
            return nil
        }
        return PulseContent.parse(j, slots: slots)
    }

    /// Reads the server's answer: the content of `slots` that have something to show.
    static func parse(_ answer: JSON, slots: [String]) -> [String: PulseSlotContent] {
        let all = answer["content"]?.object ?? [:]
        var out: [String: PulseSlotContent] = [:]
        for s in slots {
            guard let v = all[s], v.object != nil,
                  let c = try? JSONDecoder().decode(PulseSlotContent.self, from: v.encoded()), !c.isEmpty else { continue }
            out[s] = c
        }
        return out
    }

    // MARK: - Lifecycle (called by the client)

    // After identify (`clear` when the user changed) and reset: drops the content of the person before
    // when needed, and fetches every known slot again.
    func identityChanged(clear: Bool) {
        let (changed, ls): (Set<String>, [(Set<String>) -> Void]) = locked {
            generation += 1
            inFlight.removeAll()
            fetchedAt.removeAll()
            guard clear else { return ([], []) }
            let changed = Set(cache.keys)
            cache.removeAll()
            return (changed, Array(listeners.values))
        }
        notify(changed, ls)
        if !pulse.isOptedOut { refresh() }
    }

    func refreshIfStale() {
        let ids: [String] = locked { Array(known) }
        load(ids)
    }

    // MARK: - Tracking

    /// Records `app_content_viewed` {slot, variant} for content shown in `slot`. The slot views call it
    /// once per content they show; apps drawing a slot themselves call it when it appears.
    public func viewed(_ slot: String, _ content: PulseSlotContent) {
        try? pulse.track("app_content_viewed", properties: ["slot": slot, "variant": content.variant ?? ""])
    }

    /// Records `app_content_clicked` {slot, variant, link} for a tap on `slot`, then hands the link
    /// (`link`, else the card's deep link) to `PulseConfig.onInAppAction` on the main queue, as in-app
    /// buttons do.
    public func clicked(_ slot: String, _ content: PulseSlotContent, link: String? = nil) {
        let target = link ?? content.card?.deepLink
        var props: [String: Any] = ["slot": slot, "variant": content.variant ?? ""]
        if let t = target { props["link"] = t }
        try? pulse.track("app_content_clicked", properties: props)
        if let t = target, let action = pulse.config.onInAppAction { pulse.env.callback { action(t) } }
    }
}
