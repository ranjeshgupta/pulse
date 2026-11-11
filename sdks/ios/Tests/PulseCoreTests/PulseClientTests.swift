import Foundation
import XCTest
@testable import PulseCore

final class PulseClientTests: XCTestCase {
    func testAnonymousIDPersistedAndNamespacedBySiteKey() {
        let t = TestEnv()
        let a = t.client(makeConfig(siteKey: "sk_a"))
        XCTAssertTrue(a.currentID.hasPrefix("anon-"))
        XCTAssertEqual(a.currentID.count, 29)
        XCTAssertTrue(a.currentID.dropFirst(5).allSatisfy { "0123456789abcdef".contains($0) })
        XCTAssertEqual(t.storage.read("pulse.sk_a.aid"), a.currentID)
        XCTAssertNil(t.storage.read("pulse.aid"))
        XCTAssertEqual(t.client(makeConfig(siteKey: "sk_a")).currentID, a.currentID)
        let b = t.client(makeConfig(siteKey: "sk_b"))
        XCTAssertNotEqual(b.currentID, a.currentID)
        XCTAssertEqual(t.storage.read("pulse.sk_b.aid"), b.currentID)

        let k = PulseStorageKeys(siteKey: "sk_1")
        XCTAssertEqual([k.anonID, k.userID, k.userHash, k.queue, k.push, k.sessionID, k.sessionAt, k.optOut],
                       ["pulse.sk_1.aid", "pulse.sk_1.uid", "pulse.sk_1.uh", "pulse.sk_1.queue", "pulse.sk_1.push",
                        "pulse.sk_1.sid", "pulse.sk_1.sla", "pulse.sk_1.optout"])
    }

    func testTrackSendsEventWithSiteKey() throws {
        let t = TestEnv()
        let p = t.client()
        try p.track("order_completed", properties: ["total": 49.9, "n": 2, "ok": true, "at": Date(timeIntervalSince1970: 0),
                                                   "tags": ["a", "b"], "none": NSNull()])
        p.flush()
        let req = t.http.to("/v1/web/events")[0]
        XCTAssertEqual(req.url.absoluteString, "https://pulse.example.com/v1/web/events")
        XCTAssertEqual(req.headers["X-Pulse-Site-Key"], "sk_test")
        XCTAssertEqual(req.headers["Content-Type"], "application/json")
        XCTAssertEqual(t.http.eventNames, ["session_start", "order_completed"])
        let ev = t.http.sentEvents[1]
        XCTAssertEqual(ev["external_id"]?.string, p.currentID)
        XCTAssertEqual(ev["props"]?["total"], .double(49.9))
        XCTAssertEqual(ev["props"]?["n"], .int(2))
        XCTAssertEqual(ev["props"]?["ok"], .bool(true))
        XCTAssertEqual(ev["props"]?["at"], .string("1970-01-01T00:00:00.000Z"))
        XCTAssertEqual(ev["props"]?["tags"], .array([.string("a"), .string("b")]))
        XCTAssertEqual(ev["id"]?.string?.count, 32)
        XCTAssertEqual(p.pendingEvents, 0)
    }

    func testIdentifyMergesAnonymousIDOnce() throws {
        let t = TestEnv()
        let p = t.client()
        let anon = p.currentID
        try p.identify("u1", attributes: ["plan": "pro"], email: "a@example.com", userHash: "h1")
        let ev = t.http.sentEvents.first { $0["name"]?.string == "identify" }!
        XCTAssertEqual(ev["external_id"]?.string, "u1")
        XCTAssertEqual(ev["props"]?["anonymous_id"]?.string, anon)
        XCTAssertEqual(ev["props"]?["user_hash"]?.string, "h1")
        XCTAssertEqual(ev["props"]?["platform"]?.string, "ios")
        XCTAssertEqual(ev["attrs"]?["plan"]?.string, "pro")
        XCTAssertEqual(ev["email"]?.string, "a@example.com")
        XCTAssertEqual(t.storage.read("pulse.sk_test.uid"), "u1")
        XCTAssertEqual(t.storage.read("pulse.sk_test.uh"), "h1")

        try p.identify("u1", userHash: "h1")
        let second = t.http.sentEvents.filter { $0["name"]?.string == "identify" }.last!
        XCTAssertNil(second["props"]?["anonymous_id"])
    }

