<p align="center">
  <img src="https://raw.githubusercontent.com/light-speak/luxo/main/assets/logo.svg" alt="Luxo" width="200" />
</p>

<h3 align="center">luxo_client</h3>

<p align="center">
  Dart/Flutter SDK for Luxo — build APIs at the speed of light.
</p>

<p align="center">
  <a href="https://pub.dev/packages/luxo_client"><img src="https://img.shields.io/pub/v/luxo_client.svg" alt="pub" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#features">Features</a> ·
  <a href="https://github.com/light-speak/luxo/blob/main/README_CN.md">中文文档</a>
</p>

---

## Why Luxo?

**Luxo** /lɑːkèsuǒ/ — the path from database to client should be short. Instead, we turned it into a maze.

JSON repeats every field name on every response — like a memo that prints the letterhead on every line. GraphQL parses queries at runtime that were already hardcoded at compile time. ORMs reflect over structs again and again, doing work the compiler finished long ago. `SELECT *` fetches entire rows, only to throw most of them away by hand.

Every layer re-discovers what the layer before it already knew.

We started with one question: **from storage to screen, what is the minimum number of steps — and the minimum number of bytes at each step?**

**Lux** (Latin, *light*) — not a metaphor, but an engineering constraint. Binary encoding is decided at compile time. Field selection flows from client all the way down to SQL. Data should arrive the way light does: no detours, no waste.

**O** (*origin*) — everything starts from the schema. Database tables, type definitions, codecs, client SDKs — all grown from a single `.luxo` file. No second source of truth.

> **Luxo — One origin. Speed of light.**

## What is Luxo?

Luxo is a **programming language** that compiles to Go — with a built-in API framework, its own protocol, and a complete toolchain from schema to deployment.

Write `.luxo` files. Get: API server, database layer, client SDKs, migrations, monitoring dashboard. No glue code.

```luxo
model User @crud {
  name:     String @filterable
  email:    String @unique
  password: String @hidden @hash
  role:     Role = Role.USER
  posts:    [Post]
}
```

One file. Zero boilerplate. This generates everything — including the Dart client you're about to use.

## What Does This Package Do?

`luxo_client` is the **Dart/Flutter SDK** for Luxo. It connects your mobile app, Flutter web, or Dart backend to a Luxo API server.

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  Your App   │ ──→ │  luxo_client │ ──→ │ Luxo Server  │
│ (Flutter)   │     │ HTTP + WS    │     │  (Luvia)     │
└─────────────┘     └──────────────┘     └──────────────┘
                     JSON (dev)
                     Binary (prod)
```

## Typed query controls

For a generated API `listUsers` exposing an Int `count` filter and sorter, the generated query helpers preserve the full Int64 range:

```dart
import 'package:luxo_client/luxo_client.dart';
import 'generated/queries.dart';
final filters = [LuxoQuery_listUsers.count_eq(LuxoInt64.parse('9007199254740993'))];
final sorters = [LuxoQuery_listUsers.count_desc()];
```

Pass these values to the generated API's `filters` / `sorters` arguments. Helpers come from the API's advertised capabilities, not all fields on its return model. Only advertised operators are generated. Dynamic controls remain available; the server still validates every query. These helpers do not perform runtime Schema lookup.

## Install

The development mainline requires Dart 3.13 or newer. Published versions keep
their own SDK constraints; the toolchain migration does not itself publish a new SDK.
JavaScript and Wasm targets also require a host with native
`String.prototype.isWellFormed`; unsupported hosts are not silently downgraded.

The following coordinate is prepared for the unreleased `1.0.0-beta.1` candidate.
Use it after publication; pin the exact version during the first protocol freeze.

```yaml
dependencies:
  luxo_client: 1.0.0-beta.1
```

```bash
dart pub get
```

## Quick Start

For application code, prefer the [generated typed client](#code-generation).
This low-level JSON example shows the actual transport API. Run statements with
`await` inside an `async` function and replace the endpoint/token with your own.
`Int` and `Duration` use `LuxoInt64` in both JSON and Binary; full-width values
must not pass through a floating-point number.

```dart
import 'package:luxo_client/luxo_client.dart';

final transport = HttpTransport(
  'http://localhost:4000/luvia',
  options: const TransportOptions(token: 'your-jwt-token'),
  timeout: const Duration(seconds: 30),
);

try {
  final user = await transport.call<Map<String, Object?>>(
    'getUser',
    params: {
      'id': LuxoInt64.parse('9223372036854775807'),
      r'$select': 'id, name',
    },
  );
  print(user['id']);
  final posts = await transport.call<Map<String, Object?>>(
    'listPosts',
    params: {
      'page': LuxoInt64.fromInt(1),
      'pageSize': LuxoInt64.fromInt(20),
      r'$select': 'title',
    },
  );
  print(posts['items']);
} finally {
  transport.close();
}
```

Use an existing user ID in your service; the maximum value above illustrates
precision, not pre-populated sample data. Manual JSON calls without a registered
schema depend on server validation; generated clients register their schema.

## Features

### HTTP Transport

Full-featured HTTP client with JSON and binary modes:

The default native `package:http` client uses HTTP/1.1. Web builds use the
browser's negotiated protocol. `client:` accepts a compatible HTTP backend;
Binary mode selects the Luxo payload format, not the HTTP version.

```dart
final transport = HttpTransport(
  'https://api.example.com/luvia',
  options: const TransportOptions(token: 'jwt-token', mode: TransportMode.json),
  timeout: const Duration(seconds: 30),
);
try {
  final user = await transport.call<Map<String, Object?>>(
    'getUser',
    params: {'id': LuxoInt64.fromInt(1), r'$select': 'name'},
  );
  print(user['name']);
} finally {
  transport.close();
}
```

### Native HTTP/2

Native Dart/Flutter can explicitly use the SDK's HTTP/2 candidate:

```dart
import 'package:luxo_client/http2.dart';
import 'package:luxo_client/luxo_client.dart';

