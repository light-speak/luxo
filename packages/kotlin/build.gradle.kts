import org.gradle.api.tasks.wrapper.Wrapper
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
    `java-library`
    `maven-publish`
}

group = "com.luxo"
version = "0.1.0"

base {
    archivesName.set("luxo-client")
}

java {
    sourceCompatibility = JavaVersion.VERSION_1_8
    targetCompatibility = JavaVersion.VERSION_1_8
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_1_8)
        allWarningsAsErrors.set(true)
    }
}

repositories {
    mavenCentral()
}

publishing {
    publications {
        create<MavenPublication>("mavenJava") {
            from(components["java"])
        }
    }
}

val luxoAnalyzerRuntime = configurations.create("luxoAnalyzerRuntime")

dependencies {
    api("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    api("com.squareup.okhttp3:okhttp:4.12.0")

    // The compiler is isolated to the build-time field analyzer and is not
    // published as an SDK runtime dependency.
    compileOnly(libs.kotlin.compiler)
    testRuntimeOnly(libs.kotlin.compiler)
    luxoAnalyzerRuntime(libs.kotlin.compiler)

    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
}

tasks.test {
    useJUnitPlatform()
}

tasks.register<JavaExec>("benchmarkWebSocketIDs") {
    group = "verification"
    description = "Compare unsigned WebSocket metadata with the previous signed implementation"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.WebSocketIDBenchmark")
}

tasks.register<JavaExec>("benchmarkWebSocketSubscriptions") {
    group = "verification"
    description = "Compare UInt64 subscription dispatch and lifecycle with the frozen API-keyed transport"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.WebSocketSubscriptionBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkWebSocketBinarySend") {
    group = "verification"
    description = "Compare immutable binary send snapshots without the redundant vararg array copy"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.WebSocketBinarySendBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkWebSocketUnary") {
    group = "verification"
    description = "Measure one unary transport implementation in an isolated JVM"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.WebSocketIsolatedBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
    args(providers.gradleProperty("luxo.unaryMode").orElse("BINARY").get(),
        providers.gradleProperty("luxo.unaryPayloadBytes").orElse("0").get())
}

tasks.register<JavaExec>("benchmarkWebSocketTerminals") {
    group = "verification"
    description = "Compare terminal-aware subscriptions with the frozen UInt64 transport"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.WebSocketTerminalBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

// Supply the unmodified pre-change transport explicitly; never keep a second runtime copy in source.
providers.gradleProperty("luxo.decoderBaseline").orNull?.let { snapshot ->
    val baseline = file(snapshot)
    val generated = layout.projectDirectory.dir(".tmp/websocket-decoding-benchmark")
    val template = layout.projectDirectory.file("benchmark/WebSocketDecodingBenchmark.kt.txt")
    val prepare = tasks.register("prepareWebSocketDecodingBenchmark") {
        inputs.file(baseline)
        inputs.file(template)
        outputs.dir(generated)
        doLast {
            val original = baseline.readText()
            val declaration = "override suspend fun subscribe("
            check(original.windowed(declaration.length).count { it == declaration } == 1) {
                "Expected the original non-generic subscribe declaration in the baseline"
            }
            val directory = generated.asFile.apply { mkdirs() }
            directory.resolve("BeforeDecodingWebSocket.kt").writeText(original
                .replace("LuxoWebSocket", "BeforeDecodingWebSocket")
                .replace(declaration, "suspend fun subscribeRaw("))
            directory.resolve("WebSocketDecodingBenchmark.kt").writeText(template.asFile.readText())
        }
    }
    kotlin.sourceSets["test"].kotlin.srcDir(generated)
    tasks.named("compileTestKotlin") { dependsOn(prepare) }
    tasks.register<JavaExec>("benchmarkWebSocketDecoding") {
        group = "verification"
        description = "Compare decoder isolation with an explicit frozen transport snapshot"
        classpath = sourceSets["test"].runtimeClasspath
        mainClass.set("com.luxo.client.WebSocketDecodingBenchmark")
        jvmArgs("-Xms512m", "-Xmx512m")
        providers.gradleProperty("luxo.benchmarkJfr").orNull?.let { recording ->
            val settings = providers.gradleProperty("luxo.benchmarkJfrSettings").orElse("profile").get()
            jvmArgs("-XX:StartFlightRecording=settings=$settings,filename=${file(recording)},dumponexit=true")
        }
    }
}

tasks.register<JavaExec>("benchmarkHTTPProtocol") {
    group = "verification"
    description = "Compare HTTP JSON decoding, headers, byte-length validation and strict error decoding"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.HttpProtocolBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkCodecMetadata") {
    group = "verification"
    description = "Compare bounded metadata decoding with frozen pre-bounds readers"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.CodecMetadataBenchmark")
}

tasks.register<JavaExec>("benchmarkUUID") {
    group = "verification"
    description = "Compare canonical UUID parsing with the frozen permissive parser"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.UuidBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkCodecText") {
    group = "verification"
    description = "Compare strict UTF-8, direct row text reading and response completion"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.CodecTextBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkJSONInputs") {
    group = "verification"
    description = "Compare generated JSON input projection with serializer discovery"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.JSONInputBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkJSONNumbers") {
    group = "verification"
    description = "Compare strict JSON numeric conversion with the previous primitive readers"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.JsonNumbersBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

tasks.register<JavaExec>("benchmarkJSONKinds") {
    group = "verification"
    description = "Compare strict JSON string and boolean readers with previous primitive access"
    classpath = sourceSets["test"].runtimeClasspath
    mainClass.set("com.luxo.client.JsonKindsBenchmark")
    jvmArgs("-Xms512m", "-Xmx512m")
}

// Frozen sources are opt-in test inputs, never a second implementation in the published SDK.
providers.gradleProperty("luxo.unicodeBaseline").orNull?.let { snapshot ->
    val baseline = file(snapshot)
    val generated = layout.projectDirectory.dir(".tmp/unicode-benchmark")
    val template = layout.projectDirectory.file("benchmark/UnicodeBenchmark.kt.txt")
    val names = listOf("Codec.kt", "JsonProtocol.kt")
    val prepare = tasks.register("prepareUnicodeBenchmark") {
        inputs.files(names.map { baseline.resolve(it) })
        inputs.file(template)
        outputs.dir(generated)
        doLast {
            val directory = generated.asFile.apply { mkdirs() }
            for (name in names) {
                val original = baseline.resolve(name).readText()
                check(original.startsWith("package com.luxo.client\n")) { "Unexpected baseline package" }
                directory.resolve(name).writeText(original.replaceFirst("package com.luxo.client",
                    "package com.luxo.client.unicodebaseline\n\nimport com.luxo.client.*"))
            }
            directory.resolve("UnicodeBenchmark.kt").writeText(template.asFile.readText())
        }
    }
    kotlin.sourceSets["test"].kotlin.srcDir(generated)
    tasks.named("compileTestKotlin") { dependsOn(prepare) }
    tasks.register<JavaExec>("benchmarkUnicode") {
        group = "verification"
        description = "Compare strict Unicode boundaries with explicitly frozen pre-check sources"
        classpath = sourceSets["test"].runtimeClasspath
        mainClass.set("com.luxo.client.UnicodeBenchmark")
        jvmArgs("-Xms512m", "-Xmx512m")
    }
}

tasks.wrapper {
    gradleVersion = "9.7.1"
    distributionType = Wrapper.DistributionType.BIN
    distributionSha256Sum = "acd53f1edaf02f1a8ff99879f8a34b302661a057d9b063ae9e35b552f804d20a"
}

// --- Luxo codegen tasks (for user projects) ---
// Usage:
//   ./gradlew luxoGenerate -Pluxo.endpoint=http://localhost:4000/luvia -Pluxo.key=YOUR_KEY
//   ./gradlew luxoAnalyze

tasks.register<JavaExec>("luxoGenerate") {
    group = "luxo"
    description = "Generate typed client from Luxo schema introspection"
    classpath = sourceSets["main"].runtimeClasspath
    mainClass.set("com.luxo.client.LuxoCodegenCli")
    environment(
        "LUXO_INTROSPECTION_KEY",
        System.getenv("LUXO_INTROSPECTION_KEY") ?: project.findProperty("luxo.key")?.toString().orEmpty(),
    )
    args(
        project.findProperty("luxo.endpoint")?.toString() ?: "http://localhost:4000/luvia",
        project.findProperty("luxo.outDir")?.toString() ?: "src/main/kotlin/com/luxo/generated",
        project.findProperty("luxo.package")?.toString() ?: "com.luxo.generated",
        project.findProperty("luxo.schemaFile")?.toString()
            ?: layout.buildDirectory.file("luxo/luxo.schema.json").get().asFile.absolutePath,
    )
}

tasks.register<JavaExec>("luxoAnalyze") {
    group = "luxo"
    description = "Analyze source code for field access patterns and generate SelectHints"
    classpath = sourceSets["main"].runtimeClasspath + luxoAnalyzerRuntime
    mainClass.set("com.luxo.client.SelectAnalyzerCli")
    args(
        project.findProperty("luxo.srcDir")?.toString() ?: "src/main/kotlin",
        project.findProperty("luxo.hintsFile")?.toString() ?: "src/main/kotlin/com/luxo/generated/SelectHints.kt",
        project.findProperty("luxo.package")?.toString() ?: "com.luxo.generated",
        project.findProperty("luxo.schemaFile")?.toString()
            ?: layout.buildDirectory.file("luxo/luxo.schema.json").get().asFile.absolutePath,
    )
}
