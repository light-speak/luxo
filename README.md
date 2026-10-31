<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  A schema-first compiled backend language and platform.<br/>
  One language, one protocol, one toolchain for API and data services.
</p>

<p align="center">
  <a href="https://github.com/light-speak/luxo/actions/workflows/test.yml"><img src="https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg" alt="Tests" /></a>
  <a href="https://codecov.io/gh/light-speak/luxo"><img src="https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg" alt="codecov" /></a>
  <a href="https://goreportcard.com/report/github.com/light-speak/luxo"><img src="https://goreportcard.com/badge/github.com/light-speak/luxo?v=2" alt="Go Report Card" /></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.27.1+-00ADD8?style=flat&logo=go" alt="Go Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="README_CN.md">中文文档</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#the-language">The Language</a> ·
  <a href="#roadmap">Roadmap</a>
</p>

---

The Go core and CLI in 1.0.0-beta.1 support 64-bit platforms only. 32-bit Go targets are not supported or release-tested. This does not change the wire format or its Int64/UInt64 value ranges.

## Why "Luxo"?

**Luxo** /lɑːkèsuǒ/ — the path from database to client should be short. Instead, we turned it into a maze.

JSON repeats every field name on every response — like a memo that prints the letterhead on every line. GraphQL parses queries at runtime that were already hardcoded at compile time. ORMs reflect over structs again and again, doing work the compiler finished long ago. `SELECT *` fetches entire rows, only to throw most of them away by hand.

Every layer re-discovers what the layer before it already knew.

We started with one question: **from storage to screen, what is the minimum number of steps — and the minimum number of bytes at each step?**

Follow that question to its logical end, and you arrive at Luxo.

**Lux** (Latin, *light*) — not a metaphor, but an engineering constraint. Binary encoding is decided at compile time. Query plans are generated at compile time. Field selection flows from client all the way down to SQL. Every layer is pushed toward the physical limit of data transfer. Data should arrive the way light does: no detours, no waste.

**O** (*origin*) — everything starts from the schema, and only from the schema. Database tables, type definitions, codecs, client SDKs — all grown from a single `.luxo` file. No second source of truth. No definitions to keep in sync by hand. Renaming a field doesn't mean touching three files.

> **Luxo — One origin. Speed of light.**

## What is Luxo?

Luxo is a **schema-first compiled backend language and platform**. It compiles `.luxo` into Go services, database access, migrations, typed SDK metadata, and deployment entry points. Luvia exposes the same schema over JSON and Luxo Binary through HTTP, WebSocket, and native RPC.

Luxo is designed to unify workloads commonly split across REST, GraphQL, gRPC, an ORM, and handwritten client glue. PostgreSQL is the implemented database backend today; MySQL, SQLite, and MongoDB remain roadmap targets.

```luxo
model User @crud {
  name:     String @filterable
  email:    String @unique
  password: String @hidden @hash
  role:     Role = Role.USER
  avatar:   String?
  posts:    [Post]
}
```

One source definition drives the generated server, database layer, migrations, schema, and SDK contracts.

