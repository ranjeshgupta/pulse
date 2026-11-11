import Foundation
import XCTest
@testable import PulseCore

final class SessionDeviceTests: XCTestCase {
    let deviceKeys: Set<String> = ["platform", "device_type", "os", "os_version", "locale", "app_version", "device_model", "device_brand"]

    func testDevicePropsOnTheRightEvents() throws {
        let t = TestEnv()
        let p = t.client()
        try p.screen("Home", properties: ["step": 2])
        try p.track("custom")
        try p.identify("u1")
        var byName: [String: JSON] = [:]
        for e in t.http.sentEvents { byName[e["name"]!.string!] = e }

        let start = byName["session_start"]!["props"]!.object!
        XCTAssertEqual(Set(start.keys), deviceKeys.union(["session_id"]))
        XCTAssertEqual(start["platform"], .string("ios"))
        XCTAssertEqual(start["device_type"], .string("mobile"))
        XCTAssertEqual(start["os"], .string("iOS"))
        XCTAssertEqual(start["os_version"], .string("17.4"))
        XCTAssertEqual(start["app_version"], .string("2.3.0"))
        XCTAssertEqual(start["device_model"], .string("iPhone15,2"))
        XCTAssertEqual(start["device_brand"], .string("Apple"))
        XCTAssertEqual(start["locale"], .string("en-IN"))

        let screen = byName["screen_view"]!["props"]!.object!
        XCTAssertEqual(screen["screen"], .string("Home"))
        XCTAssertEqual(screen["step"], .int(2))
        XCTAssertEqual(screen["session_id"], start["session_id"])
        XCTAssertTrue(deviceKeys.isSubset(of: Set(screen.keys)))
        XCTAssertTrue(deviceKeys.isSubset(of: Set(byName["identify"]!["props"]!.object!.keys)))
        XCTAssertNil(byName["custom"]!["props"], "custom events carry no device props")
    }

    func testDevicePropsClippedAndConfigWins() throws {
        let info = DeviceInfo(platform: "ios", deviceType: "tablet", os: "iOS", osVersion: "17.4", locale: "en-US",
                              appVersion: "1.0", deviceModel: String(repeating: "x", count: 150), deviceBrand: "  Apple  ")
        let props = PulseDevice.buildProps(info, config: try PulseConfig(host: "https://h", siteKey: "sk", appVersion: "9.9"))
        XCTAssertEqual(props["device_model"]?.count, 100)
        XCTAssertEqual(props["device_brand"], "Apple")
        XCTAssertEqual(props["app_version"], "9.9")
        XCTAssertEqual(props["device_type"], "tablet")
        XCTAssertEqual(PulseDevice.buildProps(DeviceInfo(), config: nil), [:])
    }

    func testSessionsEndAfter30MinutesIdle() throws {
        let t = TestEnv()
        let p = t.client()
        try p.track("a")
        let first = p.sessionID
        t.now += 29 * 60
        try p.track("b")
        XCTAssertEqual(p.sessionID, first)
        t.now += 30 * 60
        try p.track("c")
        XCTAssertNotEqual(p.sessionID, first)
        p.flushAndWait()
        XCTAssertEqual(t.http.eventNames, ["session_start", "a", "b", "session_start", "c"])
    }

    func testBackgroundAndForeground() throws {
        let t = TestEnv()
        let p = t.client()
        try p.track("a")
        let first = p.sessionID
        p.appDidEnterBackground() // flushes
        XCTAssertEqual(t.http.eventNames, ["session_start", "a"])
        t.now += 31 * 60
        p.appWillEnterForeground()
        XCTAssertNotEqual(p.sessionID, first)
        let second = p.sessionID
        p.appDidEnterBackground()
        t.now += 5 * 60
        p.appWillEnterForeground()
        XCTAssertEqual(p.sessionID, second)

        // A restart within 30 minutes continues the session.
        p.dispose()
        t.now += 60
        XCTAssertEqual(t.client().sessionID, second)
        XCTAssertEqual(t.storage.read("pulse.sk_test.sid"), second)
    }
}

