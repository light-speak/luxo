<p align="center">
  <img src="https://raw.githubusercontent.com/light-speak/luxo/main/assets/logo.svg" alt="Luxo" width="200" />
</p>

<h3 align="center">@luxojs/client</h3>

<p align="center">
  TypeScript/JavaScript SDK for Luxo — build APIs at the speed of light.
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/@luxojs/client"><img src="https://img.shields.io/npm/v/@luxojs/client.svg" alt="npm" /></a>
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

**Lux** (Latin, *light*) — not a metaphor, but an engineering constraint. Binary encoding is decided at compile time. Field selection flows from client all the way down to SQL.

**O** (*origin*) — everything starts from the schema. Database tables, type definitions, codecs, client SDKs — all grown from a single `.luxo` file.

> **Luxo — One origin. Speed of light.**

## What Does This Package Do?

`@luxojs/client` is the **core transport layer** for Luxo. It connects your frontend (or any JS/TS environment) to a Luxo API server.

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  Your Code  │ ──→ │ @luxojs/client│ ──→ │ Luxo Server  │
│  (React/Vue)│     │ HTTP/2 + WS  │     │  (Luvia)     │
└─────────────┘     └──────────────┘     └──────────────┘
                     JSON (dev)
                     Binary (prod, 3x smaller)
```

## Install

```bash
pnpm add @luxojs/client
```

## Quick Start

```ts
import { FetchTransport } from '@luxojs/client'

const transport = new FetchTransport('http://localhost:4000/luvia', {
  token: 'your-jwt-token',
  timeout: 30000,
  onTokenExpired: async () => {
    const { token } = await refreshToken()
    return token // auto-retry with new token
  },
})

// Every API call is one line
const user = await transport.call('getUser', { id: 1n, $select: 'id, name' })
const posts = await transport.call('listPosts', { page: 1n, pageSize: 20n, $select: 'id, title' })
```

## Features

### HTTP/2 Transport

Uses native `fetch()` and the runtime's HTTP connection management. HTTP/2 multiplexing depends on the runtime, endpoint and negotiated protocol; the SDK cannot force browsers or Node.js to use HTTP/2. JSON mode also requires the native lossless-number APIs described below.

Custom HTTP headers are normalized once at construction. The SDK owns
`Content-Type` and `X-Luxo-Mode` regardless of casing; `setToken()` replaces the
authorization header and `setToken(null)` clears it. Requests reuse immutable
per-mode snapshots, so token updates do not change already-dispatched headers.
JSON parameters cannot replace the API name passed to `call()`.

```ts
const transport = new FetchTransport('https://api.example.com/luvia')
```

### WebSocket — Real-time Subscriptions

Connection auto-reconnect uses exponential backoff (1s → 2s → 4s → ... → 30s max).
It does not replay subscriptions: disconnect reports `NetworkError`, and callers
must explicitly create a new subscription if they want to resume.

```ts
import { WsTransport } from '@luxojs/client'

const ws = new WsTransport('wss://api.example.com/ws', {
  token: 'your-jwt-token',
})
```

### Binary Mode — 3x Smaller Than JSON

JSON in dev for easy debugging. Switch to binary in prod — same API, zero code changes:

```ts
import { LUXO_SCHEMA } from './luxo/schema' // from @luxojs/vite-plugin codegen

transport.setMode('binary')
transport.setSchema(LUXO_SCHEMA)

// Same call, 3x less bytes on the wire
const user = await transport.call('getUser', { id: 1n, $select: 'id, name' })
```

### Exact Integers in JSON and Binary

Schema `Int` and `Duration` use `bigint`, including input/output arrays, nullable
values and pagination fields. Their range is `-9223372036854775808n` through
`9223372036854775807n`. Do not convert identifiers through `Number`: precision
may already be lost before the SDK receives the value.

Generated clients register `LUXO_SCHEMA` automatically. In JSON mode, HTTP,
WebSocket calls/subscriptions and WeChat reject non-bigint or out-of-range
schema integers before sending. Integer paths are compiled at `setSchema`,
shared by structured type, and checked without rebuilding schema metadata or
adding another serialization pass. Binary encoding retains its existing checks;
it does not run the JSON validator. Structured JSON parameter nesting is bounded
at 128 levels. Missing/defaulted fields remain absent; null remains distinct.

Raw JSON transport calls without a schema cannot infer which values are Luxo
integers. Register the schema for typed validation. `Float` remains `number`, and
opaque `JSON` fields are not restricted to int64: their larger integer tokens
are preserved by the lossless JSON codec. Fractional/exponent tokens and negative
zero retain their original text as native `LuxoJSONNumber` values (`rawJSON`),
including `1e400`, `1e-400` and high-precision decimals. This also applies to
Binary-embedded JSON and error `data`. Neither decoding nor
`normalizeLuxoJSON` silently converts them to `number`; exact integers keep the
existing safe-number/bigint fast path. Both values of the legacy
`preserveNumberSyntax` argument now preserve number syntax.

```typescript
import { jsonNumber, isJSONNumber, encodeLuxoJSON, floatFromJSON } from '@luxojs/client'

