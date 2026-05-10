# luxo-client (Kotlin)

> Kotlin/Android client for Luxo API — OkHttp, WebSocket, Binary codec, Compiler Plugin

Kotlin/Android 客户端 — OkHttp 传输、WebSocket 订阅、Binary 编解码、编译器插件

## Install / 安装

```kotlin
// build.gradle.kts
dependencies {
    implementation("com.luxo:luxo-client:0.1.0")
}

// Optional: compile-time field tracking
plugins {
    id("com.luxo.select") version "0.1.0"
}
```

## Usage / 使用

```kotlin
val transport = OkHttpTransport(
    endpoint = "http://localhost:4000/luvia",
    token = "your-jwt-token",
    timeoutSeconds = 30,
    onTokenExpired = { refreshToken()?.token },
)
val client = LuxoClient(transport)
val user = client.getUser(1)
println(user.name)
```

## WebSocket / 订阅

```kotlin
val ws = LuxoWebSocket("ws://localhost:4000/luvia/ws")
ws.connect()
ws.subscribe("liveTraces", mapOf("projectId" to 1)) { data ->
    println("Trace: $data")
}
```

## Features / 功能

- **OkHttp Transport** — HTTP/2 with configurable timeout / OkHttp 传输 + 超时配置
- **WebSocket** — OkHttp WebSocket with reconnect / WebSocket 订阅
- **Binary Codec** — LuxoEncoder/LuxoDecoder / 二进制编解码
- **401 Auto-Refresh** — onTokenExpired callback / Token 过期自动刷新
- **Compiler Plugin (KCP)** — IR-level field tracking / 编译器插件字段追踪
- **Enum + Type codegen** — typealias + Values object / 枚举 + 类型代码生成
- **Nested $select** — Deep relation tracking via IR analysis / IR 嵌套关系追踪
- **Depth Warning** — > 5 levels compile-time warning / 深度超 5 层编译警告
