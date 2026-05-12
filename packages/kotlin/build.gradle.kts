plugins {
    kotlin("jvm") version "2.0.0"
    kotlin("plugin.serialization") version "2.0.0"
}

group = "com.luxo"
version = "0.1.0"

repositories {
    mavenCentral()
}

dependencies {
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
}

tasks.test {
    useJUnitPlatform()
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
    args(
        project.findProperty("luxo.endpoint")?.toString() ?: "http://localhost:4000/luvia",
        project.findProperty("luxo.key")?.toString() ?: "",
        project.findProperty("luxo.outDir")?.toString() ?: "src/main/kotlin/com/luxo/generated",
        project.findProperty("luxo.package")?.toString() ?: "com.luxo.generated",
    )
}

tasks.register<JavaExec>("luxoAnalyze") {
    group = "luxo"
    description = "Analyze source code for field access patterns and generate SelectHints"
    classpath = sourceSets["main"].runtimeClasspath
    mainClass.set("com.luxo.client.SelectAnalyzerCli")
    args(
        project.findProperty("luxo.srcDir")?.toString() ?: "src/main/kotlin",
        project.findProperty("luxo.hintsFile")?.toString() ?: "src/main/kotlin/com/luxo/generated/SelectHints.kt",
        project.findProperty("luxo.package")?.toString() ?: "com.luxo.generated",
    )
}
