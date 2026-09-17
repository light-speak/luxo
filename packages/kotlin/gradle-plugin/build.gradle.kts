plugins {
    alias(libs.plugins.kotlin.jvm)
    `java-gradle-plugin`
    `maven-publish`
}

group = rootProject.group
version = rootProject.version

repositories {
    mavenCentral()
}

dependencies {
    testImplementation(gradleTestKit())
    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
}

tasks.test {
    useJUnitPlatform()
}

val toolingVersions = mapOf("kotlinVersion" to libs.versions.kotlin.get(), "luxoVersion" to version.toString())
tasks.processResources {
    inputs.properties(toolingVersions)
    filesMatching("com/luxo/gradle/tooling.properties") {
        expand(toolingVersions)
    }
}

gradlePlugin {
    plugins {
        create("luxo") {
            id = "com.luxo.select"
            implementationClass = "com.luxo.gradle.LuxoGradlePlugin"
            displayName = "Luxo Kotlin"
            description = "Luxo client generation and compiler-AST field selection"
        }
    }
}
