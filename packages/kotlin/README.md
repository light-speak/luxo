# luxo-client (Kotlin)

> Kotlin/Android client for Luxo API — OkHttp, WebSocket, Binary codec, and compiler-AST field selection

Kotlin/Android 客户端 — OkHttp 传输、WebSocket 订阅、Binary 编解码、编译器 AST 字段选择

## Toolchain and integer precision / 工具链与整数精度

The SDK, serialization compiler plugin and isolated field analyzer use Kotlin
2.4.20, with Gradle 9.7.1. The [version catalog](gradle/libs.versions.toml) owns
the Kotlin version; the published Gradle plugin embeds tooling versions from
that build rather than choosing the host Gradle's Kotlin version. The SDK's JVM
bytecode target remains 1.8; upgrading the compiler does not by itself change
Android device support.

SDK、序列化编译插件及隔离字段分析器统一使用 Kotlin 2.4.20，Gradle 为 9.7.1。
Kotlin 版本只在 version catalog 中维护，发布插件携带构建时的版本元数据，不依赖宿主
Gradle 内置的 Kotlin 版本。SDK 的 JVM 字节码目标仍为 1.8；编译器升级不代表提高
Android 设备最低版本，也不替代真实设备兼容性验证。

Kotlin CI runs on Temurin JDK 26, supported by the
[Gradle compatibility matrix](https://docs.gradle.org/9.7.0/userguide/compatibility.html).
Local integration and benchmark results identify the exact JDK patch used;
they do not imply validation of every newer patch or Android release.

Kotlin CI 使用 Temurin JDK 26，见上方 Gradle 官方兼容矩阵。本地联测和基准注明
实际 JDK 补丁版本，不把这些结果视为所有更新补丁或 Android 版本均已通过。

Field analysis uses the current compiler's project-environment factory and
configuration-service initializer, without opting into deprecated K1/Core
environment entry points. Compiler internals are still version-specific and
must be revalidated on upgrades. Stable context-parameter syntax is covered by
both analyzer tests and an independently compiled generated-client consumer.

字段分析使用当前编译器的项目环境与配置服务初始化入口，不再 opt-in 已弃用的旧 K1/Core
环境入口。编译器内部 API 仍与版本绑定，每次升级都必须复测。稳定的 context 参数语法
已同时纳入分析器测试与实际生成客户端的独立编译测试。

Luxo `Int` and `Duration` map to Kotlin `Long`, including nullable values, arrays,
and `Page.total/page/pageSize`. JSON uses integer tokens and binary uses signed
64-bit varints; do not convert through `Double` or narrow to `Int`. Pagination
metadata previously used `Int`: regenerate clients and recompile consumers, and
replace explicitly typed `Int` pagination variables with `Long`. This corrects
SDK precision without changing wire bytes.

Luxo `Int` / `Duration` 对应 Kotlin `Long`，覆盖 nullable、数组以及分页的
`total/page/pageSize`。JSON 保留整数 token，Binary 使用有符号 64 位 varint，
不要中转 `Double` 或窄化成 `Int`。原来的分页元数据类型为 `Int`；升级需要重新生成
客户端、重新编译消费者，并调整显式声明为 `Int` 的分页变量。修复不改变线上编码。

Schema-aware JSON and binary parameter encoding reject fractional, floating,
string and overflowing inputs for `Int`/`Duration` instead of calling
`Number.toLong()`. Signed JVM integer widths remain accepted; nested inputs,
arrays and nullable fields use the same validation. `Float` and opaque `JSON`
retain their own numeric semantics. Input-field lookup is prepared with the
API schema, and existing JSON objects/arrays are reused after validation.

JSON/Binary 的 `Int` / `Duration` 参数拒绝浮点数、字符串及溢出输入，不再通过
`Number.toLong()` 静默截断；JVM 各有符号整数宽度仍可使用。嵌套对象、数组及可空字段
执行相同校验，`Float` 与 opaque `JSON` 不套用整数规则。字段索引随 Schema 初始化，
已有 JSON 对象和数组校验后复用；这不代表每次请求的校验开销为零。

Generated JSON responses use `decodeLuxoJsonInt` / `decodeLuxoJsonFloat` for
scalars, arrays, nullable fields, page metadata and stream values. Integer tokens
must have signed-int64 integer syntax: quoted numbers, fractions and exponents
are rejected, even when numerically integral. Float permits IEEE-754 rounding
but rejects invalid JSON numeric syntax, overflow and nonzero underflow to zero;
signed zero and finite subnormals remain valid. Invalid values produce `ParseError`
before delivery to the caller or stream callback. Error codes additionally require
the signed 32-bit range. Regenerate clients together with this runtime update.

Generated String and Boolean responses also check JSON kinds, rather than
coercing primitive content to text or accepting quoted booleans. Scalars, list
elements, nested/nullable fields and stream values share the same readers;
Base64 Bytes and textual DateTime, UUID, Decimal and enums require a JSON string.
DateTime additionally validates timezone-bearing RFC3339, at most nine fractional
digits and UTC years 0000–9999. It stays a String in both modes; Binary carries
signed floor seconds plus unsigned nanoseconds. Excess precision and range are
errors. Regenerate clients and services together: the old whole-second wire is
incompatible. Database precision and performance are separate acceptance gates.
Opaque JSON keeps its own rules; other textual formats and enum membership are
separate checks, as is stream termination after a decode error.

生成的 JSON 响应对标量、数组、可空字段、分页元数据与流统一使用严格数值读取器。
Int/Duration 只接受 signed-int64 整数语法；字符串、小数和指数即使数学上等于整数
也拒绝。Float 允许正常舍入，但拒绝非法 JSON 数字语法、溢出和非零下溢成零；
保留负零与有限次正规数。非法值在交付结果或流回调之前返回 `ParseError`；错误码
还须在有符号 32 位范围内。更新运行时后必须重新生成客户端。

These typed conversions do not narrow opaque `JsonElement` data. Generic JSON
numbers, including error data, retain their original tokens; explicit conversion
is the boundary that applies business numeric limits. The local conversion
benchmark is `./gradlew benchmarkJSONNumbers --no-daemon --warning-mode fail`;
its JVM timing/allocation results do not substitute for Android or full requests.

上述限制不缩小通用 `JsonElement` 的数域；通用 JSON（含错误 data）保留数字原文，
显式转成业务类型时才检查范围。上面的基准命令测量局部 JVM 耗时与分配，不能代替
Android 真机或完整请求验收，也不代表严格 Float 校验没有额外成本。

Generated input DTOs implement `toLuxoJson()` with explicit field projections,
not `Json.encodeToJsonElement(this)` or runtime serializer discovery. SDK calls
therefore do not require consumers to apply the serialization compiler plugin;
the optional `@Serializable` annotation remains for consumers' own serialization.
`Long` values stay integer tokens, Bytes use Base64, nested inputs call their own
projection, and opaque JSON elements retain their representation. Nullable DTO
fields encode explicit null, matching Binary's null marker; this does not change
API argument omission through `LuxoOptional.Absent`. Generated Float projections
reject nonfinite values instead of producing invalid JSON. Regenerate clients
with the updated SDK. Existing schema-boundary validation remains active.

生成输入 DTO 的 `toLuxoJson()` 逐字段编码，不再调用通用序列化器或运行时查找
serializer；正常 SDK 请求不要求消费者启用序列化编译插件。仍保留 `@Serializable`
注解，供消费者自己的序列化使用。Long 保持整数 token，Bytes 使用 Base64，嵌套
输入递归调用自身编码，opaque JSON 原样保留。DTO 可空字段显式写 null，与 Binary
的 null 标记一致；API 参数的 `LuxoOptional.Absent` 省略规则不变。生成 Float 编码
拒绝非有限值，不输出非法 JSON。升级后重新生成客户端；Schema 边界校验仍保留。

## Named enums / 命名枚举

Generated enums are sealed interfaces, not String aliases. `Role.Known.MEMBER`
is a canonical known value; `Role.fromValue(raw)` returns a known constant or a
`Role.Unknown` retaining the exact string. Exhaustive `when` expressions handle
known constants and `is Role.Unknown`. Unknown equality is scoped to that enum.
Regenerate clients and replace raw String arguments with the generated type.
JSON/Binary still carry strings; generated parameter and input writers unwrap
`rawValue` only at declared enum positions. Omitted defaults, explicit null and
unselected fields stay distinct. Generic JSON does not implicitly unwrap enums.
The server continues to reject values absent from its own Schema.

生成枚举改为 sealed interface，不再是 String 别名。`Role.Known.MEMBER` 是已知常量，
`Role.fromValue(raw)` 将已知值映射到常量，未知值以 `Role.Unknown` 保留原文。
业务可用穷尽 `when` 匹配已知常量和 Unknown 分支；不同枚举的未知值不相等。
升级需重新生成客户端，并把字符串参数改为生成类型。JSON/Binary 仍传字符串，
生成参数和输入写入器只在声明为 Enum 的位置解包 `rawValue`；缺省、null 和未选择
字段语义不变，通用 JSON 不做隐式枚举转换。服务端仍拒绝自身 Schema 未声明的值。

Missing enum references, unnamed enum returns and empty/duplicate declarations
fail before snapshots or generated sources are overwritten. Known values reuse
constants; unknown wrappers and list conversions are not allocation-free.
Performance and Android device validation remain separate acceptance gates.

缺失枚举引用、裸 Enum 返回值、空或重复声明在覆盖快照/代码前报错。已知值复用
常量，但未知包装及列表转换并非零分配；性能和 Android 真机仍需独立验收。

Introspection preserves known Go metadata, including descriptions, directives,
deprecation, primary/foreign keys and relationships. Unknown schema properties
are still rejected rather than ignored globally. Network introspection shares
the runtime's strict UTF-8 document decoder, accepts one leading document BOM,
and then decodes the typed schema once. Invalid encoding or schema data fails
before creating or overwriting the snapshot and generated sources.

Introspection 补齐 Go 已定义的描述、指令、废弃信息、主外键及关联元数据。
未知 Schema 属性仍严格拒绝，不使用全局忽略未知字段掩盖兼容问题。
网络 Introspection 与运行时共用严格 UTF-8 文档读取，接受一个文档前导 BOM，
随后只做一次强类型 Schema 解析。坏编码或 Schema 数据在创建/覆盖快照及生成文件前报错。

The build-time analyzer also reads local schema snapshots and Kotlin sources as
strict UTF-8. Snapshot documents accept one leading BOM; source code points are
not removed or replaced. Encoding failures identify the input file and preserve
existing selection hints. Generated/test directories are excluded before reading.

构建期分析器对本地 Schema 快照与 Kotlin 源码同样严格读取 UTF-8。快照接受一个文档
前导 BOM；源码字符不删除、不替换。编码失败指出文件路径，保留已有字段选择结果；
generated/test 目录在读取前排除。此检查发生在生成/分析阶段，不增加业务请求的解析轮次。

Malformed error codes produce `ParseError`, never integer wraparound or an
uncorrelated timeout. WebSocket numeric request IDs are not matched to quoted
string IDs, and wide binary frame tags cannot alias valid tags by narrowing.

非法错误码明确返回 `ParseError`，不绕回其他整数，也不丢失关联请求后等到超时。
WebSocket 数值请求 ID 不接受字符串冒充，Binary 帧类型不会因窄化而匹配合法帧。

WebSocket `$id` / binary `sequence` share the complete unsigned 64-bit range
(`0…18446744073709551615`), independently of signed business `Int`/`Duration`.
The counter, pending-call map and binary codec retain the same `Long` bit pattern;
JSON writes unsigned decimal numbers, not negative or quoted IDs. The existing
serialization dependency provides the [unsigned JSON number overload](https://kotlinlang.org/api/kotlinx.serialization/kotlinx-serialization-json/kotlinx.serialization.json/-json-primitive.html).
No big-integer runtime dependency or additional JSON round trip is introduced.

WebSocket `$id` 与 Binary `sequence` 使用完整 UInt64 范围，与业务 Int/Duration
的有符号语义分开。计数器、待响应 Map 和 Binary 编码器用同一个 Long 保存全部位；
最高位为 1 并不代表发送负数，JSON 输出无引号的无符号十进制整数。复用现有序列化
依赖的无符号数值接口，没有新增大整数运行时依赖或额外 JSON 编解码。

ID parsing accepts only canonical ASCII decimal integer tokens. Missing IDs,
negative numbers (including `-0`), leading zeroes, strings, booleans, fractions,
exponents and overflow never consume a pending request. Exhaustion raises
`RequestIDExhausted`; concurrent calls cannot wrap and overwrite another request.
Call parameters cannot override `$id`/`$api`. Subscription success requires an
unquoted JSON `true` or an empty binary acknowledgement payload; binary frame
tags must occupy one byte.

编号只接受规范 ASCII 十进制整数 token；缺失、负数（含 -0）、前导零、字符串、
布尔值、小数、指数及溢出均不能消费等待中的请求。耗尽时抛出 RequestIDExhausted，
并发调用不会回绕或覆盖另一个请求。调用参数不能覆盖 `$id/$api`。订阅成功确认必须
是 JSON 布尔 true，或不附带 payload 的 Binary 确认帧；帧类型必须为单字节。

Generated `subscribe...` methods and `Transport.subscribe` accept an optional
`observer: StreamObserver?` before the trailing data callback. `onComplete`
receives normal JSON `$end` / binary `0x15` completion; `onError` receives the
structured business error from `$end` / `0x16`, preserving raw JSON number tokens.
The subscription is removed before notification, outside the lifecycle lock;
duplicate terminal frames and late data cannot notify it again. Explicit cancel
is silent. Setup failures still throw from `subscribe`. Callback exceptions are
application failures, not silently ignored malformed frames. A lost connection
terminates established subscriptions with `NetworkError`; reconnecting does not
replay them or promise lossless resume.

生成的订阅方法和 Transport.subscribe 在末尾数据回调前增加可选 observer。
onComplete 接收正常完成，onError 接收保留原始数字的结构化业务错误。通知前先删除
登记，且不在生命周期锁内调用用户回调；重复终止和迟到数据不再次通知。主动取消
保持静默，订阅建立失败仍由 subscribe 抛出；用户回调异常不再被当成坏包吞掉。
断线以 NetworkError 终止已建立的订阅；连接重连不自动重订阅，也不承诺无损恢复。

```kotlin
val observer = StreamObserver(
    onComplete = { println("Stream completed") },
    onError = { error -> println(error) },
)
val cancel = transport.subscribe("liveTraces", { it }, mapOf("projectId" to 1L), observer) { data ->
    println(data)
}
cancel()
```

## Typed query controls / 类型化查询条件

For an API `listUsers` advertising an Int `count` filter and sorter, `Queries.kt` provides typed helpers:

```kotlin
val filters = listOf(LuxoQuery_listUsers.count_eq(9007199254740993L))
val sorters = listOf(LuxoQuery_listUsers.count_desc())
```

Pass these to the generated API's `filters` / `sorters` arguments. Only advertised fields and operators get helpers; dynamic controls remain available and server validation remains mandatory.

构造器只依据 API 明确开放的字段和操作符生成，不根据返回模型猜测。直接创建条件值，不增加运行时 Schema 查找；Int 保持 Long 精度，服务端仍严格校验查询。

## Install / 安装

These coordinates target the unreleased `1.0.0-beta.1` candidate; use them after
publication. Keep the client and field-selection plugin on the same exact version.

```kotlin
// build.gradle.kts
plugins {
    kotlin("jvm") version "2.4.20"
    id("com.luxo.select") version "1.0.0-beta.1"
}

dependencies {
    implementation("com.luxo:luxo-client:1.0.0-beta.1")
}

luxo {
    endpoint.set("http://localhost:4000/luvia")
    packageName.set("com.example.luxo")
}
```

Generate the schema-driven client once, then build normally. Kotlin compilation
loads the Luxo IR plugin to inject selections at each resolved call site. Keep the introspection key
out of command-line arguments:

```bash
LUXO_INTROSPECTION_KEY=YOUR_KEY ./gradlew luxoGenerate build
```

The plugin stores the schema snapshot in `src/main/luxo/luxo.schema.json` and
generated sources in `src/main/kotlin/com/luxo/generated` by default. The Kotlin
compiler plugin uses the project's pinned Kotlin compiler; its compiler API is
not an SDK runtime dependency. Kotlin must match the version catalog (currently
2.4.20); a mismatch is rejected rather than silently disabling inference.

Each invocation receives its own selection. Local immutable aliases, nested
`Selected.value()` accesses, inline `map`/`mapNotNull`/`forEach` callbacks and
inline subscription callbacks are supported. Explicit `select` takes precedence.
Opaque model escape, unresolved usage and API function references require an
explicitly selected wrapper; they fail compilation instead of underfetching or
selecting all fields. Generated clients no longer read API-wide `SelectHints`;
the legacy `luxoAnalyze` task is diagnostic only and is not a compile dependency.

The JVM acceptance includes generated typed Binary requests and a real Gradle
consumer with incremental source changes. Android, other Kotlin targets,
complete inference coverage and performance remain separate acceptance gates.

## Usage / 使用

```kotlin
val transport = OkHttpTransport(
    endpoint = "http://localhost:4000/luvia",
    token = "your-jwt-token",
    timeoutSeconds = 30,
    onTokenExpired = { refreshToken()?.token },
)
val client = LuxoClient(transport)
val user = client.getUser(1L)
if (user.name.isSelected) println(user.name.value())
```

Generated output fields use the `Selected<T>` value class:
unselected, selected `null`, or selected value. Input DTOs remain strict; a
shared input/output type generates `Foo` plus `FooInput`. Value classes can avoid
wrapper allocations, but generic/nullable use and primitive storage may still
box values; they are not an unconditional zero-allocation guarantee.

生成的输出字段使用 `Selected<T>` value class，区分未选择、已选择且为 `null`、
已选择且有值。值类可以省去包装对象，但泛型、可空使用及基础值存储仍可能装箱，
不能承诺所有场景零分配。输入 DTO 保持严格；输入输出共用类型会分别生成 `Foo` 和 `FooInput`。

## WebSocket / 订阅

```kotlin
val ws = LuxoWebSocket("ws://localhost:4000/luvia/ws")
ws.connect()
val cancel = ws.subscribe("liveTraces", { it }, mapOf("projectId" to 1L)) { data ->
    println("Trace: $data")
}
cancel()
```

Each subscription has an independent UInt64 ID. Concurrent subscriptions to the
same API no longer conflict; ACK, data, setup errors and cancellation match only
that ID. JSON subscribe requests send numeric `$id` with API-name `$sub`; ACK/error
`$sub`, data `$stream` and cancellation `$unsub` carry numeric IDs. Binary uses
frame tags `0x10…0x14`. Retired `0x04…0x08` subscription frames are rejected.
Use the returned idempotent cancellation handle, not `unsubscribe(apiName)`.
Timeout or coroutine cancellation while awaiting ACK sends an unsubscribe.

Low-level subscriptions require a typed `decoder` before `params`; the examples
above explicitly use an identity decoder to receive raw payloads. Generated API
methods keep their existing call syntax and supply pure typed decoders separately
from application callbacks. Regenerate clients and adapt handwritten Transport
implementations together with this interface change.

A decoder exception retires only that subscription, attempts remote cancellation
and notifies `onError` once with `DecodeError`, never `onComplete`. Failed remote
cancellation does not restore local state. Late frames are ignored; unrelated
subscriptions and ordinary calls continue. Application handler/observer exceptions
propagate unchanged, outside the decoding catch and lifecycle lock. Fatal JVM
Errors are not converted to recoverable protocol failures. Normal data delivery
does not add a lifecycle lock, reflection or serialization round trip; performance
and Android/device validation remain separate release gates.

每次订阅使用独立 UInt64 编号，同 API 可并发多次订阅，确认、数据、建立错误和退订只
关联对应编号。JSON 订阅请求的 `$sub` 仍为 API 名，`$id` 以及确认、数据、退订中
携带的编号使用无引号数字；Binary 使用 `0x10…0x14`，不降级接受旧
订阅帧。取消必须调用返回的幂等句柄，不再按 API 名退订；等待确认超时或协程取消时
也会发送退订。ACK 前或不匹配原始传输模式的数据不交给业务回调。

Disconnect retires every subscription. Established subscriptions receive one
`NetworkError` through `onError`; pending `subscribe` calls throw `NetworkError`
without also notifying the observer. Ordinary pending calls retain their
`ConnectionError`. Connection auto-reconnect may continue, but the caller must
explicitly subscribe again with a new ID. No encoded request is kept for replay,
and stale messages or cancellation handles cannot affect a new subscription.
Observers run after retirement and outside the lifecycle lock. If one throws,
the others are still notified, then the first exception is rethrown with any
distinct additional exceptions suppressed. Explicit cancel/close is silent.
Other SDKs are still migrating to this disconnect contract; it is not frozen yet.

An unparseable JSON message, invalid root or malformed Unicode closes its socket
and fails all pending calls/setup and established streams with `ParseError`.
IDs are not guessed from a damaged document; reconnect does not replay streams.
A valid envelope with bad business data remains a subscription-local `DecodeError`.

断线先移除全部订阅。已建立订阅通过 onError 收到一次 NetworkError；尚未收到 ACK
的 subscribe 直接抛出 NetworkError，不再重复通知 observer。普通等待中的调用仍报
ConnectionError。连接可以自动重连，但必须由调用方显式建立新编号的订阅，不保留
请求编码用于重放；旧消息和旧取消句柄不影响新订阅。通知在清理完成后、生命周期锁
之外执行；某个回调抛异常仍继续通知其余回调，最后重新抛出首个异常，并保留其他
异常为 suppressed。主动取消/关闭保持静默。其余 SDK 正在同步，不能据此宣布冻结。

JSON 整包解析失败（含非法根类型、非法 Unicode）关闭对应连接，全部在途调用、
待确认订阅与已建立流收到 ParseError；不从残缺文档猜测编号，重连不重放订阅。
若整包合法、只是业务数据解码失败，仍以 DecodeError 仅终止对应订阅。

## Features / 功能

- **OkHttp Transport** — HTTP/2 with configurable timeout / OkHttp 传输 + 超时配置
- **WebSocket** — OkHttp WebSocket with reconnect / WebSocket 订阅
- **Binary Codec** — LuxoEncoder/LuxoDecoder / 二进制编解码
- **401 Auto-Refresh** — onTokenExpired callback / Token 过期自动刷新
- **Gradle Plugin** — automatic incremental analysis before Kotlin compilation / Kotlin 编译前自动增量分析
- **Compiler AST** — schema-exact field tracking in an isolated process / 隔离进程中的 schema 精确字段追踪
- **Enum + Type codegen** — typealias + Values object / 枚举 + 类型代码生成
- **Nested $select** — Deep relation and lambda field tracking / 嵌套关系与 lambda 字段追踪

## Verification / 验证

### HTTP protocol boundaries / HTTP 协议边界

Only HTTP 200 carries a successful response. A 401 refresh callback runs only
after a valid error envelope is decoded with `code == 401`; malformed bodies,
body-read failures and mismatched status/code values do not refresh or resend.
There is at most one retry, retaining the original JSON/Binary mode. A declined
refresh preserves the server error; a thrown callback error propagates unchanged.
Body-read failures close the response and complete the caller with `NetworkError`.

HTTP JSON bytes are decoded strictly as UTF-8, ignoring conflicting charset
headers. One UTF-8 document BOM is accepted; field values keep U+FEFF/U+FFFD.
Malformed UTF-8, UTF-16/32 documents and invalid JSON return `ParseError` with
the HTTP status, before any authentication refresh. A real loopback test splits
UTF-8 across chunked response writes. Strict decoding adds measured time and
allocation costs; these remain subject to the performance gate.

仅 HTTP 200 承载成功响应。先验证错误体，只有 HTTP/code 均为 401 才刷新，最多重试
一次且保持原 JSON/Binary 模式；非法错误、读响应体失败或状态/code 不一致不刷新。
回调返回 null 时保留服务端错误，回调抛出的异常原样传播；响应体读取失败会关闭响应，
以 NetworkError 通知调用方，不让调用方一直等待。

HTTP JSON 固定使用严格 UTF-8，不受冲突的 charset 响应头影响。接受一个 UTF-8
文档 BOM，字段内的 U+FEFF/U+FFFD 保留。坏 UTF-8、UTF-16/32 文档和非法 JSON
在认证刷新前返回携带 HTTP 状态码的 ParseError；真实回环测试覆盖逐字节分块响应。
严格解码存在已测得的时间和分配增量，仍受性能门禁约束。

Protocol headers (`Content-Type`, `X-Luxo-Mode`) are SDK-owned and cannot be
overridden by custom casing. Custom headers are snapshotted at construction;
an explicit token takes precedence over custom Authorization. Immutable JSON/
Binary header snapshots are replaced together on token changes, avoiding custom
map traversal per request. Snapshot construction is initialization work, not a
claim of zero-allocation requests.

Content-Type 和 X-Luxo-Mode 由 SDK 控制，不能通过自定义头或大小写覆盖。初始化时
快照自定义头，显式 token 优先于自定义 Authorization；token 更新时整体替换两种
模式的不可变头快照，请求中不再遍历自定义 Map。这是初始化与请求成本分离，不代表
完整请求零分配。

Binary error decoding rejects duplicate/aliased field IDs, invalid UTF-8,
truncation and trailing bytes. Valid literal U+FFFD remains accepted. Byte-string
lengths are checked before narrowing to Int. Business integers remain Long;
the public error code is still Int, with cross-SDK error-code alignment pending.
Row/column strings, nullable strings and JSON bytes inside Binary now share
strict UTF-8 handling. Leading U+FEFF and literal U+FFFD remain intact; opaque
Bytes are not text-validated. Row strings decode their source range without an
intermediate byte copy. Ambiguous U+FFFD text uses the platform strict decoder
for verification, which has a separately measured cost. HTTP JSON uses a single
strict decoding pass as described above; device validation and performance
gates remain release requirements.

JSON strings and object keys must contain complete Unicode characters, including
opaque JSON and error data. `decodeLuxoJSON` checks literal characters and escaped
surrogate pairs before the existing parser can discard an overwritten duplicate
key. `encodeLuxoJSON` serializes once and validates the result, including raw JSON
literals; neither entry performs an encode/decode round trip or serializer discovery.
Valid pairs, U+FFFD, BOM and literal backslash sequences retain their meaning.

Regenerate clients to use the strict JSON boundaries for scalar/list/model reads
and JSON values embedded in Binary. Binary String and arena-size calculation now
reject incomplete host strings before UTF-8 encoding can replace them. String
fields and callback-based array writes restore their starting position on failure;
callers must still discard failed whole-message encodings. Bytes remains opaque.
These checks add independently measured costs; they do not imply zero allocation,
Android/device validation, or approval of pending WebSocket envelope-error policy.

The small callback-based array writer is now inline, with `crossinline` preserving
the prohibition on non-local returns. It retains sequential traversal for arbitrary
Lists and rolls back the write position before rethrowing a callback failure.
Its two `@PublishedApi` position helpers are part of the inline implementation ABI,
not a general checkpoint API. Recompile consumers to pick up inline-body changes;
the Binary layout and generated API call signatures are unchanged.

Binary 错误拒绝重复/窄化后混淆的字段 ID、非法 UTF-8、截断和尾随数据，合法 U+FFFD
字符仍可使用；字节串长度先验证再转 Int。业务整数仍是 Long，公开错误码仍是 Int，
跨 SDK 错误码范围尚待统一。行式/列式/可空字符串和 Binary 内的 JSON 现共用严格
UTF-8 读取，保留前导 U+FEFF 与合法 U+FFFD，原始 Bytes 不受文本限制。行式读取
直接使用源字节区间；含 U+FFFD 的歧义文本使用平台严格解码器复核，其成本单独
记录。HTTP JSON 使用上文描述的一次严格解码；设备与性能仍是发布门禁。

Generated scalar responses and independent scalar-array column cells check
their entire payload before returning or invoking stream callbacks. This check
is not inserted between fields of a larger message. Named enum returns use
strict scalar string readers followed by the generated Known/Unknown factory,
not model selection or model decoders. Regenerate clients with this runtime.

生成的标量响应和独立标量数组列单元在返回或触发流回调前检查完整负载，不把结束
检查插入父消息的字段之间。命名枚举返回先严格读取字符串，再由生成工厂区分
Known/Unknown，不走模型选择/解码，也不丢弃未知值；升级后需重新生成客户端。

### Runtime schema ownership / 运行时 Schema 所有权

`OkHttpTransport` constructor metadata and HTTP/WebSocket `setSchema` validate
all API, parameter, field and nested-type IDs before publication. Invalid or
duplicate IDs throw `IllegalArgumentException`; failed replacement keeps the
previous registration. Both modes use the same privately owned snapshot.
Mutating the original maps/lists has no effect until explicit re-registration;
do not mutate input containers concurrently while registration is copying them.
Shared field/type directories stay shared within one registration. Checks run
at registration, not on every request or stream message; no reflection or
encoding/decoding roundtrip is used to copy metadata.

`OkHttpTransport`构造及HTTP/WS的`setSchema`先校验API、参数、字段和嵌套type编号，
再发布两种协议共用的私有快照。非法/重复编号抛`IllegalArgumentException`，失败
保留旧登记。修改源Map/List后必须显式重新登记，复制期间不要并发修改输入容器。
共享字段/type目录在同次快照内继续共享；检查不进入每次请求或推送，不使用反射
或编解码往返复制元数据。

### Binary metadata bounds / Binary 元数据边界

Lengths and field IDs are validated at full width before converting to Kotlin
Int. Unsupported field IDs fail instead of aliasing another field or the end
marker; this is a Kotlin representation check, not a new global field-ID limit.
Array counts, column counts and arena sizes follow the Go reader's existing
limits: 1,000,000 elements, 10,000,000 rows and 64 MiB. Minimum payload lengths
are checked before result allocation, including every nullable column type.
Fixed-width Float/UUID columns validate their whole payload once, not per cell.
Business Int/Duration and page metadata remain signed Long without narrowing.

长度、字段 ID 先按完整位宽验证，再转换为 Kotlin Int。不支持的字段 ID 明确报错，
不会混淆成其他字段或结束标记；这只是 Kotlin 表示范围检查，不是新增全局字段 ID 上限。
数组、列式行数和 arena 沿用 Go 解码器的 100 万元素、1000 万行和 64 MiB 上限。
所有列类型（含 nullable）在分配结果前校验最小负载长度；固定长度 Float/UUID 列一次
验证整列，不逐单元格重复检查。业务 Int/Duration 和分页仍完整使用 signed Long。

Metadata comparisons use primitive sign-bit ordering. Both an explicit Java
unsigned comparison and the current compiler's ULong lowering call
[Long.compareUnsigned, introduced in Android API 26](https://developer.android.com/sdk/api_diff/26/changes/java.lang.Long).
The checked path avoids that platform call; this does not certify the entire SDK
on older Android versions, including its other Java APIs and dependencies.

元数据比较使用基础整数的符号位重排。直接调用 Java 无符号比较以及当前编译器生成的
ULong 比较都会调用 Android API 26 引入的 Long.compareUnsigned（见上方官方说明）。
此检查路径不依赖该平台接口，但不能据此宣称整个 SDK、其他 Java API 和依赖已通过
旧版 Android 兼容性验收。

### Checks / 检查

`./gradlew build :gradle-plugin:validatePlugins --warning-mode fail` checks SDK
tests and plugin metadata; SDK compilation treats warnings as errors. The external consumer in `tests/kotlin-consumer`
generates and compiles real client sources, and its `build` runs `verifyProtocol`:
JSON/binary HTTP round trips against the shared Go-verified int64 vectors,
including selected/null/unselected values and pagination. The loopback fixture
server is closed after execution; it is not a live Go-service, WebSocket or
Android-device integration test.

SDK 构建验证单元测试与插件元数据；外部消费者先生成并编译实际客户端，`build` 会执行
`verifyProtocol`，使用 Go 已校验的共享 int64 数据验证 JSON/Binary HTTP 往返、
字段选择状态及分页。回环测试服务执行后关闭；该检查不代表 Go 服务、WebSocket 或
Android 真机集成已经通过。

The separate [live Go consumer](../../tests/sdk-interop/README.md) generates
from a running compiled `.luxo` service and verifies HTTP/WebSocket × JSON/Binary,
including Int64 extremes, Duration, arrays, nullable/structured inputs, columnar
returns, explicit selection and native streams. Its CI step sets up the repository
Go toolchain. Neither it nor the external HTTP fixture certifies Android devices.
The external fixture additionally compiles and executes every supported input
scalar/list kind, nested and nullable JSON projection without a serialization plugin.
It also strictly compiles 11 scalar response kinds plus named enums, single/list,
call/stream, and locally injected truncation, bad UTF-8 and trailing data. These
negative probes are controlled transport payloads, not malformed network frames.

Generated JSON model/type lists require at least one Schema field per item,
including ordinary, paginated, nested and streaming lists. Unknown-only and empty
records are rejected; selected nullable fields, empty lists, opaque JSON and the
existing single-object decoder remain valid. Regenerate clients to apply this
rule. The external consumer also checks that a bad list terminates only its own
subscription, using controlled frames through the real WebSocket transport.
Performance and Android/network validation remain separate gates.

另有真实 Go 联测（见上方链接）：从运行中的 `.luxo` 编译服务生成客户端，验证四种
通道的 Int64 极值、Duration、数组、nullable/结构化输入、列式、显式选择与 native 流。
CI 独立步骤安装仓库要求的 Go 工具链；两套检查都不替代 Android 真机验收。外部
消费者另编译、执行全部输入标量/列表类型、嵌套和可空 JSON 编码，不依赖序列化插件。
另严格编译并执行 11 类标量返回与命名枚举的单值/列表、调用/流，以及本地注入的
截断、非法 UTF-8、尾随数据；这些负例不冒充真实网络畸形帧测试。

生成的 JSON 模型/type 列表每条记录必须包含至少一个 Schema 字段，覆盖普通、分页、
嵌套及流列表；空记录和仅含未知字段的记录报错。已选 nullable 字段、空列表、
通用 JSON 及单对象解码器保持原义。客户端需要重新生成；外部消费者通过真实
WebSocket Transport 注入受控坏列表，验证只终止当前订阅。性能、Android 与真实
网络仍独立验收。

Transport regression tests additionally exercise real OkHttp interceptors and
a deterministic WebSocket task boundary for invalid integers, malformed errors,
request-ID matching and subscription retries. These unit boundaries are not
substitutes for live network/WebSocket or device integration.

传输回归还覆盖真实 OkHttp 拦截器和可控 WebSocket 任务边界，检查非法整数、错误码、
请求 ID 关联与订阅重试。单元测试边界不替代真实 WebSocket 网络或设备集成。

`./gradlew benchmarkWebSocketIDs --offline --no-daemon --warning-mode fail`
runs a dependency-free local comparison against the previous signed parser and
unbounded counter. Independent JVM runs warm up and alternate samples, reporting
helper latency and thread allocations. The counter's exhaustion check has a
measured cost; improvements elsewhere do not waive its performance gate. These
results do not certify full-request, contended-counter or Android performance.

上述 benchmark 命令比较旧有符号解析/无限计数器与当前实现，无需新增依赖；独立 JVM
预热后交错采样，输出辅助函数耗时和线程分配。耗尽保护有实测成本，不能用其他路径
的提升抵扣。该检查不等同于完整请求、并发竞争或 Android 设备性能验收。

`./gradlew benchmarkWebSocketSubscriptions --offline --no-daemon --warning-mode fail`
compares current transport entry points with a frozen API-keyed implementation
kept only in test sources. It measures JSON/Binary stream dispatch, subscription
setup/ACK/cancel and unary fake-socket round trips, with per-thread allocations.
The old transport cannot isolate simultaneous subscriptions to one API and is
not an equivalently correct baseline. No network or Android cost is certified.

订阅基准直接调用真实 transport 的入口，不用反射；旧实现只保留在测试源中作对照，
不打包到 SDK、不用于协议降级。覆盖小编号、超过 Long 装箱缓存的编号和 UInt64 最大
编号，Schema 为 1/32/128 个 API，逐帧场景仅有一个活动订阅。它不替代并发争锁、
断线通知、大载荷、实际网络与 Android 设备的性能验收，各项增量仍需独立确认。

`./gradlew :benchmarkWebSocketTerminals --args=--disconnect-only --offline --no-daemon --warning-mode fail`
measures current setup/ACK, disconnect cleanup and fake-socket reconnect for
1/8/64 subscriptions, with no observer versus a shared observer. Both sides use
the new contract; this is an absolute lifecycle comparison, not a before/after
regression measurement. It excludes real network, Android and callback creation.

断线基准包含建立/确认、清理及假socket重连，比较不传observer与复用observer两种
情况，均为当前实现，不把差值当成整个修复相对旧实现的成本。报告每次生命周期的
耗时与线程分配字节，不是逐消息成本；真实网络、Android及回调创建未纳入。

`./gradlew :benchmarkHTTPProtocol --offline --no-daemon --warning-mode fail`
compares HTTP JSON body conversion, byte-length bounds, mode-specific request
headers and strict Binary errors, reusing the paired JVM harness. Header initialization is a separate
comparison; the new immutable snapshots are not free. Error comparisons share
the current primitive codec on both sides. Results include thread allocations,
not networking, actual 401 retries or device performance; local improvements do
not waive other gates. Benchmark setup, fixture encoding and equivalence checks
run before timing.

HTTP 基准共用成对 JVM 测量工具，分别比较 JSON 响应体解码、长度校验、请求头和 Binary 错误；初始化
头快照单独计量，不把它说成免费。错误比较两侧使用相同基础 codec，输出线程分配，
不包含网络、真实 401 重试或设备性能。fixture 编码和前后等价检查在计时外执行。

Use `--args=--json-only` for the JSON body comparisons alone. They include the
new byte-array and strict-decoder allocations; `--args="--json-only --candidate"`
also explores reuse of the existing conditional UTF-8 reader, not the selected
HTTP implementation. Results with U+FFFD or long text must not be omitted.

`--args=--json-only` 只运行 JSON 响应体对照，新增字节数组和严格解码分配均计入；
`--args="--json-only --candidate"` 额外比较现有条件式 UTF-8 读取器候选，它未用于
HTTP 实现。不能只展示普通小报文而省略 U+FFFD、长文本等结果。

`./gradlew :benchmarkCodecMetadata --offline --no-daemon --warning-mode fail`
compares valid row/array and Int/String/Float column decoding against frozen
pre-bounds readers with the same cursor layout and primitive loops. Fixtures and
equivalence checks are outside timing. The paired JVM harness reports latency
and thread allocations; results do not certify other column types, live network
requests, Android devices or the final release baseline.

元数据基准保留修复前的游标布局和基础解码循环，比较合法行/数组及 Int/String/Float
列，输出耗时和线程分配。fixture 与等价性检查不计时；不据此证明其他列类型、真实
网络、Android 真机或最终发布版本已通过性能门禁。