final origin = Uri.parse('https://api.example.com/luvia');
final transport = HttpTransport(
  origin.toString(),
  client: LuxoHttp2Client(origin),
  options: const TransportOptions(mode: TransportMode.binary),
);
// Register the generated schema/client before calling APIs.
// Close transport when its owner is disposed.
transport.close();
```

This optional backend uses one TLS connection per client, requires ALPN `h2`,
and restricts requests to the configured origin. It defaults to 16 concurrent
requests and 2 MiB per request/response body; excess work fails without an
unbounded queue. It buffers complete unary responses and does not automatically
retry, follow redirects, or fall back to HTTP/1.1. `HttpTransport` deadlines cancel
only the affected request; closing it cancels outstanding work. Browser builds
should keep the default client and must not import the native `http2.dart` entry.
Only buffered `Request`/`AbortableRequest` bodies are supported, as used by
`HttpTransport`; arbitrary streaming uploads are rejected.
Functional TLS checks have passed; performance acceptance is still pending.
HTTP/1.1 remains the default.

All `HttpTransport` backends also enforce
`TransportOptions(maxResponseBytes: 2 * 1024 * 1024)` by default. This positive
byte limit is configurable. Oversized streamed responses fail with `ParseError`
and cancel only that request. Injected clients must honor `AbortableRequest`;
backend-specific collection limits still apply independently.

### WebSocket — Real-time Subscriptions

Connection auto-reconnect uses exponential backoff (1s → 2s → 4s → ... → 30s max).
Disconnect terminates existing subscriptions with `NetworkError`; applications
explicitly subscribe again. Reconnection does not replay or recover missed events.

```dart
import 'dart:async';
import 'package:luxo_client/luxo_client.dart';

final ws = WsTransport(
  'wss://api.example.com/ws',
  autoReconnect: true,
);

final first = Completer<Object?>();
try {
  final unsubscribe = await ws.subscribe(
    'watchPosts',
    {r'$select': 'id, title'},
    (data) {
      if (!first.isCompleted) first.complete(data);
    },
    decoder: (data) => data,
    onError: (error) {
      if (!first.isCompleted) first.completeError(error);
    },
  );
  try {
    print(await first.future.timeout(const Duration(seconds: 30)));
  } finally {
    unsubscribe();
  }
} finally {
  ws.close();
}
```

The service must define `watchPosts` as a stream API. The transport connects on
the first call/subscription; `subscribe` waits for the server acknowledgement.
This example consumes one event and closes. An application keeps the transport
alive and calls the returned unsubscribe callback when disposing the listener.
The low-level example explicitly passes an identity decoder to receive raw data.
Generated clients instead provide a typed decoder; their public subscription
methods keep the same call syntax. Handwritten `Transport` implementations must
accept the required generic `decoder` separately from the `onData` callback.

### Binary Mode

JSON and Binary share schema types, precision and explicit selections. Generated
clients register the schema and decode either response format into the same
typed result. Payload size depends on the data; Binary has no fixed size ratio.

Generated JSON String and Boolean lists validate every element before returning
or invoking a success callback, including model/input fields and nullable lists.
They are growable typed copies, not lazy cast views that defer errors to callers.
Textual UUID, Decimal and enum lists use the same String-kind check;
this does not validate their formats or enum membership. DateTime also validates
timezone-bearing RFC3339 with at most nine fractional digits and UTC years
0000–9999. It remains a String on VM, JS and Wasm; Binary stores signed floor
seconds plus unsigned nanoseconds, without host DateTime precision loss.
Excess precision or range is rejected, not truncated. The old whole-second wire
is incompatible; database precision and performance require separate verification.
Regenerate clients with
the matching runtime. Opaque JSON retains its separate numeric and null rules.

```dart
import 'package:luxo_client/luxo_client.dart';
import 'package:your_app/src/luxo/client.dart';

