package io.pulse.sdk

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URI
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executor
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.ScheduledThreadPoolExecutor
import java.util.concurrent.TimeUnit

/**
 * Key-value storage for the SDK's state. Keys are already namespaced by site key
 * (`pulse.<siteKey>.aid`). The Android module uses SharedPreferences.
 */
interface PulseStorage {
    fun read(key: String): String?
    fun write(key: String, value: String)
    fun delete(key: String)
}

/** Storage in memory, for tests. */
class InMemoryStorage : PulseStorage {
    val values: MutableMap<String, String> = ConcurrentHashMap()
    override fun read(key: String): String? = values[key]
    override fun write(key: String, value: String) {
        values[key] = value
    }
    override fun delete(key: String) {
        values.remove(key)
    }
}

/** Storage key names, namespaced by the workspace's site key so two workspaces never share state. */
class StorageKeys(siteKey: String) {
    private val p = "pulse.$siteKey."
    val anonId = p + "aid"
    val userId = p + "uid"
    val userHash = p + "uh"
    val queue = p + "queue"
    val push = p + "push"
    val sessionId = p + "sid"
    /** Last activity, ms since the epoch. */
    val sessionAt = p + "sla"
    val optOut = p + "optout"
    /** In-app frequency state (`once` and `daily`). */
    val inApp = p + "inapp"
    /** Push click/dismiss reports waiting to be sent. */
    val pushTracks = p + "ptrack"
}

/** One HTTP request. [body] is JSON, or null for none. */
class HttpRequest(
    val method: String,
    val url: String,
    val headers: Map<String, String>,
    val body: String?,
    val timeoutMillis: Long,
)

/** One HTTP response. */
class HttpResponse(val status: Int, val body: String = "")

/** Sends HTTP requests. It blocks; the SDK calls it on its own worker thread. Throws [IOException] on network errors. */
fun interface PulseHttpClient {
    @Throws(IOException::class)
    fun execute(request: HttpRequest): HttpResponse
}

/** The default [PulseHttpClient], on `java.net.HttpURLConnection`. */
class UrlConnectionHttpClient : PulseHttpClient {
    override fun execute(request: HttpRequest): HttpResponse {
        val conn = URI(request.url).toURL().openConnection() as HttpURLConnection
        try {
            conn.requestMethod = request.method
            conn.connectTimeout = request.timeoutMillis.toInt()
            conn.readTimeout = request.timeoutMillis.toInt()
            conn.instanceFollowRedirects = false
            conn.useCaches = false
            for ((k, v) in request.headers) conn.setRequestProperty(k, v)
            if (request.body != null) {
                val bytes = request.body.toByteArray(Charsets.UTF_8)
                conn.doOutput = true
                conn.setFixedLengthStreamingMode(bytes.size)
                conn.outputStream.use { it.write(bytes) }
            } else if (request.method == "POST") {
                conn.doOutput = true
                conn.setFixedLengthStreamingMode(0)
                conn.outputStream.close()
            }
            val status = conn.responseCode
            val stream = if (status >= 400) conn.errorStream else conn.inputStream
            return HttpResponse(status, stream?.use { readAll(it) } ?: "")
        } finally {
            conn.disconnect()
        }
    }

    private fun readAll(input: InputStream): String {
        val out = ByteArrayOutputStream()
        val buf = ByteArray(8192)
        while (true) {
            val n = input.read(buf)
            if (n < 0) break
            out.write(buf, 0, n)
        }
        return out.toString("UTF-8")
    }
}

/** A scheduled task that can be cancelled. */
fun interface Cancellable {
    fun cancel()
}

/** Runs the SDK's background work (HTTP, delays) one task at a time. */
interface Dispatcher {
    fun execute(task: Runnable)
    fun schedule(delayMillis: Long, task: Runnable): Cancellable
    fun shutdown() {}
}

/** The default [Dispatcher]: one daemon worker thread. */
class ExecutorDispatcher : Dispatcher {
    private val executor = ScheduledThreadPoolExecutor(1) { r ->
        Thread(r, "pulse-worker").apply { isDaemon = true }
    }.apply { removeOnCancelPolicy = true }

    override fun execute(task: Runnable) {
        if (!executor.isShutdown) executor.execute(task)
    }

    override fun schedule(delayMillis: Long, task: Runnable): Cancellable {
        if (executor.isShutdown) return Cancellable { }
        val f: ScheduledFuture<*> = executor.schedule(task, delayMillis, TimeUnit.MILLISECONDS)
        return Cancellable { f.cancel(false) }
    }

    override fun shutdown() {
        executor.shutdown()
    }
}

/** Receives the SDK's log lines. */
fun interface PulseLogger {
    fun log(message: String)
}

/**
 * What the platform gives the core: storage, HTTP, a worker, a clock and device details.
 * The Android module builds one with SharedPreferences, the main thread for callbacks, and the
 * device's `Build` and `Configuration`. Tests pass fakes.
 */
class PulseEnvironment(
    val storage: PulseStorage,
    val platform: PulsePlatform,
    val http: PulseHttpClient = UrlConnectionHttpClient(),
    val dispatcher: Dispatcher = ExecutorDispatcher(),
    /** Milliseconds since the epoch. */
    val clock: () -> Long = System::currentTimeMillis,
    val deviceInfo: () -> DeviceInfo = { DeviceInfo(platform = platform.wireName) },
    /** Where callbacks (inbox results, unread count, in-app actions) run. Default: the worker thread. */
    val callbackExecutor: Executor = Executor { it.run() },
    val logger: PulseLogger = PulseLogger { System.err.println(it) },
    /** Shows in-app messages; null shows none. */
    val inAppPresenter: InAppPresenter? = null,
    /** Random source for ids and backoff jitter. */
    val random: java.util.Random = java.security.SecureRandom(),
)