const amount = jsonNumber('0.12345678901234567890123456789')
if (isJSONNumber(amount)) console.log(amount.rawJSON)
encodeLuxoJSON({ amount }) // unquoted original numeric token
const approximate = floatFromJSON(amount) // explicit IEEE-754 rounding
```

`jsonNumber` validates a single numeric token; object/string/boolean fragments
and whitespace are rejected. `int64FromJSON` checks integer syntax and signed
range. `floatFromJSON` permits normal IEEE-754 rounding but rejects overflow and
a nonzero raw value underflowing to zero. Creating a JS `number` yourself cannot
recover precision already lost before the SDK receives it. Business
Int/Duration/Float types are unchanged.

Generated JSON decoders require actual strings for String, DateTime, UUID,
Decimal, enum and Base64 Bytes values, and actual booleans for Boolean values.
These checks also cover array elements and nested selected fields; numbers and
the string `"false"` cannot bypass the declared type. Nullable fields still allow
null, and unselected fields stay absent. DateTime additionally checks RFC3339
syntax, at most nine fractional digits and UTC years 0000–9999; UUID syntax and
enum membership remain separate checks. `stringFromJSON` and `booleanFromJSON` expose
the same checks to handwritten decoders without coercing values.

DateTime remains a string: do not pass it through JS `Date` when exact subsecond
precision matters. Binary carries signed floor seconds plus unsigned nanoseconds;
JSON retains fractional digits without millisecond rounding. `dateTimeFromJSON`
also exposes the strict date check. Regenerate clients with the matching core and
runtime; the old whole-second layout is incompatible. Database precision and
performance are not implied by this protocol contract.

Direct JavaScript `-0` inputs also preserve their sign: `encodeLuxoJSON(-0)`
emits `-0`, not `0`. This applies to nested fields, arrays and Binary-embedded
JSON. Positive zero remains `0`; raw-number tokens retain their own spelling.

通用 JSON 的小数、指数和负零保留原文，整数沿用无损 number/bigint 路径；
Binary 内嵌 JSON 和错误 data 也遵守此规则。用 `jsonNumber` 构造数字，
`isJSONNumber` 判断后读取 `rawJSON`；`encodeLuxoJSON` 仍输出数字而非字符串。
只有显式转成 Float 才发生浮点舍入；溢出及非零值下溢成零会报错。
已在调用方转成不精确 number 的值无法恢复，普通 Int/Duration/Float 类型不变。
直接传入 JavaScript `-0` 也保留符号；嵌套字段、数组及 Binary 内嵌 JSON 一致，
正零仍输出 `0`，原始数字 token 不会被改写。

JSON mode requires native `JSON.rawJSON`, `JSON.isRawJSON`, and `JSON.parse`
reviver source context ([ECMAScript specification](https://tc39.es/proposal-json-parse-with-source/)). A newer TypeScript compiler does not add these runtime
features. Unsupported runtimes fail explicitly instead of rounding values;
verify the target browser, server or mini-program engine before deployment.

String validation also requires native
[`String.prototype.isWellFormed`](https://tc39.es/ecma262/multipage/text-processing.html#sec-string.prototype.iswellformed).
JSON strings, object keys and generic JSON/error data reject isolated UTF-16
surrogates, including escaped values hidden by duplicate keys. Binary String
writes reject the same malformed host strings before UTF-8 encoding can replace
them. Valid surrogate pairs, U+FFFD, literal backslash sequences and opaque Bytes
keep their original meaning. No polyfill or replacement-character fallback is used.

Typed `Bytes` parameters remain `Uint8Array` in application code. JSON transport
projects only schema-declared Bytes paths to Base64 using native
[`Uint8Array.toBase64`](https://tc39.es/ecma262/2026/multipage/indexed-collections.html#sec-uint8array.prototype.tobase64).
The runtime must provide this API when sending Bytes in JSON mode; no implicit
object serialization or fallback is used. Projection does not mutate caller-owned
objects or traverse opaque JSON fields. Binary encoding is unchanged.

业务代码仍用 `Uint8Array` 表示 `Bytes`。JSON 请求只对 Schema 声明的 Bytes 路径
使用原生 `Uint8Array.toBase64` 转为 Base64；缺少该能力时明确报错，不隐式序列化成对象。
转换不修改调用方数据，也不遍历不透明 JSON 字段；Binary 编码不变。

WebSocket call IDs are unsigned transport metadata, separate from signed schema
integers. JSON `$id` and Binary sequences cover `0…18446744073709551615`.
Internally the SDK keeps safe IDs as `number` and switches to `bigint` only beyond
`Number.MAX_SAFE_INTEGER`; callers do not manage this representation. Adjacent
large IDs remain distinct, including out-of-order success/error responses.
Exhaustion throws `RequestIDExhausted` without wrapping or replacing pending
calls. Call parameters cannot override `$id` or `$api`.

Missing, negative, negative-zero, boolean, quoted, fractional, exponent or
overflowing JSON IDs and malformed Binary ID varints are ignored without
consuming a pending call. JSON subscription acknowledgements require `ok: true`
unless they contain an error; successful Binary acknowledgements must have no
trailing payload. These checks do not change business `Int`/`Duration` encoding.

WebSocket 调用编号是独立的无符号协议元数据，JSON/Binary 采用相同 UInt64 范围。
SDK 内部仅在超过安全整数范围后改用 bigint，调用方不需要切换表示；大编号的成功、
错误及乱序响应保持精确匹配。耗尽时明确失败，不复用等待中的编号；非法 ID 不消费
待响应请求，调用参数不能覆盖 `$id/$api`。订阅成功必须有合法确认，不改变业务整数编码。

`src/uint64.bench.ts` retains pre-fix baselines for metadata parsing, counter
allocation and the shared signed/unsigned word writer. Run
`pnpm exec vitest bench src/uint64.bench.ts --run --disableConsoleIntercept`.

Subscriptions now allocate independent UInt64 IDs using the same exact counter.
JSON sends `$id` with the API-name `$sub`; ACK/data/cancellation use the numeric
ID, not the API name. Binary stream frames use `0x10`–`0x16` with a UInt64 prefix.
Multiple subscriptions to one API have distinct identities; repeated old cancel
handles and late frames cannot remove a newer subscription. IDs never wrap.
Transport and generated subscription methods accept an optional `StreamObserver`
after `onData`, with `onComplete()` and `onError(LuxoError)` callbacks. JSON `$end`
and Binary `0x15`/`0x16` terminate only the matching subscription, after its accepted
data, without closing the multiplexed connection. State is removed before invoking
callbacks, at most once; error data retains exact JSON number tokens. Explicit
cancellation is silent, and establishment failures still reject the subscribe Promise.
Disconnect retires all subscriptions before notifying observers. Established
subscriptions receive one `NetworkError`; pending calls and subscription setup
Promises reject without a duplicate observer notification. Connection retries do
not resend old subscriptions or retain their API/params for replay. Old sockets,
frames and cancellation handles cannot affect a new subscription. Explicit
cancel/close is silent for established subscriptions; explicit close immediately
rejects pending work even if the browser's close event arrives later.

An unparseable JSON message (including an invalid root or malformed Unicode)
closes its connection and fails all pending calls/setup and established streams
with `ParseError`. IDs are never guessed from a damaged document. Reconnection
does not replay subscriptions. A valid envelope with a bad business payload still
fails only that subscription with `DecodeError`.

All disconnect observers are notified even if one throws or closes the transport.
A single callback failure is rethrown unchanged; multiple failures are preserved
in an `AggregateError`. They are application failures, not protocol errors.
Reconnect timers are cancelled on explicit close or a new connection attempt;
failed socket construction does not permanently cache a rejected connection.
If a socket is already closing before its close callback arrives, a new request
retires its pending state before replacing the connection.
Reconnect remains bounded by ten attempts and never promises gap-free delivery.

The low-level transport now receives decoding and delivery separately:
`subscribe<T>(api, params, decode, onData, observer?)`. Regenerate clients and
adapt handwritten transports; generated public subscription methods are unchanged.
Only `decode(payload)` is inside the decoding error boundary. A failure retires
that subscription before notifying `onError` once with `DecodeError`, and attempts
remote cancellation without closing the shared connection. Cancellation-send
failure cannot undo local retirement. Late frames and repeated cancel handles
are ignored; other subscriptions and ordinary calls continue.
`onData` and observer exceptions remain application exceptions, not protocol
errors. No reflection, re-encoding, or per-message wrapper object is introduced;
subscription setup captures the decoder and handler once. Allocation and full
request performance acceptance remain separate.

This requires a matching server. Go and all four SDKs implement terminal frames;
all four SDKs now terminate subscriptions on disconnect. TS implements typed
decoder failure isolation; the corresponding Dart/Kotlin/Swift migration and
separate performance gates remain pending, so the cross-SDK stream contract is not frozen. Application
callback exceptions are not swallowed by the JSON frame parser.

Scoped speedups do not waive JSON-validation or final request-level performance
gates; full live Go/SDK acceptance remains required before protocol freeze.

### 401 Auto-Refresh

For an HTTP 401 with a valid Luxo error envelope whose `code` is also 401, the
SDK calls your callback and retries at most once. Malformed bodies, failed body
reads and mismatched status/code values never refresh or resend a request.
Returning `null` preserves the original error; a thrown refresh error propagates
unchanged. The retry retains the request's original JSON/Binary mode even if the
callback changes the mode for future calls.

```ts
const transport = new FetchTransport(endpoint, {
  onTokenExpired: async () => {
    const res = await fetch('/auth/refresh')
    return res.json().then(r => r.token) // null = give up
  },
})
```

HTTP、WebSocket 与微信共用严格错误字段解码：`error`、`message` 必须为字符串，
`code` 必须为安全整数，`traceId`/`cause` 只能缺省、为 null 或字符串；非法值报告
`ParseError`，不补默认值。`data` 中的全宽整数保持精确。错误码的当前安全整数边界
不是业务 `Int` 的限制，跨 SDK 错误码范围仍需在协议冻结前统一。

Fetch 仅在 HTTP 状态和合法错误体的 code 均为 401 时刷新，最多重试一次；读响应体
失败或解析失败不触发刷新，重试保留原传输模式。Binary 错误还拒绝重复字段、非法
UTF-8、截断和尾随数据。行式、列式字符串和 Binary 内的 JSON 字节同样严格解码，
保留合法的开头 U+FEFF，不用替代字符掩盖非法编码；原始 Bytes 不做文本校验。
Fetch JSON 也从原始字节严格解码，不受 Content-Type 中 charset 影响；保留原有
文档开头 BOM 接受行为，但不删除字符串值里的 U+FEFF。非法成功/错误响应均报告
ParseError，不触发刷新。已有真实本机 HTTP 分块回归；微信宿主文本及浏览器/最低
版本的完整验收仍须单独进行，不由 Node 测试推出。

HTTP, WebSocket and WeChat share strict error-field decoding. Required `error`
and `message` fields must be strings and `code` a safe integer; optional
`traceId`/`cause` accept only absent, null or string values. Invalid fields yield
`ParseError`, not substituted defaults. Full-width integers inside `data` remain
exact. The current error-code limit does not constrain schema `Int`; the common
cross-SDK error-code domain remains a protocol-freeze requirement. Binary errors
also reject duplicates, invalid UTF-8, truncation and trailing data. Row/column
strings and JSON bytes inside Binary also use strict UTF-8 decoding, preserve a
leading U+FEFF, and never mask invalid encoding with replacement characters.
Opaque Bytes remain bytes. Fetch JSON also strictly decodes raw UTF-8 bytes,
regardless of a declared charset. It retains the existing document-leading BOM
acceptance without deleting U+FEFF inside string values. Malformed success/error
bodies yield ParseError without token refresh. Actual loopback HTTP tests cover
chunked text; WeChat host text and browser/minimum-version acceptance still
require separate validation beyond Node tests.

### Field Selection — End to End

The magic of Luxo: your client selects fields, the server only serializes those fields, and the SQL only queries those columns.

```ts
// With @luxojs/vite-plugin, this happens automatically at compile time
const user = await transport.call('getUser', {
  id: 1n,
  $select: 'name, email, posts { title }',
})
// SQL: SELECT name, email FROM users WHERE id = 1
// + DataLoader: SELECT title FROM posts WHERE user_id IN (1)
```

Public APIs returning structured values require a non-empty `$select`; omitting
it is a protocol error, not an implicit full projection. The Vite plugin injects
the selection for statically analyzable generated-client calls, including
stream callback payloads, and emits an explicit safe projection for dynamic
usage. Raw transport callers must provide
`$select` themselves. Explicit selections return `Foo<true>` and use
`Selected<T>` (`T | undefined`) for selectable fields, so `undefined` means
unselected while `null` remains an explicitly selected nullable value. Input
DTOs stay strict; shared input/output types generate separate `Foo` and
`FooInput` interfaces.

### Codec Utilities

Binary readers enforce the same metadata limits as Go: 1,000,000 array elements,
10,000,000 columnar rows and 64 MiB advertised arena bytes. Counts are validated
before allocating result arrays, and byte lengths cannot exceed the remaining
payload. These are metadata limits, not narrower ranges for business Int/Duration
values: both JSON and Binary preserve signed 64-bit values as `bigint`.

Low-level readers retain the first error in `decoder.error`; their fallback
values are not successful results. Generated model/list decoders check this
error. `decodeScalarArray` additionally rejects malformed and trailing cell bytes,
so an invalid nested array cannot silently become an empty array. Unsupported
scalar array types fail explicitly.

Low-level binary encoding for custom protocols or advanced use:

```ts
import { Encoder, Decoder } from '@luxojs/client'

