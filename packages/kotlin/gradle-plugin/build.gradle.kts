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
    doFirst {
        systemProperty("luxo.testPluginJar", rootProject.tasks.named<Jar>("jar").get().archiveFile.get().asFile.absolutePath)
        systemProperty("luxo.testGradleUserHome", gradle.gradleUserHomeDir.absolutePath)
        systemProperty("luxo.testGradlePluginJar", tasks.jar.get().archiveFile.get().asFile.absolutePath)
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