final class PushTrackingTests: XCTestCase {
    func testReportURLs() {
        let u = "https://pulse.example.com/t/p/TOKEN"
        XCTAssertEqual(PulsePush.reportURL(u, action: .clicked, platform: .ios), u + "?a=clicked&platform=ios")
        XCTAssertEqual(PulsePush.reportURL(u, action: .dismissed, platform: .android), u + "?a=dismissed&platform=android")
        XCTAssertEqual(PulsePush.reportURL(u + "?x=1#f", action: .clicked, platform: .ios), u + "?x=1&a=clicked&platform=ios")
        for bad in ["javascript:alert(1)", "file:///etc/passwd", "ftp://h/t", "/t/p/x", "", "https://", "not a url"] {
            XCTAssertNil(PulsePush.reportURL(bad, action: .clicked, platform: .ios), bad)
        }
    }

    func testTrackURLFromUserInfo() {
        XCTAssertEqual(PulsePush.trackURL(from: ["pulse_track": "https://h/t/p/x", "aps": ["alert": "Hi"]]), "https://h/t/p/x")
        XCTAssertEqual(PulsePush.trackURL(from: ["data": ["pulse_track": "https://h/t/p/y"]]), "https://h/t/p/y")
        XCTAssertNil(PulsePush.trackURL(from: ["aps": ["alert": "Hi"]]))
        XCTAssertNil(PulsePush.trackURL(from: ["pulse_track": "javascript:x"]))
        XCTAssertNil(PulsePush.trackURL(from: ["pulse_track": 5]))
        XCTAssertNil(PulsePush.trackURL(from: nil))
    }

    func testClickIsPostedWithoutBodyOrSiteKey() {
        let t = TestEnv()
        t.http.handler = respond(204)
        let p = t.client()
        XCTAssertTrue(p.trackPushClicked(userInfo: ["pulse_track": "https://pulse.example.com/t/p/abc"]))
        let r = t.http.requests[0]
        XCTAssertEqual(r.method, "POST")
        XCTAssertEqual(r.url.absoluteString, "https://pulse.example.com/t/p/abc?a=clicked&platform=ios")
        XCTAssertNil(r.body)
        XCTAssertNil(r.headers["X-Pulse-Site-Key"])
        XCTAssertEqual(p.pendingPushReports, 0)
        XCTAssertTrue(p.trackPushDismissed(userInfo: ["pulse_track": "https://pulse.example.com/t/p/def"]))
        XCTAssertEqual(t.http.requests.last?.url.absoluteString, "https://pulse.example.com/t/p/def?a=dismissed&platform=ios")
        XCTAssertFalse(p.trackPushClicked(userInfo: ["other": "x"]))
        XCTAssertEqual(t.http.requests.count, 2)
    }

    func testReportsRetriedAndSurviveRestart() {
        let t = TestEnv()
        t.http.handler = offline
        let p = t.client()
        p.trackPushClicked(userInfo: ["pulse_track": "https://pulse.example.com/t/p/abc"])
        XCTAssertEqual(p.pendingPushReports, 1)
        XCTAssertTrue(t.storage.read("pulse.sk_test.ptrack")?.contains("abc") ?? false)
        p.dispose()
        let again = t.client()
        XCTAssertEqual(again.pendingPushReports, 1)
        t.http.handler = respond(503)
        again.flushAndWait()
        XCTAssertEqual(again.pendingPushReports, 1, "5xx keeps it")
        t.http.handler = respond(404)
        t.advance(10)
        XCTAssertEqual(again.pendingPushReports, 0, "404 drops it")
        XCTAssertNil(t.storage.read("pulse.sk_test.ptrack"))
    }
}

final class InboxTests: XCTestCase {
    let page = """
    {"messages":[
      {"id":12,"message_id":"m-12","title":"Hello","body":"World","image_url":"https://img/x.png",
       "url":"app://offer","category":"promo","data":{"k":"v","n":3},
       "created_at":"2024-05-01T10:00:00.123456Z","read_at":null,"expires_at":"2024-06-01T10:00:00+05:30"},
      {"id":11,"message_id":"m-11","title":"","body":"Old","created_at":"2024-04-01T10:00:00Z",
       "read_at":"2024-04-02T10:00:00Z","expires_at":"2024-05-01T10:00:00Z"}
    ],"unread":5}
    """

