// Kotlin client for the Chorecast API, generated at build time from api/openapi-3.0.json
// (written by `mise run gen:openapi`). Nothing generated is committed, so it can't drift.
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    alias(libs.plugins.openapi.generator)
}

val generatedDir = layout.buildDirectory.dir("generated/openapi")

openApiGenerate {
    generatorName.set("kotlin")
    inputSpec.set(rootProject.layout.projectDirectory.file("../api/openapi-3.0.json").asFile.path)
    outputDir.set(generatedDir)
    packageName.set("dev.voidrot.chorecast.api")
    library.set("jvm-ktor")
    configOptions.set(
        mapOf(
            "serializationLibrary" to "kotlinx_serialization",
            "enumPropertyNaming" to "UPPERCASE",
            "omitGradleWrapper" to "true",
            "sourceFolder" to "src/main/kotlin",
        ),
    )
}

sourceSets.main {
    kotlin.srcDir(generatedDir.map { it.dir("src/main/kotlin") })
}

tasks.named("compileKotlin") { dependsOn("openApiGenerate") }

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions { jvmTarget.set(JvmTarget.JVM_17) }
}

dependencies {
    api(libs.ktor.client.core)
    api(libs.kotlinx.serialization.json)
    // Exposed so callers can configure JSON (the generated client registers no converter itself).
    api(libs.ktor.client.content.negotiation)
    api(libs.ktor.serialization.kotlinx.json)
}
