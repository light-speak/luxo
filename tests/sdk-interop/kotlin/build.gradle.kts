import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import org.jetbrains.kotlin.gradle.tasks.KotlinCompile
import org.gradle.api.artifacts.component.ProjectComponentIdentifier

plugins {
    alias(libs.plugins.kotlin.jvm)
}

repositories {
    mavenCentral()
}

val codegenRuntime = configurations.create("codegenRuntime")
dependencies {
    implementation("com.luxo:luxo-client:1.0.0-beta.1")
    codegenRuntime("com.luxo:luxo-client:1.0.0-beta.1")
}

val scratch = providers.environmentVariable("LUXO_INTEROP_SCRATCH").map { file(it) }
layout.buildDirectory.set(layout.dir(scratch).map { it.dir("kotlin-build") })
val generated = layout.buildDirectory.dir("generated")
val endpoint = providers.environmentVariable("LUXO_INTEROP_ENDPOINT")
val schemaEndpoint = providers.environmentVariable("LUXO_INTEROP_SCHEMA_ENDPOINT").orElse(endpoint)

java {
    sourceCompatibility = JavaVersion.VERSION_1_8
    targetCompatibility = JavaVersion.VERSION_1_8
}
kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_1_8)
        allWarningsAsErrors.set(true)
    }
    sourceSets.named("main") { kotlin.srcDir(generated) }
}

val generateClient = tasks.register<JavaExec>("generateClient") {
    classpath = codegenRuntime
    mainClass.set("com.luxo.client.LuxoCodegenCli")
    args(schemaEndpoint.get(), generated.get().asFile.absolutePath, "com.luxo.interop")
    inputs.property("endpoint", schemaEndpoint)
    outputs.dir(generated)
}

tasks.named<KotlinCompile>("compileKotlin") {
    dependsOn(generateClient)
    // Only this verification fixture may seed the SDK's internal uint64 boundary seam.
    val sdk = configurations.compileClasspath.get().incoming.artifactView {
        componentFilter { it is ProjectComponentIdentifier }
    }.files
    compilerOptions.freeCompilerArgs.add(sdk.elements.map { files ->
        check(files.isNotEmpty()) { "missing included SDK build" }
        "-Xfriend-paths=${files.joinToString(",") { it.asFile.absolutePath }}"
    })
}

val verifyProtocol = tasks.register<JavaExec>("verifyProtocol") {
    group = "verification"
    description = "Verify the generated Kotlin client against a live compiled Go service"
    classpath = sourceSets["main"].runtimeClasspath
    mainClass.set("app.GoProtocolConsumerKt")
    args(
        endpoint.get(),
        providers.environmentVariable("LUXO_INTEROP_CORPUS").get(),
        providers.environmentVariable("LUXO_INTEROP_TOPOLOGY").get(),
    )
}

tasks.check { dependsOn(verifyProtocol) }
