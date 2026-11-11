import Foundation
import XCTest
@testable import PulseCore

/// Records requests and answers them with `handler`. No network.
final class FakeHTTP: PulseHTTPClient {
    var handler: (PulseHTTPRequest) throws -> PulseHTTPResponse = { _ in PulseHTTPResponse(status: 202, text: "{}") }
    var requests: [PulseHTTPRequest] = []

    func execute(_ request: PulseHTTPRequest) throws -> PulseHTTPResponse {
        requests.append(request)
        return try handler(request)
    }

    func to(_ path: String) -> [PulseHTTPRequest] { requests.filter { $0.url.path.hasSuffix(path) } }

    func body(_ r: PulseHTTPRequest) -> JSON { JSON.decode(r.body ?? Data()) ?? .null }

    var sentEvents: [JSON] { to("/v1/web/events").flatMap { body($0)["events"]?.array ?? [] } }
    var eventNames: [String] { sentEvents.compactMap { $0["name"]?.string } }
}

struct Offline: Error {}

/// Runs tasks at once on the calling thread; scheduled tasks run when `runDue` passes their time.
final class ManualDispatcher: PulseDispatcher {
    final class Task: PulseCancellable {
        let at: Date
        let work: () -> Void
        var cancelled = false
        init(at: Date, work: @escaping () -> Void) { self.at = at; self.work = work }
        func cancel() { cancelled = true }
    }

    private let now: () -> Date
    private var tasks: [Task] = []
    init(now: @escaping () -> Date) { self.now = now }

    func execute(_ work: @escaping () -> Void) { work() }

    func schedule(after seconds: TimeInterval, _ work: @escaping () -> Void) -> PulseCancellable {
        let t = Task(at: now().addingTimeInterval(seconds), work: work)
        tasks.append(t)
        return t
    }

    func runDue() {
        while true {
            let due = tasks.filter { !$0.cancelled && $0.at <= now() }.min { $0.at < $1.at }
            guard let t = due else { return }
            tasks.removeAll { $0 === t }
            t.work()
        }
    }
}

final class TestEnv {
    let storage = InMemoryStorage()
    let http = FakeHTTP()
    var platform: PulsePlatform = .ios
    var device = DeviceInfo(platform: "ios", deviceType: "mobile", os: "iOS", osVersion: "17.4", locale: "en-IN",
                            appVersion: nil, deviceModel: "iPhone15,2", deviceBrand: "Apple")
    var presenter: PulseInAppPresenter?
    var now = Date(timeIntervalSince1970: 1_700_000_000)
    lazy var dispatcher = ManualDispatcher { self.now }
    var logs: [String] = []

    func environment() -> PulseEnvironment {
        PulseEnvironment(
            storage: storage, platform: platform, http: http, dispatcher: dispatcher,
            clock: { self.now }, deviceInfo: { self.device },
            callback: { $0() }, logger: { self.logs.append($0) }, inAppPresenter: presenter)
    }

    func client(_ config: PulseConfig? = nil) -> PulseClient {
        PulseClient(config: config ?? makeConfig(), environment: environment())
    }

    func advance(_ seconds: TimeInterval) {
        now = now.addingTimeInterval(seconds)
        dispatcher.runDue()
    }
}

func makeConfig(siteKey: String = "sk_test", maxBatch: Int = 50, maxQueue: Int = 1000, inApp: Bool = false,
                onInAppAction: ((String) -> Void)? = nil) -> PulseConfig {
    try! PulseConfig(host: "https://pulse.example.com/", siteKey: siteKey, maxBatch: maxBatch, maxQueue: maxQueue,
                     debug: true, inAppMessages: inApp, onInAppAction: onInAppAction, appVersion: "2.3.0")
}

func respond(_ status: Int, _ text: String = "") -> (PulseHTTPRequest) throws -> PulseHTTPResponse {
    { _ in PulseHTTPResponse(status: status, text: text) }
}

let offline: (PulseHTTPRequest) throws -> PulseHTTPResponse = { _ in throw Offline() }
