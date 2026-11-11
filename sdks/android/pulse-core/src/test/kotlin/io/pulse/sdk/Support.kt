package io.pulse.sdk

import java.io.IOException

/** Records requests and answers them with [handler] (default 202 for events, 204 otherwise). No network. */
class FakeHttp(var handler: (HttpRequest) -> HttpResponse = { HttpResponse(202, "{}") }) : PulseHttpClient {
    val requests = ArrayList<HttpRequest>()

    override fun execute(request: HttpRequest): HttpResponse {
        requests.add(request)
        return handler(request)
    }

    fun to(path: String) = requests.filter { it.url.substringBefore('?').endsWith(path) }

    @Suppress("UNCHECKED_CAST")
    fun sentEvents(): List<Map<String, Any?>> =
        to("/v1/web/events").flatMap { (Json.decode(it.body!!) as Map<String, Any?>)["events"] as List<Map<String, Any?>> }

    fun eventNames() = sentEvents().map { it["name"] }
}

/** Runs tasks at once on the calling thread; scheduled tasks run when [advance] passes their time. */
class ManualDispatcher(private val now: () -> Long) : Dispatcher {
    private class Task(val at: Long, val run: Runnable, var cancelled: Boolean = false)

    private val tasks = ArrayList<Task>()

    override fun execute(task: Runnable) = task.run()

    override fun schedule(delayMillis: Long, task: Runnable): Cancellable {
        val t = Task(now() + delayMillis, task)
        tasks.add(t)
        return Cancellable { t.cancelled = true }
    }

    /** Runs every scheduled task due by now (including ones scheduled while running). */
    fun runDue() {
        while (true) {
            val t = tasks.filter { !it.cancelled && it.at <= now() }.minByOrNull { it.at } ?: return
            tasks.remove(t)
            t.run.run()
        }
    }

    val scheduled: Int get() = tasks.count { !it.cancelled }
}

class TestEnv(
    val storage: InMemoryStorage = InMemoryStorage(),
    val http: FakeHttp = FakeHttp(),
    val platform: PulsePlatform = PulsePlatform.ANDROID,
    var device: DeviceInfo = DeviceInfo("android", "mobile", "Android", "14", "en-IN", null, "Pixel 8", "Google"),
    val presenter: InAppPresenter? = null,
) {
    var now = 1_700_000_000_000L
    val dispatcher = ManualDispatcher { now }
    val logs = ArrayList<String>()

    fun env() = PulseEnvironment(
        storage = storage,
        platform = platform,
        http = http,
        dispatcher = dispatcher,
        clock = { now },
        deviceInfo = { device },
        logger = PulseLogger { logs.add(it) },
        inAppPresenter = presenter,
    )

    fun client(config: PulseConfig = config()) = PulseClient(config, env())

    fun advance(ms: Long) {
        now += ms
        dispatcher.runDue()
    }
}

fun config(
    siteKey: String = "sk_test",
    maxBatch: Int = 50,
    maxQueue: Int = 1000,
    inApp: Boolean = false,
    onInAppAction: ((String) -> Unit)? = null,
    appVersion: String? = "2.3.0",
) = PulseConfig(
    host = "https://pulse.example.com/",
    siteKey = siteKey,
    maxBatch = maxBatch,
    maxQueue = maxQueue,
    inAppMessages = inApp,
    onInAppAction = onInAppAction,
    appVersion = appVersion,
    debug = true,
)

fun offline(): (HttpRequest) -> HttpResponse = { throw IOException("offline") }

@Suppress("UNCHECKED_CAST")
fun Map<String, Any?>.props() = this["props"] as Map<String, Any?>