final client = LuxoClient.create(
  'https://api.example.com/luvia',
  options: const TransportOptions(mode: TransportMode.binary),
);
try {
  final user = await client.getUser(id: LuxoInt64.fromInt(1), select: 'name');
  print(user.name.value);
} finally {
  client.close();
}
```

Low-level `HttpTransport` callers must call `setSchema(luxoSchema)` with the map
from generated `schema.dart`. Without a decoder, Binary calls return raw
`Uint8List`, not a JSON map; use the generated client for mode-independent DTOs.

### 401 Auto-Refresh

Supply your application's refresh callback; return a new token to retry once,
or `null` to stop. This factory does not implement your authentication service:

```dart
HttpTransport createAuthenticatedTransport(
  String endpoint, {
  required String token,
  required Future<String?> Function() refreshToken,
}) => HttpTransport(
  endpoint,
  options: TransportOptions(token: token),
  onTokenExpired: refreshToken,
);
```

### Field Selection — End to End

The magic of Luxo: your client selects fields, the server only serializes those fields, and the SQL only queries those columns.

```dart
final user = await transport.call<Map<String, Object?>>(
  'getUser',
  params: {
    'id': LuxoInt64.fromInt(1),
    r'$select': 'name, email, posts { title }',
  },
);
print(user);
```

The server combines this explicit selection with business-logic fields and
relationship keys. SQL may therefore include required primary/foreign keys
that are not part of the selected response; omitted selection is not “all fields”.

Generated output fields use `Selected<T>`. Check `isSelected` before reading
`value`; a selected nullable field may still contain `null`. Input DTOs remain
strict, and a shared input/output type generates `Foo` plus `FooInput`.

### Binary Codec

Low-level varint/svarint/fixed64 encoding for custom protocols:

```dart
final enc = LuxoEncoder();
enc.writeVarint(42);
enc.writeString('hello');
enc.writeSvarint(LuxoInt64.fromInt(-100));
enc.writeFixed64(3.14);

final dec = LuxoDecoder(enc.bytes());
final metadata = dec.readUnsigned(); // matches writeVarint, not signed zigzag
final text = dec.readString();
final integer = dec.readInt();       // LuxoInt64, matches writeSvarint
final floating = dec.readFloat();
if (dec.error != null || dec.offset != enc.bytes().length) {
  throw FormatException(dec.error ?? 'trailing bytes');
}
print('$metadata $text $integer $floating'); // 42 hello -100 3.14
```

## Code Generation

Generate a typed client from your running Luxo service:

```bash
dart run luxo_client:generate \
  --endpoint http://localhost:4000/luvia \
  --key YOUR_INTROSPECTION_KEY \
  --out lib/src/luxo
```

This generates:

- **types.dart** — typed model classes with binary decoders
- **schema.dart** — API metadata for Binary encoding and JSON parameter validation
- **client.dart** — typed client with methods per API

```dart
import 'package:luxo_client/luxo_client.dart' show LuxoInt64, TransportOptions;
import 'package:your_app/src/luxo/client.dart';

final client = LuxoClient.create(
  'http://localhost:4000/luvia',
  options: const TransportOptions(token: 'your-jwt-token'),
);

