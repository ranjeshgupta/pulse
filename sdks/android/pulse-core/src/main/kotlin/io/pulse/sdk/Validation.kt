package io.pulse.sdk

/** Checks the server applies to what an app sends with a site key. */
object Validation {
    private val eventName = Regex("^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$")
    private val attrKey = Regex("^[a-zA-Z_][a-zA-Z0-9_]{0,63}$")

    /** Event names the site-key API refuses: consent changes and events only a server may send. */
    val reservedEventNames: Set<String> = setOf(
        "unsubscribed", "resubscribed", "tag_added", "tag_removed", "points_changed",
        "list_joined", "list_left", "contact_merged", "contact_merged_into",
    )

    /** Throws [IllegalArgumentException] unless [name] is an event name the server accepts from an app. */
    fun checkEventName(name: String) {
        require(eventName.matches(name)) {
            "event name \"$name\" must start with a letter and have at most 64 letters, digits, _ . : or -"
        }
        require(name !in reservedEventNames) { "event name \"$name\" is reserved and cannot be sent from an app" }
    }

    /** Throws [IllegalArgumentException] unless [userId] can be used as a contact's external id. */
    fun checkUserId(userId: String) {
        require(userId.isNotEmpty()) { "userId is empty" }
        require(userId.toByteArray(Charsets.UTF_8).size <= 255) { "userId is longer than 255 bytes" }
        require(!userId.startsWith("anon-")) { "userId must not start with \"anon-\" (reserved for anonymous ids)" }
    }

    /** Throws [IllegalArgumentException] if an attribute name is one the server would reject. */
    fun checkAttributes(attributes: Map<String, Any?>?) {
        for (k in attributes?.keys ?: return) {
            require(attrKey.matches(k)) {
                "attribute name \"$k\" must start with a letter or _ and have at most 64 letters, digits or _"
            }
        }
    }
}
