import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
    `java-gradle-plugin`
    `maven-publish`
}

group = rootProject.group
version = rootProject.version

java {
    sourceCompatibility = JavaVersion.VERSION_26
    targetCompatibility = JavaVersion.VERSION_26
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_26)
    }
}

repositories {
    mavenCentral()
}

dependencies {
    compileOnly("org.jetbrains.kotlin:kotlin-gradle-plugin-api:${libs.versions.kotlin.get()}")
    testImplementation("org.jetbrains.kotlin:kotlin-gradle-plugin-api:${libs.versions.kotlin.get()}")
    testImplementation(gradleTestKit())
    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
}

tasks.test {
    useJUnitPlatform()
    dependsOn(rootProject.tasks.named("jar"))
    dependsOn(tasks.jar)
    inputs.files(rootProject.tasks.named<Jar>("jar").flatMap { it.archiveFile }, tasks.jar.flatMap { it.archiveFile })
    inputs.files(rootProject.file("build.gradle.kts"), rootProject.file("settings.gradle.kts"),
        rootProject.file("gradle/libs.versions.toml"), rootProject.file("../../VERSION"))
    inputs.file(rootProject.file("../../pkg/lux/codec/testdata/protocol-v1.json"))
    inputs.files(rootProject.file("gradlew"), rootProject.file("gradlew.bat"),
        rootProject.file("gradle/wrapper/gradle-wrapper.properties"), rootProject.file("gradle/wrapper/gradle-wrapper.jar"))
    doFirst {
        systemProperty("luxo.testPluginJar", rootProject.tasks.named<Jar>("jar").get().archiveFile.get().asFile.absolutePath)
        systemProperty("luxo.testGradleUserHome", gradle.gradleUserHomeDir.absolutePath)
        systemProperty("luxo.testGradlePluginJar", tasks.jar.get().archiveFile.get().asFile.absolutePath)
        systemProperty("luxo.testRootProjectDir", rootProject.projectDir.absolutePath)
    }
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
