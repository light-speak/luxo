plugins {
    kotlin("jvm") version "2.0.0"
}

group = "com.luxo"
version = "0.1.0"

repositories {
    mavenCentral()
}

dependencies {
    compileOnly("org.jetbrains.kotlin:kotlin-compiler-embeddable:2.0.0")
}
