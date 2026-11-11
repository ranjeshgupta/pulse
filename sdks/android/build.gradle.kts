// Plugin versions are in settings.gradle.kts (pluginManagement). The Kotlin plugin is not loaded
// here: pulse-android must load it next to the Android Gradle plugin, and pulse-core must build
// without the Android Gradle plugin.

allprojects {
    group = "io.pulse"
    version = "0.1.0"
}
