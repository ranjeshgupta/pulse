import Foundation
import XCTest
@testable import PulseCore

final class ContentTests: XCTestCase {
    let answer = """
    {"content":{
      "home_banner":{"card":{"title":"Hi","body":"Welcome back","image_url":"https://img/x.png",
        "button_text":"Shop","deep_link":"app://shop","data":{"k":"v","n":3}},"variant":"vip"},
      "promo-2":{"html":"<b>Sale</b>"},
      "empty":{}
    }}
    """

    func setup(_ text: String? = nil, onAction: ((String) -> Void)? = nil) -> (TestEnv, PulseClient) {
        let t = TestEnv()
        let body = text ?? answer
        t.http.handler = { $0.url.path.hasSuffix("/v1/web/content") ? PulseHTTPResponse(status: 200, text: body) : PulseHTTPResponse(status: 202) }
        return (t, t.client(makeConfig(onInAppAction: onAction)))
    }

    func testRequestBody() throws {
        let (t, p) = setup()
        p.content.fetchBlocking(["home_banner", "promo-2", "home_banner", "Bad Slot"])
        let r = t.http.to("/v1/web/content")
        XCTAssertEqual(r.count, 1)
        XCTAssertEqual(r[0].method, "POST")
        XCTAssertEqual(r[0].headers["X-Pulse-Site-Key"], "sk_test")
        XCTAssertEqual(r[0].url.absoluteString, "https://pulse.example.com/v1/web/content")
        XCTAssertEqual(t.http.body(r[0]), .object([
            "id": .string(p.currentID), "platform": .string("ios"),
            "slots": .array([.string("home_banner"), .string("promo-2")]),
        ]))
        try p.identify("u1", userHash: "h")
        p.content.fetchBlocking(["home_banner"])
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/content").last!)["user_hash"], .string("h"))
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/content").last!)["id"], .string("u1"))

        // Only invalid ids: nothing is sent.
        let before = t.http.to("/v1/web/content").count
        XCTAssertEqual(p.content.fetchBlocking(["", "UPPER", "-x"]), [:])
        XCTAssertEqual(t.http.to("/v1/web/content").count, before)
    }

    func testSlotIDs() {
        for ok in ["home_banner", "a", "promo-2", "9lives", String(repeating: "a", count: 64)] {
            XCTAssertTrue(PulseContent.isValidSlot(ok), ok)
        }
        for bad in ["", "Home", "-a", "_a", "a b", "a.b", "é", String(repeating: "a", count: 65)] {
            XCTAssertFalse(PulseContent.isValidSlot(bad), bad)
        }
    }

    func testManySlotsAreSplit() {
        let (t, p) = setup()
        p.content.fetchBlocking((0..<120).map { "s\($0)" })
        let sizes = t.http.to("/v1/web/content").map { t.http.body($0)["slots"]?.array?.count ?? 0 }
        XCTAssertEqual(sizes, [50, 50, 20])
    }

