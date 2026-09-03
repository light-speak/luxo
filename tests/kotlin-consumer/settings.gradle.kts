pluginManagement {
    includeBuild("../../packages/kotlin")
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

includeBuild("../../packages/kotlin") {
    dependencySubstitution {
        substitute(module("com.luxo:luxo-client")).using(project(":"))
    }
}

rootProject.name = "luxo-kotlin-consumer-test"
