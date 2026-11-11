plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
    `maven-publish`
}

android {
    namespace = "io.pulse.sdk.android"
    compileSdk = 34

    defaultConfig {
        minSdk = 21
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }

    publishing {
        singleVariant("release") { withSourcesJar() }
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_11)
    }
}

dependencies {
    api(project(":pulse-core"))
    // No Firebase dependency: PulseFirebase reaches FirebaseMessaging by reflection when the app has it.
}

publishing {
    publications {
        create<MavenPublication>("release") {
            artifactId = "pulse-android"
            afterEvaluate { from(components["release"]) }
        }
    }
}
