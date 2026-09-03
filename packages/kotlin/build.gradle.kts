import org.gradle.api.tasks.wrapper.Wrapper
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("jvm") version "2.4.10"
    kotlin("plugin.serialization") version "2.4.10"
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

val kotlinCompilerVersion = "2.4.10"
val luxoAnalyzerRuntime = configurations.create("luxoAnalyzerRuntime")

dependencies {
    api("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    api("com.squareup.okhttp3:okhttp:4.12.0")

    // The compiler is isolated to the build-time field analyzer and is not
    // published as an SDK runtime dependency.
    compileOnly("org.jetbrains.kotlin:kotlin-compiler-embeddable:$kotlinCompilerVersion")
    testRuntimeOnly("org.jetbrains.kotlin:kotlin-compiler-embeddable:$kotlinCompilerVersion")
    luxoAnalyzerRuntime("org.jetbrains.kotlin:kotlin-compiler-embeddable:$kotlinCompilerVersion")

    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
}

tasks.test {
    useJUnitPlatform()
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
