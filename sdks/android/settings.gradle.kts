pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
    plugins {
        id("org.jetbrains.kotlin.jvm") version "2.4.21"
        id("org.jetbrains.kotlin.android") version "2.4.21"
        id("com.android.library") version "8.13.2"
    }
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "pulse-sdk-android"

include(":pulse-core")

// pulse-android needs the Android SDK. Include it only when one is available, so
// `gradle :pulse-core:test` works on machines (and in Docker) without it.
val androidSdkAvailable = System.getenv("ANDROID_HOME") != null ||
    System.getenv("ANDROID_SDK_ROOT") != null ||
    file("local.properties").let { it.exists() && it.readText().contains("sdk.dir") }
if (androidSdkAvailable) {
    include(":pulse-android")
} else {
    logger.lifecycle("pulse: no Android SDK found (ANDROID_HOME / local.properties sdk.dir); building pulse-core only")
}
