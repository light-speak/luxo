# luxo-client (Kotlin)

> Kotlin/Android client for Luxo API — OkHttp, WebSocket, Binary codec, and compiler-AST field selection

Kotlin/Android 客户端 — OkHttp 传输、WebSocket 订阅、Binary 编解码、编译器 AST 字段选择

## Install / 安装

```kotlin
// build.gradle.kts
dependencies {
    implementation("com.luxo:luxo-client:0.1.0")
}

plugins {
    id("com.luxo.select") version "0.1.0"
}

luxo {
    endpoint.set("http://localhost:4000/luvia")
    packageName.set("com.example.luxo")
}
```

Generate the schema-driven client once, then build normally. Kotlin compilation
automatically runs the incremental field analyzer. Keep the introspection key
out of command-line arguments:

```bash
LUXO_INTROSPECTION_KEY=YOUR_KEY ./gradlew luxoGenerate build
```

The plugin stores the schema snapshot in `src/main/luxo/luxo.schema.json` and
generated sources in `src/main/kotlin/com/luxo/generated` by default. The Kotlin
compiler is loaded only by the isolated analyzer process; it is not added to the
SDK runtime or the Gradle build classpath.

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
if (user.name.isSelected) println(user.name.value())
```

Generated output fields use the allocation-free `Selected<T>` value class:
unselected, selected `null`, or selected value. Input DTOs remain strict; a
shared input/output type generates `Foo` plus `FooInput`.

生成的输出字段使用零额外包装分配的 `Selected<T>` value class，区分未选择、已选择且为
`null`、已选择且有值。输入 DTO 保持严格；输入输出共用类型会分别生成 `Foo` 和 `FooInput`。

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
- **Gradle Plugin** — automatic incremental analysis before Kotlin compilation / Kotlin 编译前自动增量分析
- **Compiler AST** — schema-exact field tracking in an isolated process / 隔离进程中的 schema 精确字段追踪
- **Enum + Type codegen** — typealias + Values object / 枚举 + 类型代码生成
- **Nested $select** — Deep relation and lambda field tracking / 嵌套关系与 lambda 字段追踪