try {
  final user = await client.getUser(id: LuxoInt64.fromInt(1), select: 'name');
  print(user.name.value);
  final posts = await client.listPosts(
    page: LuxoInt64.fromInt(1),
    pageSize: LuxoInt64.fromInt(20),
    select: 'title',
  );
  print(posts.items.length);
} finally {
  client.close();
}
```

## Compile-time Field Tracking

Luxo resolves Dart types and injects selections into individual call sites in an
isolated project snapshot. Your original source files are not modified. The
default generated client location is `lib/src/luxo/client.dart`.

```bash
dart run luxo_client:build --entry bin/main.dart
```

Export a native AOT executable from the same call-site-transformed snapshot:

```bash
dart run luxo_client:build --entry bin/main.dart --compile exe --output .tmp/release/app
```

The output is project-relative, must not already exist, and is published only
after compilation succeeds. The temporary source snapshot is removed; the
executable remains runnable and the original business source is unchanged.
This is Dart native AOT, not Flutter packaging or cross-compilation.

Your code, for an API with an `Int` parameter named `id`:

```dart
final user = await client.getUser(id: LuxoInt64.fromInt(1));
print(user.name.value);
```

This invocation receives `select: 'name'`. Another invocation of `getUser` that
reads only `id` receives `select: 'id'`, not their union. The
server combines it with fields required by business logic and associations.

The analyzer follows local aliases, nested relations, null-aware access, list
indexing/iteration, pagination items and inline collection/Future callbacks.
`Selected.value`, collection helpers and pagination metadata are not schema
fields. Selections belong to resolved invocations, not API-wide hint tables;
same-named variables and other client libraries cannot contaminate them.
Dart 3.13 primary-constructor fields are supported through resolved types.

An explicit `select:` always takes precedence. This is static analysis, not
whole-program tracing: opaque helper calls and dynamic result usage require an
explicit selection. Custom SDK output locations use `--client lib/path/client.dart`.
Unsafe or empty inference fails instead of selecting all fields. The old
build_runner hint builder is disabled by default and rejects explicit use with a
migration diagnostic. Use the call-site build entry above; API-wide hint output
is not consumed by generated clients.

For Flutter tooling, the machine-protocol entry runs against the same isolated
snapshot and refreshes selections before forwarding each `app.restart` command:

```bash
dart run luxo_client:flutter --flutter /path/to/flutter -- -d macos
```

This entry uses Flutter's JSON machine protocol, not interactive `r`/`R` keys.
Invalid source rejects the restart without replacing the last valid snapshot;
the running application can reload again after the source is corrected.
Dart VM execution, macOS hot reload and macOS Release execution have been exercised
with a generated client. Release verification includes per-call JSON/Binary selections
and exact Int64 values against a real Go service and gateway. Use `app.stop` to end
the machine-protocol application; input EOF is forwarded and owned snapshots are cleaned.
IDE breakpoint mapping, artifact export/signing and other Flutter devices remain
separate acceptance work, not completed production-readiness claims. Resolve
dependencies in the original project first; the staged runner uses `--no-pub`.

## JSON parameter precision

Opaque JSON preserves every accepted numeric token on VM, JavaScript and Wasm.
Exact integers retain the existing portable int/BigInt representation;
fractional/exponent tokens and negative zero use `LuxoJSONNumber.literal` without
first converting to double. This applies to Binary-embedded JSON and error
`data`, not only JSON transport. The legacy `preserveNumberSyntax` argument no
longer enables a lossy mode; `normalizeLuxoJSON` checks depth without converting
numbers or copying/mutating caller-owned collections.

```dart
final amount = LuxoJSONNumber.parse('0.12345678901234567890123456789');
print(amount.literal);
print(encodeLuxoJSON({'amount': amount})); // original unquoted numeric token
print(amount.toDouble()); // explicit IEEE-754 rounding
print(LuxoJSONNumber.parse('9223372036854775807').toInt64());
```

Construction accepts exactly one numeric token, not whitespace or an unchecked
JSON fragment. `toInt64` checks integer syntax and signed range; `toDouble`
allows IEEE-754 rounding but rejects overflow and nonzero underflow to zero.
Neither conversion changes the stored literal. Host doubles already rounded by
application code cannot regain precision. Business Int/Duration/Float types stay
unchanged.

Generated `[JSON]` values use `List<Object?>`: an element may be JSON null even
when the list itself is required. A nullable list uses `List<Object?>?`. Inputs,
row/columnar model fields, API replies and subscriptions follow the same rule;
regenerate older clients that emitted `List<Object>`.

通用 JSON 的整数沿用无损 int/BigInt，小数、指数和负零通过
`LuxoJSONNumber.literal` 保留原文；VM/JS/Wasm、Binary 内嵌 JSON、错误 data
使用同一规则。`LuxoJSONNumber.parse` 只接受一个合法数字，发送时仍是数字 token，
不是引号字符串。显式 `toInt64()` 校验整数语法与范围，`toDouble()` 允许 IEEE-754
舍入，但溢出或非零值下溢到零会报错；不会改写原文。普通业务类型不变。

`[JSON]` 生成 `List<Object?>`：列表必填不代表 JSON 元素不能为 null；外层可空时
才使用 `List<Object?>?`。输入、行式/列式字段、API 与订阅一致，旧版生成
`List<Object>` 的客户端需要重新生成。

JSON and Binary share the signed Int/Duration range and precision. Use
`LuxoInt64.fromInt` for portable safe integers and `LuxoInt64.parse` for full-width
values. The VM's native `int` representation is not a portable replacement for
`LuxoInt64` in handwritten Web transport parameters.

With a registered schema, HTTP JSON calls, WebSocket calls and subscriptions
validate supplied integer parameters, nullable lists and nested input fields
while writing JSON. They do not truncate floats or coerce booleans, strings or
raw BigInt values into schema integers. Nullable lists may be null, not contain
null integer elements. Invalid parameters fail before sending with a
`ConfigError` and a path such as `input.children[1].id`. Non-finite opaque JSON
numbers still throw `FormatException`, not a misleading HTTP `NetworkError`.

Registration validates API/parameter/field IDs and owns immutable metadata for
both modes before replacing the schema. Invalid or duplicate IDs throw
`FormatException`; failed registration preserves the previous schema and JSON
plan. Shared field/type directories stay shared within each snapshot. It compiles
only reachable input metadata. Changes take effect through `setSchema`, not by
mutating a previously registered map. Recursive schemas use a work queue; input
depth is bounded. Generated input objects are projected once, without reflection
or an extra JSON encoding/decoding pass. Missing nested field types require
regenerating the client and fail explicitly when those fields are supplied.
Absent parameters and schema-free manual JSON calls still require server
validation; this is not a replacement for full server-side validation.

JSON 与 Binary 的 Int/Duration 范围和精度一致。安全整数使用
`LuxoInt64.fromInt`，完整 64 位值使用 `LuxoInt64.parse`；不要把 VM 中直接使用
原生 int 的行为当成 Web 手工参数的可移植用法。注册 Schema 后，HTTP JSON、
WebSocket 调用和订阅在写入 JSON 时校验整数、可空列表和嵌套输入，不将浮点数、
布尔值、字符串或原始 BigInt 强转成整数。可空列表不允许整数元素为 null；错误在
发送前返回，包含 `input.children[1].id` 这样的路径。普通 JSON 中的非有限数仍报
`FormatException`，不再误报 HTTP `NetworkError`。

登记时检查API/参数/字段编号并保存两种传输共用的不可变元数据快照；非法或重复
编号抛出`FormatException`，登记失败保留旧Schema与JSON参数计划。共享字段/type
目录在快照内继续共享，只编译可达输入元数据。修改后须重新调用
`setSchema`，不要依赖原 Map 的后续修改。递归 Schema 通过工作队列编译，输入
深度有界；生成对象仅转换一次，无反射和额外 JSON 编解码往返。旧客户端缺少嵌套
字段类型时需重新生成，传入这些字段会明确报错。未传参数及无 Schema 的手工
JSON 调用仍由服务端校验，不以此替代完整服务端校验。

Generated inputs implement `LuxoJSONParameters.toLuxoParameters()`: request
projection retains `LuxoInt64`, its arrays and nested typed inputs. Public
`toJson()` still provides the generic JSON representation; it is not used as an
intermediate transport conversion. Regenerate existing clients to obtain this
projection and correct scalar parameter mapping when introspection includes
`typeName`. Custom `LuxoJSONEncodable` implementations remain supported under
the existing typed-parameter validation rules.

生成输入通过 `LuxoJSONParameters.toLuxoParameters()` 直接保留 `LuxoInt64`、
整数数组和嵌套输入；公开 `toJson()` 的通用 JSON 表示不变，不再作为传输的中间
转换。旧生成客户端应重新生成，同时修复真实 Schema 含 `typeName` 时的标量参数
映射。自定义 `LuxoJSONEncodable` 继续遵循已有的强类型参数校验。

### Real Go interoperability / 真实 Go 联测

Tests and benchmarks are distributed in the source repository, not the pub package. Run the following development commands from `packages/dart` in a repository checkout.

`dart test test/go_interop_test.dart --reporter expanded` builds a real service
from [the shared Luxo fixture](../../tests/sdk-interop), fetches live schema,
generates the Dart client and exercises HTTP/WebSocket in JSON/Binary on VM,
JavaScript and Wasm. It needs the repository's Go and Node toolchains, but no DB.
The loopback service and its temporary generated files are closed/removed by the
test. This suite does not cover pagination, multi-service routing or other SDKs.

上述命令从共享 `.luxo` 编译真实 Go 服务，并从在线 Schema 生成 Dart 客户端，在
VM/JS/Wasm 分别验证两种编码与 HTTP/WebSocket。需要 Go、Dart、Node，不需要
数据库；测试后关闭回环服务并清理临时产物。分页、多服务与其他 SDK 不由本套件
证明。该联测已纳入完整 `dart test` 和 CI。

## Executable documentation checks

```sh
dart test test/readme_test.dart
```

The test extracts the nine Dart examples above into separate libraries,
preserving their imports and checking against a freshly generated client.
Only application-package paths, endpoints and the surrounding async/lifecycle
context are supplied by the fixture. It runs the real selection-hint builder,
strict analysis and loopback HTTP/WebSocket calls, including full-width JSON,
Binary decoding, pagination and one-time token refresh. It also checks that
type-only Bytes/JSON schemas retain their required conversion imports while
ordinary/unused types do not add unused imports. Existing installed test tools
are reused; this is not a fresh package installation or live Go-server test.

测试直接提取上文九段 Dart 代码，保留各自 import，配合真实生成客户端和字段选择
构建器进行严格分析与回环 HTTP/WebSocket 验证；只补充应用包路径、测试地址及
async/生命周期上下文。覆盖全宽 JSON 整数、Binary 解码、分页和一次刷新重试，
同时检查生成器按实际字段保留或省略转换库导入。复用已安装工具，不把此测试当作
全新环境安装或真实 Go 服务验收。

## Web and Wasm verification

Binary readers enforce the same allocation bounds on VM, JavaScript and Wasm:
1,000,000 array elements, 10,000,000 columnar rows and a 64 MiB arena. Counts
are checked before allocating results; blob lengths must fit the remaining
payload. Fixed-width columns preflight the entire column. Business
`Int`/`Duration` and pagination values retain their full signed 64-bit range.
Boolean and nullable markers use exactly one byte (`0` or `1`).

Generated scalar/scalar-array API responses and scalar-array cells check both
decoder errors and complete consumption; truncated input cannot become a
successful zero, false or empty array, and trailing bytes fail. Inline row fields
share their parent cursor and do not require that the entire row end after each field.
`Enum` arrays use the same string-element representation in JSON and Binary.
Regenerate clients to pick up generated cell checks and Enum-array handling.

VM、JS、Wasm 统一检查数组 100 万元素、列式 1000 万行和 arena 64 MiB 上限，
分配前检查最小负载，变长单元格检查实际长度；固定宽度列整列预检。业务 Int、Duration
和分页值仍保留完整 signed int64 范围。Boolean/nullable 标记必须是单字节 0 或 1。
生成的顶层标量/标量数组响应和独立数组 cell 同时检查内部错误及剩余字节，不把损坏
数据当作 0、false 或空数组成功返回。行内字段共用父游标，不逐字段要求整行结束。
Enum 数组在 JSON/Binary 中均使用字符串元素；需重新生成客户端以获得这些修复。

Named enum declarations now generate a sealed type (for example, `TaskStatus`),
`TaskStatusKnown` constants and `TaskStatusUnknown` values carrying `rawValue`.
Use `TaskStatus.fromValue(text)` when reading application text; known members
resolve to constants, while unknown text is preserved without becoming a known
member. Calls, subscriptions, parameters and model fields use this type or its
list form; scalar enum results still do not require `$select` or model decoders.
Regenerate clients and replace raw string arguments with the generated values.
Typed writers send `rawValue` with the unchanged string wire encoding; the server
still rejects members absent from its own Schema. Generic JSON does not implicitly
convert enum objects: explicitly pass `rawValue` when that is the intended data.
Enum fields and parameters require `typeName` referencing a declared enum; API
results require the concrete enum name, not bare `Enum`. Invalid or missing
enum metadata fails generation before existing client files are overwritten.
Minimal scalar-only, input-only, empty and internal-only schemas pass strict
analysis without unused imports or selection helpers. Generated subscriptions
validate complete scalar/list payloads before invoking `onData`; tests include
truncation, trailing bytes, invalid Boolean/UTF-8/JSON and overflowing integers.
Generic JSON number representation is unchanged.

命名枚举现生成 sealed 类型（如 `TaskStatus`）、已知常量 `TaskStatusKnown` 和
携带 `rawValue` 的 `TaskStatusUnknown`。`TaskStatus.fromValue(text)` 将已知成员
映射到常量，未知原文明确保留，不冒充已知值。调用、订阅、参数和模型字段使用该
类型或其列表；需重新生成客户端并替换原来的字符串参数。线上仍编码为字符串，
服务端仍拒绝自身 Schema 未声明的成员；通用 JSON 不会隐式转换枚举对象，如需
原文应显式传入 `rawValue`。标量枚举不会误走模型解码或要求 `$select`。
Enum字段和参数必须带有指向枚举声明的`typeName`，API返回值必须写具体枚举名，
不能使用裸`Enum`。缺失或无效元数据在生成文件前报错，不覆盖已有客户端。
仅标量、仅输入类型、空 Schema
及仅内部 API 的最小项目均通过严格分析，不生成未使用的导入或字段选择辅助函数。
生成的订阅在触发 `onData` 前完成标量/列表负载校验；回归包含截断、尾随字节、非法
Boolean/UTF-8/JSON 和整数溢出。通用 JSON 数值表示不变。

Binary string fields preserve leading U+FEFF and literal U+FFFD in row,
columnar, nullable, array and error-message paths. Malformed UTF-8 sets the
decoder's first error, stops subsequent string reads and discards partial
string arrays; complete-response checks reject the result. Raw Bytes remain
opaque. This does not change HTTP/JSON document BOM handling or generic JSON
number representation. The implementation uses the platform strict UTF-8
decoder, not a handwritten Unicode validator.

Binary 字符串在行式、列式、nullable、数组和错误消息中保留开头的 U+FEFF 及
合法 U+FFFD。非法 UTF-8 保留首个解码错误，后续字符串读取停止，坏数组不返回
部分结果；完整响应检查明确拒绝该结果。Bytes 不按文本处理，HTTP/JSON 文档
BOM 和通用 JSON 数值表示不变。读取复用平台严格解码器，不手写 Unicode 校验器。

`benchmark/codec_text_benchmark.dart` measures the former and current readers
separately under native AOT, JavaScript and Wasm. It includes construction,
arrays and completion checks, not network or allocations. Leading-BOM cases
explicitly compare different outputs because the former implementation lost a
character. Added correctness costs are not automatically performance-approved.

文本基准分别测 Native AOT、JavaScript 和 Wasm，包含构造、数组及完成检查，
不代表网络或分配性能。BOM 对照明确标记旧实现会丢字符；新增正确性成本仍须
性能评审，不能用其他场景提速抵扣。

WebSocket JSON `$id` and binary `sequence` preserve the same unsigned 64-bit
range, `0…18446744073709551615`; business `Int`/`Duration` remain signed int64.
Small IDs use a typed `int` counter; only IDs above the portable safe-integer
limit use Dart's existing `BigInt`. Counter state is created once per transport.
Exhaustion fails with `RequestIDExhausted`, without wrapping or replacing a
pending call. Parameters cannot override call `$id`/`$api` metadata.

Subscriptions now have independent UInt64 identities too: subscribe JSON carries
`$id`, and `$sub` acknowledgements/errors, `$stream` data and `$unsub` cancellation
carry that numeric identity, not the API name. Binary uses frames `0x10…0x16`
with a UInt64 prefix; subscribe then carries the canonical API request. Multiple
subscriptions to the same API are independent, cancellation handles are idempotent,
and established subscriptions retain their encoding when the transport mode changes.
Retired `0x04…0x08` frames are not accepted. `subscribe` and generated subscription
methods accept optional `onDone` and `onError(LuxoError)` callbacks. The server drains
accepted data before sending JSON `$end` or Binary `0x15` (complete)/`0x16` (error).
Each subscription is removed before notifying its observer, at most once; errors
retain exact JSON number tokens in `data`. Completion leaves the socket and other
subscriptions open. Explicit cancellation is silent. Establishment errors still
reject the returned Future rather than invoking the terminal callbacks.

Disconnect removes all subscriptions before notifying established observers once
with `NetworkError`; pending calls and subscription acknowledgements fail directly.
Connections can reconnect, but old requests/parameters are not retained or replayed.
Old cancellation handles cannot cancel a new subscription. Explicit `close()` is
silent for established subscriptions and immediately rejects connection waiters,
even before a pending handshake finishes; a socket that opens later is closed.
Exceptions thrown by application callbacks use Dart's uncaught asynchronous error
handling and do not prevent other disconnected observers from being notified.
An unparseable JSON message, invalid root or malformed Unicode closes its
connection and terminates pending calls/setup and established subscriptions with
`ParseError`. The transport never guesses IDs from a damaged document or replays
subscriptions on reconnection. This differs from a valid envelope with invalid
business data, which remains a subscription-local `DecodeError`.
Subscription decoding is separate from application delivery. A typed decoder
failure removes only that subscription, attempts remote cancellation and notifies
`onError` once with `DecodeError`, never `onDone`. Late data and repeated cancellation
are ignored; other subscriptions and unary calls continue. Application `onData`
exceptions stay outside the decoding catch and use the caller's Zone. A decoder
that closes or cancels its own subscription does not receive a second notification.
Cross-SDK migration and performance/protocol gates remain unfinished; this is not
a frozen release.

WebSocket JSON `$id` 与 Binary `sequence` 使用相同的完整 UInt64 范围，业务
Int/Duration 仍为有符号 64 位。普通编号用强类型 int 计数器，只有超过跨平台安全
整数范围后才使用现有 BigInt；计数器状态在每个 transport 初始化时创建一次。
耗尽时明确抛 RequestIDExhausted，不回绕或覆盖等待中的请求；调用参数不能覆盖
`$id/$api`。没有新依赖、反射或额外 JSON 编解码。

Incoming JSON IDs require canonical unsigned integer syntax; quoted values,
booleans, negative zero, fractions, exponents and overflow cannot consume a
pending response. Binary headers enforce the ten-byte uint64 bound and reject
truncation/overflow, retaining an `int` for small IDs without a per-frame cursor
object. Wide encoding uses two words, sharing the existing Web signed-integer
byte loop without per-byte BigInt arithmetic. Other metadata's safe-int
`writeVarint` contract is unchanged; `writeUint64(BigInt)` explicitly writes the
full unsigned range. Successful subscription acknowledgements require JSON
`true` or an empty binary payload.

接收端拒绝字符串、布尔值、负零、小数、指数或溢出的 JSON 编号。Binary 帧头检查
截断和十字节上限，小编号仍保留 int，不为帧头新增游标对象。大编号编码复用 Web
有符号整数的双 32 位字编码循环，不逐字节执行 BigInt 运算。其他元数据的 writeVarint
安全整数契约不变，完整 UInt64 用显式 writeUint64(BigInt) 写入；订阅成功确认必须
是 JSON true 或没有附加 payload 的 Binary 确认帧。

HTTP, WebSocket calls and subscription acknowledgements share strict error
decoding. Malformed fields, invalid UTF-8/JSON, duplicate binary fields and
unrepresentable error codes produce `ParseError`; a correlated response must
not disappear into a timeout. Error `data` keeps exact large integers and raw
fractional/exponent tokens. These checks run only for error responses,
not as an extra traversal or serialization pass on successful requests.

HTTP、WebSocket 调用和订阅确认复用错误解析。字段类型错误、非法 UTF-8/JSON、
Binary 重复字段，以及当前公开 int 错误码不能精确表示的值，均明确返回 ParseError，
不能吞掉已关联响应导致超时。data 保留大整数精度和小数/指数原文；检查仅进入错误
分支，不在成功请求上增加遍历或编解码。错误码目前沿用跨平台安全 int 范围，不能将
这一 SDK 限制当作业务 Int/Duration 的协议范围；跨 SDK 错误码契约仍需冻结前统一验收。

The existing public `int` error code retains the portable safe-integer range;
that SDK limitation does not redefine the business Int/Duration wire domain.
Cross-SDK error-code contract alignment remains a release requirement.

HTTP request mode owns `Content-Type` and `X-Luxo-Mode`; custom headers cannot
override either, regardless of casing. Other headers are snapshotted when the
transport is created, and `setToken` updates authorization in both modes. A
refresh callback is invoked at most once, only after a valid error envelope
with both HTTP status and error code 401. Malformed responses never cause a
refresh or resend; a retry retains the original request mode. JSON response
bytes are decoded strictly as UTF-8, independent of a conflicting charset
header, without a second decoding pass.

JSON strings and object keys must contain complete Unicode characters, including
inside opaque JSON and error data. Lone surrogate escapes are rejected even when
a later duplicate key would overwrite the bad value. Valid pairs, U+FFFD and
literal backslash sequences keep their meaning. Binary String writers reject
incomplete host strings before UTF-8 encoding can replace them; Bytes remains
opaque. Regenerate clients to apply the same checks to typed JSON scalar, list,
model and input readers. These checks do not add reflection or an encode/decode
round trip; they are not a claim of zero serialization or zero allocation.

Generated JSON model/type lists require every record to contain at least one
Schema field. This covers ordinary lists, paginated items, nested lists and
stream data; empty and unknown-only records fail before delivery. Empty lists
and valid selected nullable fields remain valid, and opaque JSON objects are
not subject to model projection rules. Regenerate clients to apply this check;
public API calls and single-object `fromJson` signatures are unchanged.

生成的 JSON 模型/type 列表要求每条记录至少包含一个 Schema 字段，覆盖普通列表、
分页、嵌套列表和流；空记录、仅含未知字段的记录不能进入成功结果。空列表和合法
可空字段仍有效，通用 JSON 对象不受模型投影限制。客户端须重新生成，公开 API
调用方式与单对象 fromJson 签名不变。

HTTP 的 Content-Type/X-Luxo-Mode 由所选模式决定，自定义头不论大小写都不能
覆盖；其余头在创建 transport 时快照，setToken 同时更新两种模式的鉴权。
只有合法错误响应且 HTTP 状态码、错误码均为 401 时，才最多调用一次刷新回调。
非法响应不刷新、不重发，重试保持原请求模式。JSON 响应直接按 UTF-8 严格解码，
不受冲突 charset 影响，也不增加第二次解码。

The browser WebSocket adapter uses `package:web` with static `dart:js_interop`;
the platform selector uses `dart.library.js_interop` for both JavaScript and
Wasm. It does not import `dart:html` or add reflection to the binary codec.
Typed-data views preserve their byte offset and length when sent. Remote close
events complete the message stream so pending calls report a network error.

From this package directory, with Dart 3.13+ and Node 26 installed:

```bash
dart pub get
dart analyze --fatal-infos
dart test
dart test -p node -c dart2js -c dart2wasm -j 1 \
  test/int64_test.dart test/int64_codec_test.dart test/int64_json_test.dart \
  test/socket_web_test.dart test/uint64_test.dart test/error_response_test.dart \
  test/json_parameters_test.dart test/http_protocol_test.dart \
  test/codec_metadata_test.dart test/codec_test.dart test/codec_text_test.dart \
  test/unicode_test.dart
