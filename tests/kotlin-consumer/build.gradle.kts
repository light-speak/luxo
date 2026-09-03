plugins {
    kotlin("jvm") version "2.4.10"
    id("com.luxo.select")
}

repositories {
    mavenCentral()
}

dependencies {
    implementation("com.luxo:luxo-client:0.1.0")
}

layout.buildDirectory = layout.projectDirectory.dir(".tmp/build")

kotlin {
    sourceSets.named("main") {
        kotlin.srcDir(layout.projectDirectory.dir(".tmp/generated"))
    }
}

luxo {
    outputDirectory.set(layout.projectDirectory.dir(".tmp/generated/com/luxo/generated"))
}
