import Foundation

/// One message in the app inbox.
public struct PulseInboxMessage: Equatable {
    /// The inbox entry's id: pass it to markRead, delete, click and list(before:).
    public let id: Int64
    /// The id of the send this message came from.
    public let messageID: String
    public let title: String
    public let body: String
    public let imageURL: String?
    /// Link or deep link to open when the message is tapped.
    public let url: String?
    /// Category set by the sender.
    public let category: String?
    /// Custom key-value data set by the sender.
    public let data: [String: String]
    public let createdAt: Date
    /// When the message was read, or nil while unread.
    public let readAt: Date?
    /// When the server removes the message.
    public let expiresAt: Date

    public var isRead: Bool { readAt != nil }

    /// Reads one entry of the server's `messages` list.
    public init?(json j: JSON) {
        guard let id = j["id"]?.int else { return nil }
        self.id = id
        messageID = j["message_id"]?.text ?? ""
        title = j["title"]?.text ?? ""
        body = j["body"]?.text ?? ""
        imageURL = j["image_url"]?.text
        url = j["url"]?.text
        category = j["category"]?.text
        var d: [String: String] = [:]
        for (k, v) in j["data"]?.object ?? [:] {
            switch v {
            case .string(let s): d[k] = s
            case .null: d[k] = ""
            default: d[k] = String(decoding: v.encoded(), as: UTF8.self)
            }
        }
        data = d
        createdAt = PulseTime.parse(j["created_at"]?.text) ?? Date(timeIntervalSince1970: 0)
        readAt = PulseTime.parse(j["read_at"]?.text)
        expiresAt = PulseTime.parse(j["expires_at"]?.text) ?? Date(timeIntervalSince1970: 0)
    }
}

/// One page of the inbox, newest first.
public struct PulseInboxPage: Equatable {
    public var messages: [PulseInboxMessage]
    /// Unread messages in the whole inbox.
    public var unread: Int
    public init(messages: [PulseInboxMessage] = [], unread: Int = 0) {
        self.messages = messages
        self.unread = unread
    }
    /// An empty page (also what `list` returns when the request failed).
    public static let empty = PulseInboxPage()
    /// Whether older messages may exist: call `list(before: messages.last!.id)`.
    public var hasMore: Bool { messages.count >= PulseInbox.pageSize }
}

/// The current person's app inbox: `PulseClient.shared?.inbox`.
///
/// A signed-in user needs their user hash (`identify(_:userHash:)`) even when the workspace does not
/// require identity verification for events, because inboxes are private: without it the calls throw
/// `PulseError.userHashRequired`. Anonymous people need none. Network errors, 429 and 5xx don't throw:
/// `list` returns an empty page and the actions return 0 / false. While opted out nothing is requested.
///
/// The `...Blocking` methods run on the calling thread (not the main thread); the `async` ones run on
/// the SDK's queue.
public final class PulseInbox {
    /// Messages per page of `GET /v1/web/inbox`.
    public static let pageSize = 50

    private let pulse: PulseClient // strong: the client lives as long as the app; a reference kept by the app never dangles
    private let lock = NSLock()
    private var unread = 0
    private var known: [Int64: Bool] = [:]
    private var listeners: [UUID: (Int) -> Void] = [:]

    init(_ pulse: PulseClient) { self.pulse = pulse }

    /// Unread messages, as of the last `list` (and lowered by `markRead` and `delete`).
    public var unreadCount: Int { lock.lock(); defer { lock.unlock() }; return unread }

    /// Calls `listener` (on the main queue) with the unread count whenever it changes. Cancel the
    /// returned handle to stop.
    @discardableResult
    public func addUnreadListener(_ listener: @escaping (Int) -> Void) -> PulseCancellable {
        let id = UUID()
        lock.lock()
        listeners[id] = listener
        lock.unlock()
        return ListenerHandle { [weak self] in
            guard let self = self else { return }
            self.lock.lock()
            self.listeners[id] = nil
            self.lock.unlock()
        }
    }

    private struct ListenerHandle: PulseCancellable {
        let remove: () -> Void
        func cancel() { remove() }
    }

    private func setUnread(_ n: Int) {
        lock.lock()
        let changed = unread != n
        unread = n
        let ls = Array(listeners.values)
        lock.unlock()
        if changed && !ls.isEmpty { pulse.env.callback { ls.forEach { $0(n) } } }
    }

