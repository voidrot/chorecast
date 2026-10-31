plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.voidrot.chorecast"
    // AndroidX (core 1.19, Compose 1.12, lifecycle 2.11) requires compiling against API 37.
    // targetSdk stays at 36 so runtime behavior doesn't change.
    compileSdk {
        version = release(37) { minorApiLevel = 2 }
    }

    defaultConfig {
        // NOTE: the applicationId is permanent once published to Google Play.
        applicationId = "dev.voidrot.chorecast"
        minSdk = 26
        targetSdk = 36
        versionName = "0.0.0" // x-release-please-version
        versionCode = versionCodeFor(versionName!!)
    }

    // "play" may use FCM as a push fallback; "foss" is Google-free (UnifiedPush only).
    flavorDimensions += "distribution"
    productFlavors {
        create("play") { dimension = "distribution" }
        create("foss") { dimension = "distribution" }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.tooling.preview)
    implementation(libs.androidx.compose.material3)
    debugImplementation(libs.androidx.compose.ui.tooling)

    testImplementation(libs.junit)
}

/** Derives a monotonic versionCode from semver: major * 1_000_000 + minor * 1_000 + patch. */
fun versionCodeFor(versionName: String): Int {
    val (major, minor, patch) = versionName.substringBefore('-').split('.').map(String::toInt)
    require(minor < 1000 && patch < 1000) { "minor/patch must be < 1000: $versionName" }
    return maxOf(1, major * 1_000_000 + minor * 1_000 + patch)
}