## Why Not...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| **Field Selection** | ✅ Supported but too loose — clients can craft arbitrarily deep queries, needs extra depth/complexity limits to prevent abuse | ❌ None — response fixed to proto definition (`FieldMask` exists but requires manual handling) | ❌ None — each endpoint returns fixed fields, `?fields=` requires manual implementation | ✅ Schema-level field visibility, compile-time validation, propagated down to SQL — only selected columns are queried |
| **Binary Transport** | ❌ Spec is encoding-agnostic, but virtually all deployments use JSON — significant serialization overhead at scale | ✅ Protobuf binary encoding — compact and efficient | ❌ Any format via content negotiation in theory, but JSON dominates in practice with no standard binary option | ✅ One schema for JSON and Luxo Binary; the SDK mode or `X-Luxo-Mode` explicitly selects the wire format |
| **One Schema** | ❌ SDL defines the API, but ORM/DB mapping maintained separately — two sources of truth that drift apart (code-first tools can help) | ❌ `.proto` for wire format + ORM for database — two definitions to keep in sync | ❌ No schema-driven workflow — routes, models, and docs all written by hand | ✅ One `.luxo` file generates API, DB migrations, client SDK, and docs — single source of truth |
| **N+1 Prevention** | ❌ Resolver-per-field pattern naturally causes N+1 — requires manual DataLoader integration (standard practice; frameworks like Hasura auto-solve) | N/A — no nested field resolution model | ❌ Nested resources require manual query optimization or eager loading | ✅ Compiler auto-analyzes relations and generates DataLoader batching — no manual intervention needed |
| **Null Safety** | ⚠️ SDL `!` marks non-null with server-side runtime enforcement; client codegen can provide compile-time type safety | ✅ Protobuf fields have default zero values with compile-time type safety (but proto3 can't distinguish "zero" from "unset" without `optional`) | ❌ No null safety — null errors only surface at runtime | ✅ Language-level compile-time null safety: `?` nullable declaration, `?.` safe access, `?:` Elvis fallback — null errors caught before running |
| **Error Handling** | ❌ Loosely-typed `errors` array with message string + optional extensions — hard for clients to handle structurally | ✅ gRPC Status with 16 standard codes + rich error details — well-typed | ❌ HTTP status codes + custom JSON body — no unified error structure standard | ✅ One structured transport error envelope; native `Result<T>` lowers to Go `(T, error)` and `?` propagates failures |
| **Concurrency** | ⚠️ Most implementations auto-parallelize independent resolvers (gqlgen / Apollo / graphql-java), but no language-level concurrency primitives — complex orchestration still depends on the host language | ❌ Supports bidirectional streaming, but concurrency orchestration is entirely manual | ❌ No built-in concurrency — fully depends on framework or manual thread/goroutine management | ✅ Built-in `async` / `await` + `Channel` — compiles directly to Go goroutines and channels, zero-cost concurrency |
| **Multi-service** | ⚠️ Apollo Federation is mature but operationally complex — requires extra gateway layer and cross-service coordination | ⚠️ Native point-to-point RPC, but multi-service orchestration still needs service mesh/discovery (Istio, Consul, etc.) | ❌ Inter-service calls require hand-written HTTP clients or additional frameworks | ✅ `extend` for cross-service type composition + built-in gateway routing + native RPC — multi-service out of the box |
| **Scaling** | ❌ Monolith-to-federation migration requires rewriting resolvers, adding `@key`/`@external` annotations, deploying Apollo Router | ❌ Service split requires redefining `.proto` files, regenerating stubs, rewriting client calls | ❌ Every split means new routes, new HTTP clients, new deployment configs | ✅ The compiler generates embedded and clustered entry points, RPC routing, and federation loaders from the same schema contract |

## The Language

Luxo is a backend programming language, not only a schema DSL. Its scalar types are `Int`, `Float`, `String`, `Boolean`, `DateTime`, `Duration`, `UUID`, `Decimal`, `Bytes`, and `JSON`; generic runtime types include `Result<T>`, `Channel<T>`, `Page<T>`, and `Cursor<T>`. Luxo compiles to Go.

DateTime preserves nanoseconds in both JSON and Binary. JSON requires timezone-bearing RFC3339 with at most nine fractional digits and UTC years 0000–9999; Binary carries floor Unix seconds plus unsigned nanoseconds. Excess precision and out-of-range values are rejected. This pre-release change requires matching regenerated services and SDKs, with no legacy time fallback. Database storage precision and performance are separate acceptance gates. Internal RPC is now version 4, adding explicit nullable root responses; the corresponding four-SDK migration is not complete and the release is not frozen.

### Null Safety

```luxo
val user = User.find(id: 1)          // User (auto-throws NotFound)
val maybe = User.where(id == 1).first() // User? (nullable)
val name = maybe?.name               // safe access
val sure = maybe ?: throw NotFound   // elvis — assert or throw
```

### Pattern Matching — `when`

`when` replaces if/else for multi-branch logic:

```luxo
val level = when(score) {
  in 90..100 -> "A"
  in 80..89  -> "B"
  else       -> "C"
}

when(result) {
  is Ok  -> result.value
  is Err -> throw result.error
}
```

### Result Type + `?` Operator

Errors propagate with `?` — no try/catch, no async/await infection.

```luxo
fn loadUser(id: Int): Result<User> @native

api getUser(id: Int): User {
  loadUser(id)?                      // value → unwrap, error → propagate
}
```

`Result<T>` is the ABI for Go-backed native functions. Public APIs declare their payload type (`User` above); transport errors use the shared structured error envelope.

### Compiled Functions and Native Boundaries

Ordinary functions are compiled into direct module-local Go methods. They can call other compiled functions or cross into a Go implementation through `@native`; neither path uses reflection or runtime name lookup.

```luxo
fn normalizeScore(score: Int, ceiling: Int = 100): Int {
  when(score) {
    in 0..ceiling -> score
    else -> ceiling
  }
}

fn verifyRelease(projectId: Int): Result<Boolean> @native
fn recordRelease(projectId: Int) @native

api releaseReady(projectId: Int, score: Int): Boolean {
  val normalized = normalizeScore(ceiling: 100, score: score)
  recordRelease(projectId)             // Go error is propagated automatically
  verifyRelease(projectId)? && normalized >= 80
}
```

Named arguments are matched to declarations at compile time. Defaults must be type-safe compile-time constants, and invalid names, duplicates, missing required values, or type mismatches stop generation during semantic analysis.
Value-returning native functions use `Result<T>` and explicit `?`; native functions without a value lower to Go `error` and propagate failures automatically.
Function variadics use `...values: T`, must be last, cannot have defaults, and preserve the same direct Go variadic ABI for compiled and `@native` functions. Public APIs use `[T]` instead because their named parameter IDs must remain stable on the wire.

### Concurrency — No async/await Infection

```luxo
// Everything looks synchronous — Go runtime handles scheduling
val user = fetchUser(1)              // no await needed

// Concurrent execution — only when you want it
val (user, posts) = await {
  fetchUser(1)                       // run simultaneously
  fetchPosts(1)                      // run simultaneously
}

// Fire and forget
async {
  sendEmail(user.email, "Welcome!")
}

// Channels — compiles to Go channels
val ch = Channel<Int>(10)
ch <- 42                             // send
val value = <-ch                     // receive
```

### yield — For Loops as Expressions

```luxo
val found = for item in items {
  if item.special { yield item }     // exit loop with value
}
// found: Item? — yield's value, or null if not found
```

### Variables — `val` + `var`

```luxo
val name = "immutable"               // immutable (global + local)
var count = 0                        // mutable (local only)
count += 1
```

### Collection Operations

```luxo
val total = items.sumOf { it.price * it.quantity }
val active = users.filter { it.status == "active" }
val names = users.map { it.name }.joinToString(", ")
val vip = users.any { it.role == Role.VIP }
```

### API Definition — Three Levels

Declare cache behavior explicitly in the project's `luxo.cache.json`:

```json
{"version":1,"policies":{"userPreview":{"mode":"ttl","ttl":"1m","scope":"private"}}}
```

Beta supports explicit TTL response caching, not automatic write invalidation.
TTL starts when an entry is filled; it does not bound the age of the underlying database snapshot.
Use uncached queries for decisions that require fresh data. Identity isolation is preserved;
an explicit backend, deployment namespace and bounded origin concurrency are required at startup.
The `invalidation` mode is rejected rather than silently converted to TTL.

```luxo
// 1. Zero code — framework generates everything
api getUser(id: Int): User @cache(policy: "userPreview")

// 2. With logic — write in Luxo
api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw PasswordTooShort
  val user = User.create(name: input.name, email: input.email, password: input.password)
  val token = generateToken(user, expires: 7d)
  AuthResult { token, user }         // shorthand — field name = variable name
}

// 3. Complex — write in Go
api oauthLogin(provider: String, code: String): AuthResult @native
```

### Modules — `use`

```luxo
use http                             // stdlib import
use common.{ Base, Page }           // destructured import
```

### Current User — `my`

```luxo
api createPost(title: String): Post @auth {
  val post = Post.create(title: title, userId: my.id)
  post
}
```

### Events — `event` / `emit` / `on`

```luxo
type OrderSnapshot { id: Int }
event OrderCreated(order: OrderSnapshot, userId: Int) // typed event declaration

api placeOrder(id: Int): Order @auth {
  val order = Order.create(userId: my.id)
  emit OrderCreated(order: OrderSnapshot { id: order.id }, userId: my.id) // typed emit
  order
}

on OrderCreated { order ->                       // event listener
  "order created".i                              // .i = info log
}
```

Event payloads use explicit `type` snapshots, not database `model` or `extend`
types, including inside nested fields and lists. The publisher chooses which
fields belong to the event; changing a database model does not add event fields.
Structured snapshots support at most 64 nested `type` values on each path. The
first type is level one; the event envelope and list containers do not add levels.
In-memory publishing and wire encoding/decoding reject excessive depth, including
native cycles, with `event.ErrSnapshotDepth`; content is never truncated. Regenerate
event publishers and consumers together. Valid wire bytes remain unchanged.

### Debug Chain — `.d`

```luxo
val user = User.find(id: 1).d        // .d prints and returns self
```

### Field Selection — All The Way Down

```
getUser(1) {
  name email
  posts { title comments { content user { name } } }
}
```

Client selects fields → API serializes only those → SQL queries only those. End to end.

Every public API returning a structured value requires a non-empty `$select`;
an omitted selection is a protocol error rather than an implicit full-table
projection. The Vite analyzer normally injects the selection at compile time
for generated one-shot calls and stream subscription callbacks. For escaped
or dynamic usage it emits an explicit safe projection, while raw
transport callers must provide `$select` themselves. Explicit selections return
`Foo<true>` and preserve field presence exactly: unselected, selected `null`, or
selected value. Input DTOs remain strict; shared input/output types generate
`Foo` and `FooInput` separately.

### Real-time Streams

Event payloads are explicit snapshots. The authenticated example uses the application's `User` auth model.

```luxo
type DanmakuSnapshot { roomId: Int text: String }
type NotificationSnapshot { userId: Int text: String }
type ScoreEvent { matchId: Int score: Int }
event DanmakuSent(roomId: Int, danmaku: DanmakuSnapshot)
event NotificationCreated(userId: Int, notification: NotificationSnapshot)

// Event-driven stream with filter
api watchDanmaku(roomId: Int): DanmakuSnapshot @stream(DanmakuSent) {
  it.roomId == roomId
}

// Auth-filtered stream
api watchNotifications: NotificationSnapshot @stream(NotificationCreated) @auth {
  it.userId == my.id
}

// Go-controlled stream
api watchLiveScore(matchId: Int): ScoreEvent @stream @native
```

## Quick Start

```bash
go install github.com/light-speak/luxo/cmd/luxo@v1.0.0-beta.1

luxo init my-app
cd my-app
luxo add user
cp .env.example .env
luxo gen
luxo run
```

This beta.1 coordinate requires the corresponding release artifacts to be published.
The CLI and adapters are separate modules in this repository; pin them to the same
candidate as the core. Select optional backends before `luxo gen`; changing a backend
requires regenerating and rebuilding. For NATS, add
`github.com/light-speak/luxo/pkg/lux/nats@v1.0.0-beta.1` and set
`LUXO_EVENT_BACKEND=nats`. Runtime addresses do not select adapters.

## Architecture

> [View full architecture diagram](assets/architecture.svg)

**Luvia is always on.** Embedded mode runs every module and the gateway in one process. Cluster mode uses generated per-module service binaries plus a generated gateway; RPC routing and federation loaders come from the same analyzed schema, without handwritten transport clients.

JSON and Luxo Binary are both production transports. HTTP clients select them through the SDK transport mode or `X-Luxo-Mode: json|binary`; WebSocket supports JSON text and canonical binary frames, while native RPC uses its binary framing. `APP_ENV` never silently changes the wire contract.

Both encodings preserve the same value semantics. Business `Int`/`Duration` are signed int64; WebSocket call IDs (`$id` in JSON, `sequence` in Binary) are unsigned uint64. JSON IDs must be unquoted decimal integer tokens in `0…18446744073709551615`, not booleans, strings, fractions or exponents. Invalid/missing/overflowing IDs are dropped before handler dispatch, never coerced to zero. Successful and failed calls echo the exact valid ID. SDK counters must fail on exhaustion instead of rounding, narrowing or overwriting pending calls; complete cross-SDK boundary acceptance remains a beta release gate.

### Gateway HTTP implementation

The gateway defaults to `APP_HTTP_TRANSPORT=luxo`. TLS ALPN and cleartext HTTP/2
prior-knowledge connections use Luxo's dedicated HTTP/2 server; HTTP/1.1 and
WebSocket retain the standard Go HTTP path on the same listener. Set
`APP_HTTP_TRANSPORT=standard` explicitly to use the standard HTTP implementation
for HTTP/2 as well. Unknown values fail at startup. This startup setting selects
the server implementation, not the JSON/Binary encoding or a client's HTTP version.

### Request and SQL Tracing

Luxo traces a request as an execution DAG across the gateway, services,
DataLoaders, and databases. An active Studio Playground debug request carries
the project debug key and explicitly opts into details. PostgreSQL then records
pool wait, operation, resource, timing, argument count, affected rows, and error
code. SQL text is stripped of regular and nested comments, has string,
dollar-quoted, and numeric literals redacted, and is capped at 4 KiB. Argument
values and database credentials never enter a trace.

Ordinary production requests have no trace session: the database hot path only
performs only constant-time context lookups, reads no clock, constructs no metadata, and allocates
nothing. Samples selected by `LUXO_TRACE_SAMPLE_RATE` retain only a normalized
SQL-shape fingerprint and structural metadata; comments, literal values, and
parameter ordinals do not affect that aggregation key. Samples never construct
or persist SQL text. Detailed traces use a streaming, versioned Luxo Binary
response envelope instead of HTTP headers, so the gateway does not buffer or
copy the business body. Traces are capped at 128 spans with explicit truncation
metadata.

### Wire Compatibility

`luxo.lock` v2 pins model/type/event field IDs, API IDs, parameter IDs, and their wire types. `luxo gen` rejects breaking changes before rewriting the lock. Use `--allow-breaking` only when every deployed producer and consumer will be regenerated together.

Compatibility here means a new server continues accepting old clients, so compatible releases should deploy servers before regenerated clients. Adding a model/type field or an optional API parameter is compatible in that direction. Removing or changing a field/parameter, adding a required parameter, changing an API return type, removing an API, or changing an event payload is breaking. Removed IDs stay reserved and are never reused.


### Studio Registration Transport Security

Configure `LUXO_STUDIO_URL`, `LUXO_API_KEY`, and the public project UUID `LUXO_PROJECT_ID` to enable registration and periodic heartbeats. Remote URLs require HTTPS by default; `localhost` and loopback IPs may use HTTP for local development. Only explicitly trusted private networks should enable remote HTTP through `LUXO_STUDIO_ALLOW_INSECURE_HTTP=true`; this does not encrypt the connection. URLs must not contain embedded credentials, query parameters, or fragments.

Registration, heartbeats, deregistration, metrics, and trace exports never follow HTTP redirects, preventing replay of credential-bearing request bodies. Gateway shutdown cancels in-flight registration, heartbeats, and dependency probes, joins the registration worker, then attempts deregistration with a separate two-second timeout. These operations stay outside application request hot paths.

### Release and Stability Policy

Cross-repository Swift CI pins candidate commit `c0fa9cc3334045ce488bd5d97d1a3a8c82ff8071` and checks that its `VERSION` matches the core, rather than following floating `main`. Local conformance against that commit does not replace remote checkout after pushing, the complete release gates, or publication; every candidate update must record the tested revisions and compatibility matrix again.

The next release target is **`v1.0.0-beta.1`**, not an already published stable release. Luxo follows [Semantic Versioning](https://semver.org/): Git tags use the `v` prefix; package versions use the format required by their ecosystem.

| Stage | Version sequence | Scope |
| --- | --- | --- |
| Beta | `v1.0.0-beta.1`, `v1.0.0-beta.2`, … | Establish the compatibility baseline and fix correctness, security, and performance defects. |
| Release candidate | `v1.0.0-rc.1`, `v1.0.0-rc.2`, … | Feature freeze; release-blocking fixes only. |
| Stable | `v1.0.0` | Publish the verified public contract. |
| Maintenance | `v1.0.1` / `v1.1.0` / `v2.0.0` | Compatible fixes / compatible additions / incompatible public-contract changes. |

Beta releases are prereleases, not a promise of production stability. From beta.1 onward, preserve compatibility by default. Any unavoidable beta contract break requires explicit review, migration instructions, updated SDKs, and compatibility tests; a new beta number alone does not make a breaking change safe. Published tags and artifacts must never be overwritten.

The public contract covers DSL semantics, supported CLI/configuration, generated/native Go interfaces, SDK behavior, explicit field selection, schema/lock IDs and wire types, JSON/Binary and RPC/stream framing, errors, and Studio registration/telemetry. Internal implementation and trace timing measurements are not stable APIs. Product versions, wire-envelope versions, and `luxo.lock` versions are separate: a product release must not renumber unchanged encodings or reuse removed IDs.

Each release must identify the exact core commit and tested TypeScript, Dart, Kotlin, and Swift SDK versions/commits. Release gates include compiler/runtime tests, race checks, lint and reachable-path coverage review, benchmark comparison against the previous baseline, cross-SDK protocol fixtures, and a clean external consumer build. Preserve baseline fixtures and generated consumers; running only regenerated clients against a new server is not a backward-compatibility test. Before the first stable release, verify Studio against the candidate without a developer Go workspace or unpinned sibling checkout.

SDK convenience features may follow the core release, but protocol correctness cannot: `Int` and `Duration` must preserve the entire signed 64-bit range, reject overflow/truncated binary values, and agree across JSON/Binary, nullable values, arrays, columns, and streams. Use shared exact-wire fixtures, not just same-SDK round trips. Validate Dart VM and JavaScript targets independently; passing native tests does not certify web precision. SDK version pinning is not a reason to defer these release blockers.

The first candidate uses `1.0.0-beta.1` from [`VERSION`](VERSION) across the core, four SDKs, React/Vite companions, and Kotlin plugin. TS companions require the exact matching client; Swift installation examples also pin the exact version. Version-alignment tests prevent stale package coordinates, but matching numbers do not replace protocol tests or verified commit SHAs. These are candidate metadata, not a claim that release or freeze is complete.

Go adapters and the CLI remain in this repository but are independently versioned modules. The first candidate requires the following tags on the same verified repository commit; the root tag alone does not publish nested modules:

| Module directory | Candidate tag |
| --- | --- |
| Repository root | `v1.0.0-beta.1` |
| `cmd/luxo` | `cmd/luxo/v1.0.0-beta.1` |
| `pkg/lux/pg` | `pkg/lux/pg/v1.0.0-beta.1` |
| `pkg/lux/cache/redis` | `pkg/lux/cache/redis/v1.0.0-beta.1` |
| `pkg/lux/nats` | `pkg/lux/nats/v1.0.0-beta.1` |
| `pkg/lux/storage/s3` | `pkg/lux/storage/s3/v1.0.0-beta.1` |

This table defines release coordinates, not publication status. Publish only after the release gates pass and authorization is granted. Verify the released coordinates in an external consumer with `GOWORK=off` and no local replacements; workspace builds alone cannot prove published dependency resolution. Applications require only their selected adapters. All Luxo module requirements, including the CLI's PG dependency, must match the first candidate version.

Studio releases must pin the tested core Go module, CLI, frontend SDKs, and CI checkout to the recorded release/commit, never floating `main` or `latest`. Local source overrides are development-only. Studio keeps an independent version; any later independent SDK releases still need a compatibility matrix, not an assumption based on version numbers. PostgreSQL is the currently implemented database backend; future database backends and new Studio features are outside this release freeze.

#### Pre-Beta Internal RPC Upgrade (Under Validation)

`fn @service`, generated batch loaders, relation resolvers, and named remote
loaders register through `Router.HandleInternal`. HTTP and WebSocket only use
the public route table; RPC exports both public APIs and internal functions at
startup. Schema metadata carries `internal`, and startup validation rejects
conflicting registrations. The `svc:` prefix is not an authorization rule.
Regenerate services and gateways together. Studio registration, heartbeats,
deregistration, and telemetry now use explicit control APIs authenticated with
the project API key, without legacy `svc:` aliases. All four SDK generators use
explicit scope metadata. Studio preserves internal entries in Schema documentation,
but excludes them from executable Playground APIs, including restored requests.
Full deployment/performance acceptance remains in progress; this is not a
frozen release or a replacement for RPC peer authentication.

The unreleased internal protocol does not retain old-gateway compatibility or automatic fallback. Regenerate, rebuild, and deploy gateways and services together from the same verified core commit. This does not change the public SDK HTTP/WebSocket JSON/Binary formats.

Each new TCP connection exchanges a fixed preface and requires an exact `rpc.ProtocolVersion` match before business requests. Pooled connections do not repeat the handshake. New connections, reconnects, and newly opened stream connections incur one extra network RTT, subject to separate performance acceptance. Requests use one kind/flags/token/optional-trace/body format; historical trace-request decoders are removed. Version checks do not replace authentication or schema/lock ID consistency checks.

Internal relation resolvers now require `keys` (field 1) and `shape` (field 2: bit 0 list, bit 1 nullable). Each response group is the canonical field value, including presence and length, with an object or columnar-list body generated by the target service. The gateway validates and copies those bytes without model decoding/re-encoding. Required missing objects and multiple rows for a single relation fail; empty lists and selected null values remain distinct. Regenerate both gateways and services together.

Nested response regressions pass for the new format. Unary RPC errors now preserve raw JSON data, differing interpolation text and the internal-error flag without converting numbers to floating point. Generated TS, Dart, Kotlin and Swift consumers exercise error data through gateways over HTTP/WebSocket and JSON/Binary. Native stream completion and structured terminal errors also have direct-service and gateway consumer regressions in all four SDKs; complete release, device and remaining performance gates stay open. This does not freeze Beta.

#### Pre-Beta Error Data Interface Migration

The development Go API uses static `errors.ErrorData` methods `AppendJSON(dst []byte) ([]byte, error)` and `AppendText(dst []byte, field string) ([]byte, bool)` instead of `I18nData()/MapData` and reflective formatting. Luxo error declarations and constructor arguments are unchanged: rerun `luxo gen`. Handwritten native error data implementations must implement the new methods. Use `errors.IntID(value)`, `errors.StringID(value)`, or `errors.UUIDID(value)` for `ResourceError.ID` rather than a dynamically typed ID.

AppendJSON must append valid JSON on success and preserve the original dst prefix on failure; `errors.DataWriter` provides static writers. AppendText supplies only the requested field and returns the original dst with false for a missing or unrepresentable value. Direct i18n callers may use string-only `i18n.Values`. Int/Duration retain the full signed 64-bit range; generic JSON keeps original numeric tokens without floating-point conversion. Internal errors never encode or interpolate data, expose only `error.internal` as their message, and disclose cause only in development mode.

This is a pre-freeze Go source-interface change, not a change to the public JSON/Binary numeric contracts or binary layouts. Unary RPC data propagation is covered by the integration tests above, not a certification of every SDK path or performance gate. Float writer error propagation has been separately approved and implemented; performance acceptance remains independent.

#### Pre-Beta Static Request Parameter Migration

`Request.ParamJSON`, `ParamJSONOptional`, `ParamJSONNullable`, and `ParamJSONOptionalNullable` have been removed. Regenerate services with `luxo gen`; ordinary `.luxo` and `@native` business signatures and valid wire layouts are unchanged. Handwritten Request handlers must use concrete getters such as `ParamInt`, `ParamUUID`, `ParamDuration`, and their array variants. Use `ParamRawJSON` / `ParamRawJSONArray` for opaque JSON, not arbitrary Go destinations; numbers retain their original text. For structured local JSON input, generated handlers use `ParamInput` and `ReadJSONInput` / `ReadJSONInput<Type>List` directly. Binary input uses `ParamMessage` / `ParamMessageArray`, generated `ReadLuxoInput`, and `Decoder.Finish()`.

Generated handlers distinguish missing, explicit null, and a value. Defaults apply only when missing; explicit null clears a nullable default and fails for non-nullable parameters. Handwritten handlers must preserve the same distinction with `HasParam` and `ParamIsNull`. Binary Bytes/JSON borrow request storage during the synchronous handler; copy them before retaining them asynchronously. Historical reflective readers exist only in test references, never in ordinary builds.

`api.EncodeBinaryRequest` also requires explicit `json.RawMessage` documents for JSON parameters. Lists accept `[]json.RawMessage`, a raw JSON array document, or `[]any` containing only raw documents; maps, structs, custom `MarshalJSON` values and implicit scalar conversion are rejected. For example, pass ``json.RawMessage(`{"count":123456789012345678901234567890}`)`` instead of a Go map. Valid documents retain their exact bytes, including numeric tokens; invalid input returns an error and a nil request body. This restriction is on the Go encoding API, not a wire-format change.

Local structured JSON input no longer takes an intermediate JSON-to-Binary-to-model detour. Gateway forwarding still validates and encodes parameters for the internal Binary RPC boundary; that network encoding is distinct from local model decoding. This removes the arbitrary-target reflection fallback, not every dynamic boundary in Luxo, and does not imply zero encoding, zero allocations, or completed whole-chain performance acceptance.

#### Request Log Sampling

Request logs are disabled unless `LOG_REQUESTS=true`. When enabled, successful requests are sampled one in 100 by default. Set `LOG_REQUESTS_FULL=true` as well to opt into full logging for development or troubleshooting; it does not enable logging by itself. Errors bypass sampling, but bounded asynchronous queues can still drop records and report counters. These logs are not a durable audit channel. Metrics and trace sampling are independent.

For explicit Go configuration, `RouterOptions.RequestLogSampleEvery` uses 100 when zero, 1 for full logging, and N for one in N successes. Use `api.RouterOptionsFromEnv()` before overriding options if environment-based logging settings are desired. Generated cached and uncached services use the same environment policy.

#### NATS Task Retries and Dead Letters

`queue.NATSQueue` uses JetStream, not the non-persistent Core NATS event broadcast.
`MaxRetries` bounds business delivery attempts. Once exhausted, subsequent deliveries
only transfer the task to `<queue>.dlq`, including after worker restart. A failed
DLQ creation or publication does not acknowledge the original. Transfer retries use
exponential `RetryDelay`, capped at one minute, and the original is acknowledged
only after JetStream confirms publication. The broker schedules delayed NAKs;
waiting tasks do not each require a framework goroutine.

Transfer is at-least-once, not exactly-once: an uncertain publish acknowledgement or
lost original ACK can duplicate the dead letter. Consumers must remain idempotent
using application task identifiers. Queue streams use `DiscardNew`: when an active
capacity limit is reached, new writes are rejected instead of evicting unacknowledged
tasks. Operators must monitor storage and backlog, and must not delete durable
consumers or reset delivery state as a task-recovery shortcut.

#### Finite Float Writer Migration (Under Validation)

`codec.LuxoMarshaler.MarshalLuxo` now returns `([]byte, error)`; rerun `luxo gen` for events and update handwritten implementations. Encoder Float value/field/array writers and ColumnarWriter Float column writers return `error`, rejecting NaN/infinities without committing a partial field or column. `rpc.EncodeParams` also returns `([]byte, error)` and rejects unsupported values instead of dropping parameters. NATS does not publish when marshaling fails; the existing `Emit` error return is unchanged.

Generated model/type `WriteLuxo` and all `WriteColumnar` variants now return `error`; `codec.AppendFixed64` returns `([]byte,error)`, and `ResponseBuf.AppendFloat` returns `error`. Nested events, compiled/native handlers and internal RPC propagate failures. Direct ResponseBuf callers must discard the current output after a write error.

`codec.ColumnarWriter.Bytes()` now returns `([]byte, error)`. It rejects nonempty zero-column output, invalid record/arena counts, nonpositive field IDs and inconsistent column lengths before touching the output buffer; failures return nil bytes. Callers must check the error. Empty lists retain `00 00 00`, and a selected all-null column remains valid. Call `Reset()` after inconsistent columns or invalid field IDs; generated direct writers already return errors and do not use this buffered helper. This API change does not imply performance acceptance for unmeasured workloads.

Stream encoder callbacks and `StreamPayloadJSON` return `([]byte,error)`; `TypedStream.Send` and `Dispatch*` report failures without sending the failed payload. All five schema Binary-to-JSON converters also return `([]byte,error)`, propagated through HTTP, WebSocket and CLI instead of disguising invalid responses as integers, null or default pagination. Rerun `luxo gen` and update handwritten encoder callbacks; ordinary DSL and native business return types are unchanged.

Native stream handlers now receive generated, strongly typed parameter snapshots and return `error`. Authentication, defaults, and parameter checks complete before the subscription ACK. A normal return drains accepted messages before completion; a business error terminates that subscription with its structured error data. The gateway propagates RPC termination and payload-conversion failures rather than silently ending a void handler. Each subscription has an independent UInt64 identity. SDKs terminate subscriptions on disconnect and leave resubscription to the caller; no replay or continuous delivery is implied. These implemented contracts do not replace final candidate, device, and performance acceptance.

Valid wire bytes, negative zero and finite subnormals are preserved. Checks apply only to fields actually written, without reflection or additional serialization round trips. Cross-SDK revalidation, end-to-end performance acceptance and protocol freeze remain pending. An asynchronous subscriber failure cannot retroactively fail a completed publish, retract previously delivered valid messages, or roll back database writes.

#### Modern Stable Toolchains

The development mainline actively adopts the latest stable toolchains and useful stable language features. It does not maintain old compiler support merely for its own sake. Experimental flags, nightly compilers, and preview APIs are not requirements of released SDKs. Toolchain upgrades do not change wire versions, field IDs, integer precision, or selection semantics.

The current migration targets are Go 1.27.1+, TypeScript 7, Dart 3.13+, and Kotlin 2.4.20; Swift tracks its latest stable compiler line in separately verified SDK changes. Compiler versions and device/runtime deployment targets are separate decisions: a newer Kotlin or Swift compiler does not by itself justify dropping older Android or iOS devices.

The exact Node version for JavaScript development and SDK interoperability is defined in [`.node-version`](.node-version), shared by TS and Dart Web CI instead of a floating major. Before installing dependencies, `sh scripts/check-node.sh` verifies the actual runtime; Studio's linked build uses the same core script. Old or preview versions, invalid pins, and failed tool invocations stop validation without downloading or installing tools. TS CI also pins pnpm and reports the actual Node, pnpm and TypeScript compiler versions. Node declarations in all three TS packages are build/test-only, with resolved versions pinned by lockfiles; they do not enter browser runtime code. Client/React retain ES2022 output, and the Vite plugin declares its minimum Node requirement separately. Updating development tools does not certify browser or minimum plugin-runtime compatibility.

Dart CI pins both the supported minimum, 3.13.0, and the selected stable version, 3.13.4, in the [workflow](.github/workflows/test.yml). Both use the same analysis, VM, JavaScript/Wasm, and generated-consumer checks. Startup verification rejects a different compiler version, a preview channel, or a failed tool invocation instead of treating floating `stable` as a reproducible baseline. Updating this exact version neither raises the SDK minimum nor approves performance costs caused by compiler changes.

The Go generator and generated projects move together: database entry points use generic methods `DB.QueryRows[T]`, `DB.QueryRow[T]` and `DB.QueryScalar[T]`, retaining package-level call compatibility over one implementation. Existing `Query[T]`, create/update bases and DataLoader already share generic algorithms; model scanners, field selection and codecs remain schema-specialized. `NativeResolver` keeps concrete signatures because interface methods cannot declare independent type parameters.

Go-based analysis tools must also be built with a compiler that understands the project's syntax. From this repository root, rebuild the pinned checker with `go install github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0` and inspect it with `go version -m "$(command -v gocyclo)"` after upgrading Go. The tracked pre-commit hook, `bash scripts/lint.sh`, and CI share [check-go.sh](scripts/check-go.sh), checking `pkg/`, `cmd/`, `scripts/`, and `internal/`. Missing tools, parse failures, unexpected diagnostics, unformatted files, and complexity above 25 all fail the gate with diagnostics preserved. Nothing is automatically installed or skipped locally; reinstall the tracked hook if using an older copied version.

For a standalone checkout, run `sh scripts/setup-modules.sh .` once to bind candidate modules locally; this is unnecessary when a complete parent workspace already exists. The script refuses to overwrite `go.work` and does not change release manifests. Since `go test ./pkg/...` excludes nested modules, local lint and CI check the CLI, PG, Redis, NATS, and S3 modules separately. Adapter integration tests still require their corresponding service configuration.

On macOS with Colima, the testcontainers cleanup container needs the Docker socket path inside the VM, not the host path.
Verify it with `colima ssh -- test -S /var/run/docker.sock`; if successful, prefix the test command with
`TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock`. Do not disable the cleanup container to bypass mount failures.

For a CI-like local database test run, set `LUXO_TEST_DATABASE_URL` to a dedicated PostgreSQL test database: each test creates and cleans its own schema. Without it, tests start individual containers, whose cumulative startup/cleanup can exceed the local lint script's 60-second package timeout. Do not point this setting at application data or skip database tests to obtain a passing result.

Dart hints use resolved types at build time for primary-constructor fields, local aliases, nested relations, list/page access and inline callbacks. Cross-file and `part` merging isolates symbols and client libraries. SDK wrappers such as `Selected.value` are not wire fields, and explicit `select` takes precedence. See the [Dart SDK](packages/dart/README.md#compile-time-field-tracking) for the default output directory and static-analysis boundaries; source analysis never runs on the request path.

Adoption includes generators, generated consumers, build/declaration tools, language-aware field-selection analysis, CI, and documentation, not just manifest edits. Unsupported source syntax must produce a diagnostic, never silently expand field selection. Stable features are adopted where they improve correctness, performance, or maintainability; new syntax alone is not evidence of faster runtime code. Reflection and generic hot-path serialization remain prohibited.

Lock exact resolved tools for reproducible builds and record them in release evidence. CI verifies the supported minimum and current stable toolchain where different; previews belong in separate scheduled compatibility checks. Compare baseline and candidate on the same toolchain, and separately measure compiler upgrades on unchanged code. Do not reset performance baselines to hide regressions. Published artifacts keep their recorded requirements; increasing a released package's minimum compiler requires an explicit compatibility review, version decision, and migration notes, never a silent maintenance update.

References: [Go 1.27](https://go.dev/doc/go1.27), [Dart 3.13](https://dart.dev/blog/announcing-dart-3-13), [TypeScript 7](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/).

#### Performance Blocking and Approved Feature Costs

Performance review distinguishes request-path regressions from necessary compilation costs. Initialization on first use in each compilation is neither per-request allocation nor once-per-process initialization. Required features still need tests and benchmarks; necessity alone grants no exception.

Default CI rules remain unchanged: at least 10 samples per revision; significant time growth above 5% or significant allocation growth triggers a second sample group, and repeated regressions block. The 5% threshold is an automated detection threshold, not an expendable performance budget. Reflection, generic request-path serialization, and confirmed hot-path defects still require rejection during review.

Approved costs for necessary functionality have one machine-readable source: [approved-costs.json](scripts/benchgate/approved-costs.json), rather than a duplicated list of budgets here. Each record identifies its reason, exact baseline SHA, package, benchmark, and approved absolute time or allocation increase limits; wildcards are forbidden. Approval for memory does not approve time, and approval for a cold path does not approve request paths.

CI applies and prints an approval only when both sample groups contain matching comparisons and both satisfy its limits. Unapproved regressions, exceeded budgets, and other samples retain the default gates. A different baseline SHA no longer matches the approval, preventing rolling budget accumulation. New or expanded approvals require explicit maintainer confirmation; tools must not grant them automatically. Local samples do not replace end-to-end request validation.

When a zero baseline increases to a positive value with a significant difference, benchstat displays the relative change as `?`. The gate uses the valid absolute measurements to detect a regression and require confirmation, never treating it as zero growth or automatic approval. A `?` with a nonzero baseline, invalid measurements, or insufficient samples is still rejected.

CI retains raw benchmark samples and benchstat comparisons for 14 days, including failed runs. Gate diagnostics report exact baseline, candidate, and absolute differences rather than rounded percentages alone. Queue integration tests require a healthy JetStream-enabled NATS server, not just an open NATS port. Each lexer/parser/semantic fuzz target runs two million executions with four workers and a 180-second hard timeout; any failure or timeout still fails CI. Fuzz logs, toolchain information, and saved failing inputs are retained for 14 days.

## AI-Native by Design

Luxo isn't just shorter code — it's **the most reliable language for AI to write backend APIs**.

| | Traditional (Go/TS) | Luxo |
|---|---|---|
| **Context needed** | 50+ files, 10K+ tokens | 1 `.luxo` file, ~500 tokens |
| **AI output correctness** | Compiles ≠ correct, needs tests | Compiler catches types, nulls, exhaustiveness |
| **Code style variance** | AI picks different patterns each time | One way to write everything |
| **Schema understanding** | AI reverse-engineers from code | Schema IS the code |

```
"Add a product favorites feature"

→ AI generates 15 lines of .luxo
→ Compiler validates types, null safety, required fields
→ Done. API is running.

Same task in Go? 200+ lines across 7 files.
Same task in TypeScript? 150+ lines, 4 packages, no compile-time safety.
```

**Luxo + AI = reliable backend engineer.** Other languages give AI too much freedom. Luxo gives AI exactly the right constraints.

## Roadmap

### Phase 1 — Compiler ✅
- [x] Lexer · Parser (Pratt parser)
- [x] Semantic Analyzer (layered declaration/type/body/post-analysis passes, type checking, null safety, field injection, directive validation)
- [x] LSP Server (diagnostics, completion, hover, go-to-definition, references)
- [x] VS Code Extension (syntax highlighting, LSP integration)

### Phase 2 — Codegen + Runtime ✅
- [x] Code generation (11 schema-driven module artifacts plus embedded, service, and gateway entries)
- [x] Type-safe query builder (pgx, zero-reflection scanner, `$select` → SQL)
- [x] CRUD handler generation (`@crud` → get/list/create/update/delete)
- [x] DataLoader runtime (2ms batch window, field merging, `@soft` filtering)
- [x] Relation auto-resolve (`$select` nested fields → DataLoader → SQL)
- [x] `@hash` bcrypt auto-hash on create/update, `VerifyPassword` for login
- [x] `@hidden` excluded from default SELECT (no password in API responses)
- [x] `@by` relation directive + auto-inference (belongsTo/hasMany/hasOne)
- [x] Migration engine (declarative diff · rename detection · safety warnings · `--dry-run`)
- [x] Advisory lock + checksum verification + `CREATE INDEX CONCURRENTLY` auto-split
- [x] Auth runtime (JWT sign/verify/refresh, Luvia `AuthMiddleware`, `Identity(ctx)`)
- [x] Standard library (str · slice · math · datetime · crypto · jsonutil · httputil · convert)
- [x] Project scaffold (`luxo init` + `luxo add` + `luxo gen` + `luxo run`)
- [x] Pluggable dialect interface (PostgreSQL implemented; MySQL, SQLite, and MongoDB are roadmap targets)
- [x] `luxo deploy compose` — Dockerfile + docker-compose.yml generation

### Phase 3 — Multi-service + Binary ✅
- [x] Binary protocol (varint · svarint · fixed64 · field mask · columnar encoding)
- [x] Luvia Gateway — schema-driven Binary↔JSON (handler binary-only, Luvia translates)
- [x] `fn @service` — expose functions as RPC endpoints (Luxo protocol)
- [x] `extend` + `Model.load()` — cross-module DataLoader with visibility control
- [x] Multi-condition DataLoader (FK · composite key · static analysis)
- [x] Cluster mode — per-module binary, Gateway routing, `DEPLOY_MODE` switch
- [x] `luxo deploy compose` — Dockerfile + docker-compose.yml generation
- [x] Auto-migrate on startup (`EnsureDatabase` + `migrate.Up`)
- [x] RPC default address inference (`module:9000` Docker DNS)
- [x] Schema introspection (`GET /luvia?$schema` with `X-Introspection-Key`)
- [x] `luxo.schema.json` export for SDK tooling
- [x] Graceful shutdown · CORS · XSS security headers
- [x] Event system (emit / on · ChanBus + NATSBus · Luxo binary codec)
- [x] WebSocket transport — JSON/Binary dual-mode, concurrent dispatch
- [x] `@stream` subscription — typed SDK methods, server acknowledgement/error, event/native sources, field mask and backpressure
- [x] Schema-only SDK signatures — pagination, selection, nullable arguments and streams never depend on API naming conventions

### Phase 4 — Client SDK ✅
- [x] `@luxo/client` — Transport interface + FetchTransport + WxTransport + LuxoError
- [x] `@luxo/vite-plugin` — compile-time field tracking + auto `$select` injection
- [x] `@luxo/react` — `useLuxoQuery` hook + `LuxoProvider`
- [x] Dart SDK (`luxo_client` — HttpTransport + WsTransport + Luxo binary codec + build_runner field tracking)
- [x] Kotlin SDK (`com.luxo.client` — OkHttp + coroutine + Luxo binary codec + isolated compiler-AST field tracking via Gradle plugin)
- [x] [Swift SDK](https://github.com/light-speak/luxo-swift) (`LuxoClient` — standalone SPM package with URLSession, async/await, Luxo binary codec and SwiftSyntax field tracking)

### Phase 5 — Production + Ecosystem (In Progress)
- [ ] Luxo Studio (actively developing; backend and React foundations are in place)
- [ ] HTTP/3 (QUIC) support
- [ ] MCP Server (AI reads/writes .luxo projects natively)
- [ ] luxo-ai (natural language → .luxo → running API)
- [x] `luxo deploy helm` — Helm Chart generation

## Contributing

Luxo is in early development. Contributions, ideas, and feedback are welcome.

## License

Apache-2.0 · Copyright 2026 light-speak