```

VM tests also compile real JS/Wasm consumers and execute JSON/binary calls,
full-width request IDs, counter exhaustion, pre-send parameter rejection,
subscriptions and disconnect handling against a temporary loopback WebSocket
server. Processes, sockets and generated fixture directories are test-owned;
no persistent application server is required. CI runs these checks with both
the current stable Dart release and the declared minimum SDK version.
Node-hosted Web API tests do not establish all-browser or live Go-server
compatibility; those integrations and final performance comparisons remain
separate release checks.

`benchmark/codec_metadata_benchmark.dart` compares valid row, scalar-array and
column decoding against frozen pre-bounds readers, including complete array-cell
validation. Fixtures and output equivalence checks run outside timing. Measure
native AOT, JS and Wasm separately; this is not a compiler-version comparison,
allocation measurement or approval of added per-request validation costs.

浏览器适配器已迁移至 `package:web` 静态互操作，JS/Wasm 共用平台选择；没有给
Binary 编解码加入反射。发送子视图不会泄漏底层数组的其他字节，远端断开会完成流并
通知等待中的请求。上述命令分别验证 VM 和编译后的 JS/Wasm，VM 套件还会启动临时
回环 WebSocket 服务进行实际调用、订阅与断线验证。Node 环境结果不等于所有浏览器
或真实 Go 服务联调已通过，最低版本与最终性能仍按各自门禁验收。

`benchmark/uint64_benchmark.dart` compares counter/ID checks, the extracted
Web signed-integer byte loop and JSON/binary error decoding with frozen baselines. Native AOT, JS and Wasm
must be measured separately; the Web clock uses `performance.now`, not the
coarse dart2js Stopwatch clock. Added validation costs remain subject to the
performance gate. This helper-only benchmark does not measure whole packets,
allocation rates, wide-ID slow paths, browsers or device performance.

Its `writers` argument isolates the byte loop and complete Web signed writer
(word extraction, zigzag, emission), using varying buffer lengths and nonzero
offsets on both sides to prevent a fixed-size-only baseline. The shared emitter
uses JS/Wasm compiler inline hints; type/bounds checks remain enabled, and
regressions verify out-of-bounds writes still fail. This is not a native signed
codec benchmark, even if the Web algorithm is compiled to a native executable.

局部基准独立比较 Native AOT、JS、Wasm，Web 使用高精度时钟。新增检查成本不
自动豁免，也不以其他路径提速抵扣；尚未据此验收整帧、分配、大编号慢路径或设备性能。

## vs. Others

| | dio / http | Chopper | Retrofit | **luxo_client** |
|---|---|---|---|---|
| **Binary protocol** | ❌ | ❌ | ❌ | ✅ JSON + Binary |
| **Field selection** | ❌ | ❌ | ❌ | ✅ Compile-time |
| **WebSocket** | ❌ manual | ❌ | ❌ | ✅ Auto-reconnect |
| **Auto 401 refresh** | ❌ interceptor | ❌ interceptor | ❌ | ✅ Built-in |
| **Codegen** | ❌ | ✅ Annotation | ✅ Annotation | ✅ Schema-driven |
| **Field tracking** | ❌ | ❌ | ❌ | ✅ AST analysis |

## Ecosystem

| Package | Platform | Description |
|---------|----------|-------------|
| [`@luxojs/client`](https://www.npmjs.com/package/@luxojs/client) | npm | TypeScript/JavaScript SDK |
| [`@luxojs/react`](https://www.npmjs.com/package/@luxojs/react) | npm | React hooks |
| [`@luxojs/vite-plugin`](https://www.npmjs.com/package/@luxojs/vite-plugin) | npm | Vite compile-time `$select` |
| **[`luxo_client`](https://pub.dev/packages/luxo_client)** | pub.dev | Dart/Flutter SDK |

## Links

- [Luxo Framework](https://github.com/light-speak/luxo) · [中文文档](https://github.com/light-speak/luxo/blob/main/README_CN.md)
- [Why "Luxo"?](https://github.com/light-speak/luxo#why-luxo) — *Lux (light) + O (origin)*
- [The Language](https://github.com/light-speak/luxo#the-language) — 32 keywords, compiles to Go
- [API Reference](https://pub.dev/documentation/luxo_client/latest/)

## License

Apache-2.0 · Copyright 2026 [light-speak](https://github.com/light-speak)