    func testListParsesMessages() throws {
        let t = TestEnv()
        t.http.handler = respond(200, page)
        let p = t.client()
        var counts: [Int] = []
        p.inbox.addUnreadListener { counts.append($0) }
        let res = try p.inbox.listBlocking()
        let r = t.http.requests.last!
        XCTAssertEqual(r.method, "GET")
        XCTAssertEqual(r.url.absoluteString, "https://pulse.example.com/v1/web/inbox?id=\(p.currentID)")
        XCTAssertEqual(r.headers["X-Pulse-Site-Key"], "sk_test")
        XCTAssertEqual(res.unread, 5)
        XCTAssertEqual(p.inbox.unreadCount, 5)
        XCTAssertEqual(counts, [5])
        XCTAssertFalse(res.hasMore)
        let m = res.messages[0]
        XCTAssertEqual(m.id, 12)
        XCTAssertEqual(m.messageID, "m-12")
        XCTAssertEqual(m.title, "Hello")
        XCTAssertEqual(m.imageURL, "https://img/x.png")
        XCTAssertEqual(m.url, "app://offer")
        XCTAssertEqual(m.category, "promo")
        XCTAssertEqual(m.data, ["k": "v", "n": "3"])
        XCTAssertEqual(PulseTime.iso(m.createdAt), "2024-05-01T10:00:00.123Z")
        XCTAssertEqual(PulseTime.iso(m.expiresAt), "2024-06-01T04:30:00.000Z")
        XCTAssertFalse(m.isRead)
        XCTAssertTrue(res.messages[1].isRead)
        _ = try p.inbox.listBlocking(before: 11)
        XCTAssertTrue(t.http.requests.last!.url.absoluteString.hasSuffix("&before=11"))
    }

    func testSignedInUserNeedsUserHash() throws {
        let t = TestEnv()
        t.http.handler = respond(200, page)
        let p = t.client()
        try p.identify("u+1")
        t.http.requests.removeAll()
        XCTAssertThrowsError(try p.inbox.listBlocking()) {
            XCTAssertEqual($0 as? PulseError, .userHashRequired("the inbox of a signed-in user needs their user hash: call identify(userID, userHash: hex(HMAC-SHA256(identity secret, userID))) with the hash computed on your server", statusCode: nil))
        }
        XCTAssertThrowsError(try p.inbox.markReadBlocking([1]))
        XCTAssertEqual(t.http.requests.count, 0, "refused before sending")

        try p.identify("u+1", userHash: "abc")
        _ = try p.inbox.listBlocking()
        XCTAssertEqual(t.http.requests.last!.url.absoluteString, "https://pulse.example.com/v1/web/inbox?id=u%2B1&user_hash=abc")

        t.http.handler = respond(403, "{}")
        XCTAssertThrowsError(try p.inbox.listBlocking()) {
            guard case .userHashRequired(_, let code)? = $0 as? PulseError else { return XCTFail() }
            XCTAssertEqual(code, 403)
        }
    }

    func testFailuresReturnEmpty() throws {
        let t = TestEnv()
        let p = t.client()
        for h in [respond(429), respond(500), offline, respond(200, "not json")] {
            t.http.handler = h
            XCTAssertEqual(try p.inbox.listBlocking(), .empty)
            XCTAssertEqual(try p.inbox.markReadBlocking([1]), 0)
            XCTAssertFalse(try p.inbox.clickBlocking(1))
        }
    }

    func testActionsAndUnreadCount() throws {
        let t = TestEnv()
        t.http.handler = respond(200, page)
        let p = t.client()
        _ = try p.inbox.listBlocking()
        t.http.handler = respond(200, "{\"changed\":1}")
        XCTAssertEqual(try p.inbox.markReadBlocking([12]), 1)
        XCTAssertEqual(t.http.body(t.http.requests.last!),
                       .object(["id": .string(p.currentID), "action": .string("read"), "message_ids": .array([.int(12)])]))
        XCTAssertEqual(p.inbox.unreadCount, 4)
        let n = t.http.requests.count
        XCTAssertEqual(try p.inbox.markReadBlocking([]), 0)
        XCTAssertEqual(try p.inbox.deleteBlocking([]), 0)
        XCTAssertEqual(t.http.requests.count, n)
        XCTAssertTrue(try p.inbox.clickBlocking(12))
        XCTAssertEqual(t.http.body(t.http.requests.last!)["action"], .string("click"))

        t.http.handler = respond(200, page)
        _ = try p.inbox.listBlocking()
        t.http.handler = respond(200, "{\"changed\":1}")
        try p.inbox.deleteBlocking([11])
        XCTAssertEqual(p.inbox.unreadCount, 5, "deleting a read message")
        try p.inbox.deleteBlocking([12])
        XCTAssertEqual(p.inbox.unreadCount, 4)
        try p.inbox.markReadBlocking()
        XCTAssertEqual(t.http.body(t.http.requests.last!)["message_ids"], .array([]))
        XCTAssertEqual(p.inbox.unreadCount, 0)

        t.http.handler = respond(200, page)
        _ = try p.inbox.listBlocking()
        try p.identify("u2", userHash: "h")
        XCTAssertEqual(p.inbox.unreadCount, 0, "identify clears the count")
    }

