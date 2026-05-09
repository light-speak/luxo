plugins {
    kotlin("jvm") version "2.0.0"
    `java-gradle-plugin`
}

group = "com.luxo"
version = "0.1.0"

repositories {
    mavenCentral()
}

dependencies {
    implementation(kotlin("gradle-plugin-api"))
    compileOnly("org.jetbrains.kotlin:kotlin-compiler-embeddable:2.0.0")
}

gradlePlugin {
    plugins {
        create("luxo") {
            id = "com.luxo.select"
            implementationClass = "com.luxo.gradle.LuxoGradlePlugin"
            displayName = "Luxo Select Optimizer"
            description = "Compile-time field selection optimization for Luxo API calls"
        }
    }
}