const enc = new Encoder()
enc.writeSvarint(42n)
enc.writeString('hello')
enc.writeSvarint(-100n)
enc.writeFixed64(3.14)

const dec = new Decoder(enc.bytes())
dec.readInt()      // 42n
dec.readString()   // 'hello'
dec.readInt()      // -100n
dec.readFloat()    // 3.14
if (dec.error !== null) throw new Error(dec.error)
```

## Ecosystem

| Package | Description |
|---------|-------------|
| **[`@luxojs/client`](https://www.npmjs.com/package/@luxojs/client)** | Core transport + binary codec |
| [`@luxojs/react`](https://www.npmjs.com/package/@luxojs/react) | React hooks — `useLuxoQuery` / `useLuxoMutation` |
| [`@luxojs/vite-plugin`](https://www.npmjs.com/package/@luxojs/vite-plugin) | Compile-time `$select` injection + typed client codegen |
| [`luxo_client`](https://pub.dev/packages/luxo_client) | Dart/Flutter SDK |

## vs. Others

| | Axios / fetch | Apollo Client | tRPC | **@luxojs/client** |
|---|---|---|---|---|
| **Binary protocol** | ❌ | ❌ | ❌ | ✅ JSON + Binary |
| **Field selection** | ❌ | ✅ (GraphQL) | ❌ | ✅ Compile-time |
| **WebSocket** | ❌ | ✅ | ✅ | ✅ Auto-reconnect |
| **Auto 401 refresh** | ❌ manual | ❌ manual | ❌ | ✅ Built-in |
| **Bundle size** | ~2KB | ~40KB | ~10KB | ~15KB |
| **Schema → SDK** | ❌ | Codegen needed | ✅ | ✅ Zero-config |

## Build and verification

`pnpm build` bundles JavaScript with tsdown and emits declarations from source with
the TypeScript 7 CLI. The CJS (`dist/index.js`) and ESM (`dist/index.mjs`) entries
remain separate; `dist/index.d.ts` is the public declaration entry. The bundler is
a development dependency, not part of the SDK runtime.

Run `pnpm typecheck` for strict production-source checks and `pnpm test` for the
unit suite. The React and Vite plugin consumer suites also verify built client
packages in CJS/ESM applications. Run `pnpm exec vitest bench --run` separately
from builds and other tests. The HTTP benchmark checks a successful response
before measuring; framework changes require comparable baselines, not comparisons
between different Vitest versions.

`src/codec-metadata.bench.ts` exports `measureMetadata(before, after)` and
`measureText(before, after)` for two
separately TS-compiled codec snapshots. It compares warmed, alternating batches
without Vite runtime wrappers. Keep snapshots under the repository `.tmp/`, use
the same compiler/runtime for both, and run measurements without concurrent
builds/tests. Results include decoder construction and array/cell reads, but do
not establish allocation, browser, network or release-SHA performance gates.

`src/json-params.bench.ts` also requires native CommonJS modules, supplied by
`LUXO_BENCH_MODULES`; compile into a dedicated directory under the core checkout's
`.tmp/` with TS 7 (`--module Node16 --moduleResolution Node16`,
`--verbatimModuleSyntax false --emitDeclarationOnly false --declaration false`).
The Bytes-free comparison isolates `validate` versus `prepare` on the same integer
plan, not the entire change from a release revision. Never benchmark the old
incorrect object encoding of Bytes as an equivalent successful request.

## Links

- [Luxo Framework](https://github.com/light-speak/luxo) · [中文文档](https://github.com/light-speak/luxo/blob/main/README_CN.md)
- [Why "Luxo"?](https://github.com/light-speak/luxo#why-luxo) — The origin story
- [Full comparison table](https://github.com/light-speak/luxo#why-not) — vs GraphQL, gRPC, REST

## License

Apache-2.0 · Copyright 2026 [light-speak](https://github.com/light-speak)