    func testAsyncList() async throws {
        let t = TestEnv()
        t.http.handler = respond(200, page)
        let p = t.client()
        let res = try await p.inbox.list()
        XCTAssertEqual(res.messages.count, 2)
    }
}

final class InAppTests: XCTestCase {
    final class FakePresenter: PulseInAppPresenter {
        var available = true
        var shown: [PulseInAppMessage] = []
        var close: ((PulseInAppOutcome) -> Void)?
        func canPresent() -> Bool { available }
        func present(_ message: PulseInAppMessage, completion: @escaping (PulseInAppOutcome) -> Void) {
            shown.append(message)
            close = completion
        }
        func dismiss(_ outcome: PulseInAppOutcome = PulseInAppOutcome()) {
            let c = close!
            close = nil
            c(outcome)
        }
    }

    func campaign(_ id: Int, _ event: String, _ frequency: String, screen: String? = nil, delay: Int = 0, endAt: String? = nil) -> JSON {
        var trigger: [String: JSON] = ["trigger_event": .string(event), "delay_seconds": .int(Int64(delay)),
                                       "frequency": .string(frequency), "platforms": .array([.string("ios")])]
        if let s = screen { trigger["screen"] = .string(s) }
        var m: [String: JSON] = [
            "id": .int(Int64(id)), "layout": .string("modal"), "trigger": .object(trigger),
            "content": .object(["title": .string("T\(id)"), "body": .string("B"), "buttons": .array([
                .object(["text": .string("Go"), "action": .string("url"), "value": .string("app://go")]),
                .object(["text": .string("Later"), "action": .string("dismiss")]),
            ])]),
        ]
        if let e = endAt { m["end_at"] = .string(e) }
        return .object(m)
    }

    func setup(_ campaigns: [JSON], onAction: ((String) -> Void)? = nil) -> (TestEnv, PulseClient, FakePresenter) {
        let presenter = FakePresenter()
        let t = TestEnv()
        t.presenter = presenter
        let body = JSON.object(["messages": .array(campaigns)]).encodedString
        t.http.handler = { $0.url.path.hasSuffix("/v1/web/inapp") ? PulseHTTPResponse(status: 200, text: body) : PulseHTTPResponse(status: 202) }
        let p = t.client(makeConfig(inApp: true, onInAppAction: onAction))
        return (t, p, presenter)
    }

    func fire(_ t: TestEnv, _ p: PulseClient, _ name: String) {
        try! p.track(name)
        t.advance(0)
    }

