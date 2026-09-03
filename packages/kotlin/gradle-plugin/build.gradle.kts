plugins {
    kotlin("jvm") version "2.4.10"
    `java-gradle-plugin`
    `maven-publish`
}

group = "com.luxo"
version = "0.1.0"

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