    func testResetStartsNewAnonymousIDAndSession() throws {
        let t = TestEnv()
        let p = t.client()
        try p.identify("u1")
        let sid = p.sessionID
        XCTAssertNotNil(sid)
        p.reset()
        XCTAssertFalse(p.isIdentified)
        XCTAssertTrue(p.currentID.hasPrefix("anon-"))
        XCTAssertNil(p.sessionID)
        XCTAssertNil(t.storage.read("pulse.sk_test.uid"))
        try p.track("after")
        XCTAssertNotEqual(p.sessionID, sid)
    }

    func testValidationErrors() throws {
        let p = TestEnv().client()
        for bad in ["1bad", "has space", String(repeating: "a", count: 65), "", "é"] {
            XCTAssertThrowsError(try p.track(bad), bad)
        }
        XCTAssertNoThrow(try p.track(String(repeating: "a", count: 64)))
        XCTAssertNoThrow(try p.track("ok.name:with-dash_1"))
        for r in ["unsubscribed", "resubscribed", "tag_added", "tag_removed", "points_changed", "list_joined", "list_left"] {
            XCTAssertThrowsError(try p.track(r), r)
        }
        XCTAssertThrowsError(try p.identify(""))
        XCTAssertThrowsError(try p.identify("anon-1"))
        XCTAssertThrowsError(try p.identify(String(repeating: "é", count: 128)))
        XCTAssertNoThrow(try p.identify(String(repeating: "é", count: 127) + "a"))
        XCTAssertThrowsError(try p.identify("u", attributes: ["bad-key": 1]))
        XCTAssertThrowsError(try p.identify("u", attributes: ["1x": 1]))
        XCTAssertNoThrow(try p.identify("u", attributes: ["_ok": 1]))
        XCTAssertThrowsError(try p.screen(""))
        XCTAssertThrowsError(try PulseConfig(host: "https://h", siteKey: "sk", maxBatch: 51))
        XCTAssertThrowsError(try PulseConfig(host: "https://h", siteKey: "sk", maxBatch: 0))
        XCTAssertThrowsError(try PulseConfig(host: "/", siteKey: "sk"))
        XCTAssertThrowsError(try PulseConfig(host: "https://h", siteKey: ""))
    }

    func testBatchesAtMostMaxBatch() throws {
        let t = TestEnv()
        t.http.handler = offline
        let p = t.client(makeConfig(maxBatch: 3))
        for i in 0..<7 { try p.track("e\(i)") }
        XCTAssertEqual(p.pendingEvents, 8)
        t.http.requests.removeAll()
        t.http.handler = respond(202)
        p.flushAndWait()
        XCTAssertEqual(t.http.to("/v1/web/events").map { t.http.body($0)["events"]?.array?.count ?? 0 }, [3, 3, 2])
        XCTAssertEqual(p.pendingEvents, 0)
    }

    func testFlushesWhenMaxBatchWaitingAndOnTimer() throws {
        let t = TestEnv()
        let p = t.client(makeConfig(maxBatch: 2))
        try p.track("a")
        XCTAssertEqual(t.http.to("/v1/web/events").count, 1)

        let t2 = TestEnv()
        let q = t2.client()
        try q.track("a")
        XCTAssertEqual(t2.http.requests.count, 0)
        t2.advance(10)
        XCTAssertEqual(t2.http.eventNames, ["session_start", "a"])
        XCTAssertEqual(q.pendingEvents, 0)
    }

    func testQueueSurvivesRestartAndMaxQueue() throws {
        let t = TestEnv()
        t.http.handler = offline
        let p = t.client()
        try p.track("offline_1")
        try p.track("offline_2")
        p.dispose()
        let again = t.client()
        XCTAssertEqual(again.pendingEvents, 3)
        t.http.handler = respond(202)
        again.flushAndWait()
        XCTAssertEqual(t.http.eventNames, ["session_start", "offline_1", "offline_2"])

        let t2 = TestEnv()
        t2.http.handler = offline
        let q = t2.client(makeConfig(maxBatch: 5, maxQueue: 5))
        for i in 0..<10 { try q.track("e\(i)") }
        XCTAssertEqual(q.pendingEvents, 5)
        t2.http.handler = respond(202)
        q.flushAndWait()
        XCTAssertEqual(Array(t2.http.eventNames.suffix(5)), ["e5", "e6", "e7", "e8", "e9"])
    }

