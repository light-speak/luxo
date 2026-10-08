plugins {
    kotlin("jvm") version "2.4.20"
    id("com.luxo.select")
}

repositories {
    mavenCentral()
}

dependencies {
    implementation("com.luxo:luxo-client:1.0.0-beta.1")
}

layout.buildDirectory = layout.projectDirectory.dir(".tmp/build")

kotlin {
    compilerOptions { allWarningsAsErrors.set(true) }
    sourceSets.named("main") {
        kotlin.srcDir(layout.projectDirectory.dir(".tmp/generated"))
    }
}

luxo {
    outputDirectory.set(layout.projectDirectory.dir(".tmp/generated/com/luxo/generated"))
}

val verifyProtocol = tasks.register<JavaExec>("verifyProtocol") {
    group = "verification"
    description = "Exercise generated int64 clients over real HTTP JSON and binary requests"
    classpath = sourceSets["main"].runtimeClasspath
    mainClass.set("app.ProtocolConsumerKt")
    args(layout.projectDirectory.file("../../pkg/lux/codec/testdata/protocol-v1.json").asFile.absolutePath)
}

tasks.check {
    dependsOn(verifyProtocol)
}

fun writeBenchmarkHarness(directory: File, harness: File) {
    val shared = harness.readText().substringAfter("internal object PairedJvmBenchmark {", "")
    check(shared.isNotEmpty()) { "Paired JVM harness not found" }
    directory.resolve("PairedJvmBenchmark.kt").writeText("""
        package app
        import com.sun.management.ThreadMXBean
        import java.lang.management.ManagementFactory
        import java.util.Locale
        import java.util.function.LongSupplier
        internal object PairedJvmBenchmark {
    """.trimIndent() + shared)
}

providers.gradleProperty("luxo.enumBenchmark").orNull?.let {
    check(!providers.gradleProperty("luxo.projectionBaseline").isPresent) { "Run enum and projection benchmarks separately" }
    val generated = layout.projectDirectory.dir(".tmp/enum-benchmark")
    val template = file("benchmark/EnumBenchmark.kt.txt")
    val harness = file("../../packages/kotlin/src/test/kotlin/com/luxo/client/WebSocketIDBenchmark.kt")
    val prepare = tasks.register("prepareEnumBenchmark") {
        inputs.files(template, harness)
        outputs.dir(generated)
        doLast {
            val directory = generated.asFile.apply { mkdirs() }
            writeBenchmarkHarness(directory, harness)
            directory.resolve("EnumBenchmark.kt").writeText(template.readText())
        }
    }
    kotlin.sourceSets["main"].kotlin.srcDir(generated)
    tasks.named("compileKotlin") { dependsOn(prepare) }
    tasks.register<JavaExec>("benchmarkEnums") {
        group = "verification"
        description = "Compare generated known/unknown enum boundaries with strict string readers"
        classpath = sourceSets["main"].runtimeClasspath
        mainClass.set("app.EnumBenchmark")
        jvmArgs("-Xms512m", "-Xmx512m")
    }
}

// Capture the actual pre-change generated sources once; never silently replace them.
tasks.register("captureProjectionBaseline") {
    dependsOn("luxoGenerate")
    doLast {
        val snapshot = file(".tmp/projection-before").apply { mkdirs() }
        for (name in listOf("Types.kt", "LuxoClient.kt", "Schema.kt", "SelectHints.kt")) {
            val source = file(".tmp/generated/com/luxo/generated/$name").readText()
            val target = snapshot.resolve(name)
            check(!target.exists() || target.readText() == source) { "Projection baseline already exists with different content" }
            target.writeText(source)
        }
    }
}

// Compare real generated clients; the frozen side is an explicit local input, not shipped code.
providers.gradleProperty("luxo.projectionBaseline").orNull?.let { snapshot ->
    val baseline = file(snapshot)
    val names = listOf("Types.kt", "LuxoClient.kt", "Schema.kt", "SelectHints.kt")
    val generated = layout.projectDirectory.dir(".tmp/projection-benchmark")
    val template = file("benchmark/JsonProjectionBenchmark.kt.txt")
    val harness = file("../../packages/kotlin/src/test/kotlin/com/luxo/client/WebSocketIDBenchmark.kt")
    val prepare = tasks.register("prepareProjectionBenchmark") {
        inputs.files(names.map { baseline.resolve(it) }, template, harness)
        outputs.dir(generated)
        doLast {
            val directory = generated.asFile.apply { mkdirs() }
            for (name in names) {
                val original = baseline.resolve(name).readText()
                check(original.contains("package com.luxo.generated\n")) { "Unexpected baseline package" }
                directory.resolve(name).writeText(original.replaceFirst("package com.luxo.generated",
                    "package com.luxo.projectionbefore"))
            }
            writeBenchmarkHarness(directory, harness)
            directory.resolve("JsonProjectionBenchmark.kt").writeText(template.readText())
        }
    }
    kotlin.sourceSets["main"].kotlin.srcDir(generated)
    tasks.named("compileKotlin") { dependsOn(prepare) }
    tasks.register<JavaExec>("benchmarkProjection") {
        group = "verification"
        description = "Compare JSON list projection with the explicitly frozen generated client"
        classpath = sourceSets["main"].runtimeClasspath
        mainClass.set("app.JsonProjectionBenchmark")
        jvmArgs("-Xms512m", "-Xmx512m")
        providers.gradleProperty("luxo.benchmarkJfr").orNull?.let { recording ->
            val repository = file(".tmp/jfr-repository")
            doFirst { repository.mkdirs() }
            jvmArgs("-XX:FlightRecorderOptions=repository=$repository")
            jvmArgs("-XX:StartFlightRecording=settings=profile,filename=${file(recording)},dumponexit=true")
        }
    }
}
