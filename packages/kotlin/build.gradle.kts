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
}

// --- Luxo codegen tasks (for user projects) ---
// Usage:
//   ./gradlew luxoGenerate -Pluxo.endpoint=http://localhost:4000/luvia -Pluxo.key=YOUR_KEY
//   ./gradlew luxoAnalyze

tasks.register("luxoGenerate") {
    group = "luxo"
    description = "Generate typed client from Luxo schema introspection"
    doLast {
        val endpoint = project.findProperty("luxo.endpoint")?.toString()
            ?: error("Set -Pluxo.endpoint=http://...")
        val key = project.findProperty("luxo.key")?.toString()
            ?: error("Set -Pluxo.key=YOUR_KEY")
        val outDir = project.findProperty("luxo.outDir")?.toString()
            ?: "src/main/kotlin/com/luxo/generated"
        val pkg = project.findProperty("luxo.package")?.toString()
            ?: "com.luxo.generated"

        com.luxo.client.Codegen.LuxoCodegen.generate(endpoint, key, outDir, pkg)
    }
}

tasks.register("luxoAnalyze") {
    group = "luxo"
    description = "Analyze source code for field access patterns and generate SelectHints"
    doLast {
        val srcDir = project.findProperty("luxo.srcDir")?.toString()
            ?: "src/main/kotlin"
        val outFile = project.findProperty("luxo.hintsFile")?.toString()
            ?: "src/main/kotlin/com/luxo/generated/SelectHints.kt"
        val pkg = project.findProperty("luxo.package")?.toString()
            ?: "com.luxo.generated"

        com.luxo.client.ksp.SelectAnalyzer.analyze(srcDir, outFile, pkg)
    }
}