    func testFetchesAtStartWithPlatform() throws {
        let (t, p, _) = setup([campaign(1, "app_open", "once")])
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/inapp")[0]), .object(["id": .string(p.currentID), "platform": .string("ios")]))
        let m = p.inApp.messages[0]
        XCTAssertEqual(m.id, 1)
        XCTAssertEqual(m.content.title, "T1")
        XCTAssertTrue(m.content.buttons[0].isURL)
        try p.identify("u1", userHash: "h")
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/inapp").last!),
                       .object(["id": .string("u1"), "user_hash": .string("h"), "platform": .string("ios")]))
        p.reset()
        XCTAssertEqual(t.http.to("/v1/web/inapp").count, 3)
    }

    func testOnceSurvivesRestart() {
        let (t, p, presenter) = setup([campaign(1, "app_open", "once")])
        fire(t, p, "app_open")
        XCTAssertEqual(presenter.shown.count, 1)
        presenter.dismiss()
        fire(t, p, "app_open")
        XCTAssertEqual(presenter.shown.count, 1)
        p.flushAndWait()
        XCTAssertEqual(t.http.eventNames, ["session_start", "app_open", "inapp_shown", "inapp_dismissed", "app_open"])
        p.dispose()
        let again = t.client(makeConfig(inApp: true))
        fire(t, again, "app_open")
        XCTAssertEqual(presenter.shown.count, 1)
    }

    func testSessionDailyAlways() {
        do {
            let (t, p, presenter) = setup([campaign(2, "x", "session")])
            fire(t, p, "x"); presenter.dismiss()
            fire(t, p, "x")
            XCTAssertEqual(presenter.shown.count, 1)
            p.dispose()
            fire(t, t.client(makeConfig(inApp: true)), "x")
            XCTAssertEqual(presenter.shown.count, 2, "session: once per app run")
        }
        do {
            let (t, p, presenter) = setup([campaign(3, "x", "daily")])
            fire(t, p, "x"); presenter.dismiss()
            fire(t, p, "x")
            XCTAssertEqual(presenter.shown.count, 1)
            t.now += 24 * 3600
            fire(t, p, "x")
            XCTAssertEqual(presenter.shown.count, 2, "daily: next day")
        }
        do {
            let (t, p, presenter) = setup([campaign(4, "x", "always")])
            fire(t, p, "x")
            fire(t, p, "x")
            XCTAssertEqual(presenter.shown.count, 1, "one at a time")
            XCTAssertTrue(p.inApp.isShowing)
            presenter.dismiss()
            fire(t, p, "x")
            XCTAssertEqual(presenter.shown.count, 2)
        }
    }

    func testScreenTriggerDelayAndEndAt() throws {
        let (t, p, presenter) = setup([campaign(5, "screen_view", "always", screen: "Checkout", delay: 3),
                                       campaign(6, "y", "always", endAt: "2000-01-01T00:00:00Z")])
        try p.screen("Home")
        t.advance(5)
        XCTAssertEqual(presenter.shown.count, 0)
        try p.screen("Checkout")
        t.advance(2)
        XCTAssertEqual(presenter.shown.count, 0)
        t.advance(1)
        XCTAssertEqual(presenter.shown.map { $0.id }, [5])
        presenter.dismiss()
        fire(t, p, "y")
        XCTAssertEqual(presenter.shown.count, 1, "ended campaign")
    }

    func testClickRecordsEventAndCallsAction() {
        var opened: [String] = []
        let (t, p, presenter) = setup([campaign(7, "x", "always")], onAction: { opened.append($0) })
        fire(t, p, "x")
        presenter.dismiss(PulseInAppOutcome(button: presenter.shown[0].content.buttons[0]))
        XCTAssertEqual(opened, ["app://go"])
        fire(t, p, "x")
        presenter.dismiss(PulseInAppOutcome(expired: true))
        p.flushAndWait()
        XCTAssertEqual(t.http.eventNames.filter { $0.hasPrefix("inapp") }, ["inapp_shown", "inapp_clicked", "inapp_shown"])
        let click = t.http.sentEvents.first { $0["name"]?.string == "inapp_clicked" }!["props"]
        XCTAssertEqual(click, .object(["inapp_id": .int(7), "button": .string("Go"), "url": .string("app://go")]))
    }

    func testNotShownWithoutPresenterAndRefreshInterval() {
        let (t, p, presenter) = setup([campaign(8, "x", "once")])
        presenter.available = false
        fire(t, p, "x")
        XCTAssertEqual(presenter.shown.count, 0)
        presenter.available = true
        fire(t, p, "x")
        XCTAssertEqual(presenter.shown.count, 1, "frequency not used up while it could not show")

        XCTAssertEqual(t.http.to("/v1/web/inapp").count, 1)
        t.now += 10 * 60
        p.appWillEnterForeground()
        XCTAssertEqual(t.http.to("/v1/web/inapp").count, 1)
        t.now += 5 * 60
        p.appWillEnterForeground()
        XCTAssertEqual(t.http.to("/v1/web/inapp").count, 2)
    }
}

final class JSONTests: XCTestCase {
    func testRoundTripAndTimes() {
        let v = JSON(any: ["s": "a\"b\n😀", "i": 1, "d": 1.5, "b": false, "n": NSNull(), "l": [1, "x"], "o": ["k": [:] as [String: Any]]] as [String: Any])
        XCTAssertEqual(JSON.decode(v.encoded()), v)
        XCTAssertEqual(v["i"], .int(1))
        XCTAssertEqual(v["b"], .bool(false))
        let opt: String? = nil
        XCTAssertEqual(JSON(any: opt as Any), .null)
        XCTAssertNil(JSON.decode("{"))
        XCTAssertEqual(PulseTime.parse("1970-01-01T00:00:01.5Z")?.timeIntervalSince1970, 1.5)
        XCTAssertEqual(PulseTime.parse("1970-01-01T01:00:00+01:00")?.timeIntervalSince1970, 0)
        XCTAssertNil(PulseTime.parse("yesterday"))
    }
}