    /// Returns the newest 50 messages, or the 50 before message id `before`, and updates `unreadCount`.
    public func listBlocking(before: Int64? = nil) throws -> PulseInboxPage {
        if pulse.isOptedOut { return .empty }
        try checkHash()
        var query = [("id", pulse.currentID)]
        if let h = pulse.identifiedHash { query.append(("user_hash", h)) }
        if let b = before { query.append(("before", String(b))) }
        let res: PulseHTTPResponse
        do {
            res = try pulse.get("/v1/web/inbox", query: query)
        } catch {
            pulse.log("inbox request failed: \(error)", always: true)
            return .empty
        }
        guard try ok(res, "list") else { return .empty }
        guard let j = JSON.decode(res.body), j.object != nil else {
            pulse.log("inbox answer is unreadable", always: true)
            return .empty
        }
        let messages = (j["messages"]?.array ?? []).compactMap(PulseInboxMessage.init(json:))
        let n = Int(j["unread"]?.int ?? 0)
        lock.lock()
        if before == nil { known.removeAll() }
        for m in messages { known[m.id] = !m.isRead }
        lock.unlock()
        setUnread(n)
        return PulseInboxPage(messages: messages, unread: n)
    }

    /// Marks messages `ids` read, or every message when `ids` is nil. Returns how many changed (an
    /// empty list sends nothing). Reads count as opens in the sender's report.
    @discardableResult
    public func markReadBlocking(_ ids: [Int64]? = nil) throws -> Int {
        if let ids = ids, ids.isEmpty { return 0 }
        let changed = try action("read", ids ?? [])
        lock.lock()
        var n = unread
        if let ids = ids {
            if changed > 0 {
                for id in ids where known[id] != nil { known[id] = false }
                n = max(0, unread - changed)
            }
        } else {
            for k in known.keys { known[k] = false }
            n = 0
        }
        lock.unlock()
        setUnread(n)
        return changed
    }

    /// Deletes messages `ids`, or **every message** when `ids` is nil. Returns how many were deleted.
    @discardableResult
    public func deleteBlocking(_ ids: [Int64]? = nil) throws -> Int {
        if let ids = ids, ids.isEmpty { return 0 }
        let changed = try action("delete", ids ?? [])
        lock.lock()
        var n = unread
        if let ids = ids {
            if changed > 0 {
                let unreadGone = ids.filter { known.removeValue(forKey: $0) == true }.count
                n = max(0, unread - unreadGone)
            }
        } else {
            known.removeAll()
            n = 0
        }
        lock.unlock()
        setUnread(n)
        return changed
    }

    /// Records a tap on message `id` (a click in the sender's report). Open its `url` yourself.
    @discardableResult
    public func clickBlocking(_ id: Int64) throws -> Bool { try action("click", [id]) > 0 }

    /// `listBlocking` on the SDK's queue.
    public func list(before: Int64? = nil) async throws -> PulseInboxPage { try await run { try self.listBlocking(before: before) } }

    /// `markReadBlocking` on the SDK's queue.
    @discardableResult
    public func markRead(_ ids: [Int64]? = nil) async throws -> Int { try await run { try self.markReadBlocking(ids) } }

    /// `deleteBlocking` on the SDK's queue.
    @discardableResult
    public func delete(_ ids: [Int64]? = nil) async throws -> Int { try await run { try self.deleteBlocking(ids) } }

    /// `clickBlocking` on the SDK's queue.
    @discardableResult
    public func click(_ id: Int64) async throws -> Bool { try await run { try self.clickBlocking(id) } }

    private func run<T>(_ work: @escaping () throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { (c: CheckedContinuation<T, Error>) in
            pulse.env.dispatcher.execute {
                do { c.resume(returning: try work()) } catch { c.resume(throwing: error) }
            }
        }
    }

    private func action(_ action: String, _ ids: [Int64]) throws -> Int {
        if pulse.isOptedOut { return 0 }
        try checkHash()
        var body: [String: JSON] = [
            "id": .string(pulse.currentID),
            "action": .string(action),
            "message_ids": .array(ids.map { .int($0) }),
        ]
        if let h = pulse.identifiedHash { body["user_hash"] = .string(h) }
        let res: PulseHTTPResponse
        do {
            res = try pulse.post("/v1/web/inbox", body)
        } catch {
            pulse.log("inbox \(action) failed: \(error)", always: true)
            return 0
        }
        guard try ok(res, action) else { return 0 }
        return Int(JSON.decode(res.body)?["changed"]?.int ?? 0)
    }

    private func checkHash() throws {
        if pulse.isIdentified && pulse.identifiedHash == nil {
            throw PulseError.userHashRequired(
                "the inbox of a signed-in user needs their user hash: call identify(userID, userHash: " +
                    "hex(HMAC-SHA256(identity secret, userID))) with the hash computed on your server",
                statusCode: nil)
        }
    }

    private func ok(_ res: PulseHTTPResponse, _ what: String) throws -> Bool {
        let code = res.status
        if (200..<300).contains(code) { return true }
        if code == 403 {
            throw PulseError.userHashRequired(
                "the server refused the inbox of \(pulse.currentID): a signed-in user needs a valid user hash",
                statusCode: 403)
        }
        pulse.log(code == 429 ? "inbox \(what) rate limited (429); try again later"
            : "inbox \(what) answered \(code): \(String(decoding: res.body, as: UTF8.self))", always: true)
        return false
    }

    func forget() {
        lock.lock()
        known.removeAll()
        lock.unlock()
        setUnread(0)
    }
}
