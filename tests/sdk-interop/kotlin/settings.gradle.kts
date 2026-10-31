pluginManagement {
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

dependencyResolutionManagement {
    versionCatalogs {
        create("libs") {
            from(files("../../../packages/kotlin/gradle/libs.versions.toml"))
        }
    }
}

includeBuild("../../../packages/kotlin") {
    dependencySubstitution {
        substitute(module("com.luxo:luxo-client")).using(project(":"))
    }
}

rootProject.name = "luxo-kotlin-go-interop"