    func testDecoding() throws {
        let (_, p) = setup()
        let res = p.content.fetchBlocking(["home_banner", "promo-2", "empty", "missing"])
        XCTAssertEqual(Set(res.keys), ["home_banner", "promo-2"], "empty and missing slots show nothing")
        let c = res["home_banner"]!
        XCTAssertEqual(c.variant, "vip")
        XCTAssertNil(c.html)
        XCTAssertEqual(c.card, PulseSlotCard(title: "Hi", body: "Welcome back", imageURL: "https://img/x.png",
                                            buttonText: "Shop", deepLink: "app://shop", data: ["k": "v", "n": "3"]))
        XCTAssertEqual(res["promo-2"], PulseSlotContent(html: "<b>Sale</b>"))

        // Lenient: wrong types read as missing; Codable round trip.
        let odd = try JSONDecoder().decode(PulseSlotContent.self, from: Data(#"{"card":{"title":5,"body":""},"variant":""}"#.utf8))
        XCTAssertEqual(odd, PulseSlotContent(card: PulseSlotCard()))
        let back = try JSONDecoder().decode(PulseSlotContent.self, from: JSONEncoder().encode(c))
        XCTAssertEqual(back, c)
    }

    func testCacheAndListeners() throws {
        let (t, p) = setup()
        var heard: [Set<String>] = []
        let handle = p.content.addListener { heard.append($0) }
        XCTAssertNil(p.content.cached("home_banner"))
        p.content.load(["home_banner", "missing"])
        XCTAssertEqual(p.content.cached("home_banner")?.variant, "vip")
        XCTAssertNil(p.content.cached("missing"))
        XCTAssertEqual(heard, [["home_banner"]])

        // Fresh: load sends nothing; stale after 15 minutes.
        p.content.load(["home_banner"])
        XCTAssertEqual(t.http.to("/v1/web/content").count, 1)
        t.now += 14 * 60
        p.appWillEnterForeground()
        XCTAssertEqual(t.http.to("/v1/web/content").count, 1)
        t.now += 2 * 60
        p.appWillEnterForeground()
        XCTAssertEqual(t.http.to("/v1/web/content").count, 2)
        XCTAssertEqual(Set(t.http.body(t.http.to("/v1/web/content").last!)["slots"]!.array!.compactMap { $0.string }),
                       ["home_banner", "missing"])
        XCTAssertEqual(heard.count, 1, "unchanged content is not announced again")

        // A slot that disappears from the answer is dropped.
        t.http.handler = respond(200, #"{"content":{}}"#)
        p.content.fetchBlocking(["home_banner"])
        XCTAssertNil(p.content.cached("home_banner"))
        XCTAssertEqual(heard.last, ["home_banner"])
        handle.cancel()
    }

    func testIdentifyAndResetRefetch() throws {
        let (t, p) = setup()
        p.content.fetchBlocking(["home_banner"])
        XCTAssertNotNil(p.content.cached("home_banner"))
        t.http.handler = offline
        try p.identify("u1")
        XCTAssertNil(p.content.cached("home_banner"), "another person's content is dropped")
        XCTAssertEqual(t.http.to("/v1/web/content").count, 2, "known slots are fetched again")

        t.http.handler = respond(200, answer)
        try p.identify("u1", userHash: "h") // same user: content kept until the new answer
        XCTAssertEqual(t.http.to("/v1/web/content").count, 3)
        XCTAssertEqual(p.content.cached("home_banner")?.variant, "vip")
        t.http.handler = offline
        try p.identify("u1", userHash: "h")
        XCTAssertNotNil(p.content.cached("home_banner"))

        p.reset()
        XCTAssertNil(p.content.cached("home_banner"))
        XCTAssertEqual(t.http.to("/v1/web/content").count, 5)
        XCTAssertEqual(t.http.body(t.http.to("/v1/web/content").last!)["id"], .string(p.currentID))
    }

    func testFailureKeepsCache() {
        let (t, p) = setup()
        p.content.fetchBlocking(["home_banner"])
        for h in [offline, respond(500), respond(429), respond(400, #"{"error":"x"}"#), respond(200, "not json")] {
            t.http.handler = h
            let res = p.content.fetchBlocking(["home_banner", "promo-2"])
            XCTAssertEqual(res["home_banner"]?.variant, "vip")
            XCTAssertNil(res["promo-2"])
            XCTAssertEqual(p.content.cached("home_banner")?.variant, "vip")
        }
    }

    func testOptedOutRequestsNothing() {
        let (t, p) = setup()
        p.content.fetchBlocking(["home_banner"])
        p.optOut()
        XCTAssertEqual(p.content.fetchBlocking(["home_banner"])["home_banner"]?.variant, "vip")
        p.content.refresh()
        XCTAssertEqual(t.http.to("/v1/web/content").count, 1)
        p.optIn()
        XCTAssertEqual(t.http.to("/v1/web/content").count, 2)
    }

    func testAsyncAndCompletion() async {
        let (_, p) = setup()
        let res = await p.content.fetch(["home_banner"])
        XCTAssertEqual(res["home_banner"]?.card?.title, "Hi")
        var got: [String: PulseSlotContent]?
        p.content.fetch(["promo-2"]) { got = $0 }
        XCTAssertEqual(got?["promo-2"]?.html, "<b>Sale</b>")
    }

    func testViewedAndClicked() throws {
        var opened: [String] = []
        let (t, p) = setup(onAction: { opened.append($0) })
        let c = p.content.fetchBlocking(["home_banner"])["home_banner"]!
        p.content.viewed("home_banner", c)
        p.content.clicked("home_banner", c)
        p.content.clicked("promo-2", PulseSlotContent(html: "<b>x</b>"), link: "https://shop.example.com/a")
        p.content.clicked("promo-2", PulseSlotContent(html: "<b>x</b>"))
        p.flushAndWait()
        let events = t.http.sentEvents.filter { ($0["name"]?.string ?? "").hasPrefix("app_content_") }
        XCTAssertEqual(events.map { $0["name"]!.string! },
                       ["app_content_viewed", "app_content_clicked", "app_content_clicked", "app_content_clicked"])
        XCTAssertEqual(events[0]["props"], .object(["slot": .string("home_banner"), "variant": .string("vip")]))
        XCTAssertEqual(events[1]["props"], .object(["slot": .string("home_banner"), "variant": .string("vip"), "link": .string("app://shop")]))
        XCTAssertEqual(events[2]["props"], .object(["slot": .string("promo-2"), "variant": .string(""), "link": .string("https://shop.example.com/a")]))
        XCTAssertEqual(events[3]["props"], .object(["slot": .string("promo-2"), "variant": .string("")]))
        XCTAssertEqual(opened, ["app://shop", "https://shop.example.com/a"])
    }
}