    func testRetriesWithBackoffOn429And5xxAndNetwork() throws {
        for failure in [respond(429), respond(503), offline] {
            let t = TestEnv()
            t.http.handler = failure
            let p = t.client()
            try p.track("a")
            p.flushAndWait()
            XCTAssertEqual(p.pendingEvents, 2)
            let sent = t.http.requests.count
            t.http.handler = respond(202)
            t.advance(10)
            XCTAssertEqual(t.http.requests.count, sent + 1)
            XCTAssertEqual(p.pendingEvents, 0)
        }
    }

    func testBackoffDoublesToFiveMinutes() throws {
        XCTAssertEqual((1...10).map { Backoff.delaySeconds($0) }, [2, 4, 8, 16, 32, 64, 128, 256, 300, 300])
        let t = TestEnv()
        t.http.handler = respond(500)
        let p = t.client()
        try p.track("a")
        for _ in 0..<7 { p.flushAndWait() } // next retry in 128 s
        let n = t.http.requests.count
        t.advance(10)
        t.advance(10)
        XCTAssertEqual(t.http.requests.count, n, "timer waits during backoff")
        p.flushAndWait()
        XCTAssertEqual(t.http.requests.count, n + 1, "an explicit flush always tries")
    }

    func testDrops400And403() throws {
        for code in [400, 403] {
            let t = TestEnv()
            t.http.handler = respond(code, "{\"error\":\"x\"}")
            let p = t.client()
            try p.track("a")
            p.flushAndWait()
            XCTAssertEqual(p.pendingEvents, 0)
            XCTAssertTrue(t.logs.contains { $0.contains("dropping") })
        }
    }

    func testOptOutClearsQueueAndBlocksSending() throws {
        let t = TestEnv()
        t.http.handler = offline
        let p = t.client()
        try p.track("a")
        p.registerPushToken("tok")
        XCTAssertEqual(p.pendingEvents, 2)
        p.optOut()
        XCTAssertTrue(p.isOptedOut)
        XCTAssertEqual(p.pendingEvents, 0)
        XCTAssertEqual(t.storage.read("pulse.sk_test.optout"), "1")

        t.http.handler = respond(202, "{}")
        t.http.requests.removeAll()
        try p.track("b")
        try p.screen("Home")
        try p.identify("u1")
        p.registerPushToken("tok2")
        XCTAssertFalse(p.trackPushClicked(userInfo: ["pulse_track": "https://pulse.example.com/t/p/abc"]))
        XCTAssertEqual(try p.inbox.listBlocking(), .empty)
        p.flushAndWait()
        t.advance(60)
        XCTAssertEqual(p.pendingEvents, 0)
        XCTAssertEqual(t.http.requests.count, 0)
        XCTAssertEqual(p.currentID, "u1")

        let again = t.client()
        XCTAssertTrue(again.isOptedOut)
        try again.track("c")
        XCTAssertEqual(again.pendingEvents, 0)
        again.optIn()
        XCTAssertFalse(again.isOptedOut)
        XCTAssertNil(t.storage.read("pulse.sk_test.optout"))
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/devices").last!)["token"]?.string, "tok2")
        try again.track("d")
        again.flushAndWait()
        XCTAssertTrue(t.http.eventNames.contains("d"))
    }

    func testPushTokenRegistration() throws {
        let t = TestEnv()
        let p = t.client()
        var ok: Bool?
        p.registerPushToken("fcm-token") { ok = $0 }
        XCTAssertEqual(ok, true)
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/devices").last!),
                       .object(["external_id": .string(p.currentID), "token": .string("fcm-token"), "platform": .string("ios")]))
        try p.identify("u1", userHash: "h")
        var body = t.http.body(t.http.to("/v1/web/devices").last!)
        XCTAssertEqual(body["external_id"]?.string, "u1")
        XCTAssertEqual(body["user_hash"]?.string, "h")
        p.reset()
        body = t.http.body(t.http.to("/v1/web/devices").last!)
        XCTAssertTrue(body["external_id"]?.string?.hasPrefix("anon-") ?? false)
        XCTAssertEqual(t.http.to("/v1/web/devices").count, 3)

        p.registerPushToken("android-tok", platform: .android)
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/devices").last!)["platform"]?.string, "android")

        t.http.handler = respond(400, "bad")
        p.registerPushToken("tok3") { ok = $0 }
        XCTAssertEqual(ok, false)
    }
}
