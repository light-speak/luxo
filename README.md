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
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go" alt="Go Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="README_CN.md">中文文档</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#the-language">The Language</a> ·
  <a href="#roadmap">Roadmap</a>
</p>

---

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

```luxo
// 1. Zero code — framework generates everything
api getUser(id: Int): User @cache(ttl: 60)

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
event OrderCreated(order: Order, userId: Int)    // typed event declaration

api placeOrder(id: Int): Order @auth {
  val order = Order.create(userId: my.id)
  emit OrderCreated(order: order, userId: my.id) // typed emit
  order
}

on OrderCreated { order ->                       // event listener
  "order created".i                              // .i = info log
}
```

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

```luxo
event DanmakuSent(danmaku: Danmaku)
event NotificationCreated(notification: Notification)

// Event-driven stream with filter
api watchDanmaku(roomId: Int): Danmaku @stream(DanmakuSent) {
  it.roomId == roomId
}

// Auth-filtered stream
api watchNotifications: Notification @stream(NotificationCreated) @auth {
  it.userId == my.id
}

// Go-controlled stream
api watchLiveScore(matchId: Int): ScoreEvent @stream @native
```

## Quick Start

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo add user
cp .env.example .env
luxo gen
luxo run
```

## Architecture

> [View full architecture diagram](assets/architecture.svg)

**Luvia is always on.** Embedded mode runs every module and the gateway in one process. Cluster mode uses generated per-module service binaries plus a generated gateway; RPC routing and federation loaders come from the same analyzed schema, without handwritten transport clients.

JSON and Luxo Binary are both production transports. HTTP clients select them through the SDK transport mode or `X-Luxo-Mode: json|binary`; WebSocket and native RPC use their canonical binary framing. `APP_ENV` never silently changes the wire contract.

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

Until SDK versions are stabilized, the cross-repository Swift development CI temporarily follows `main`. This checks current integration but is not reproducible release compatibility evidence; pin the SDK commit and record the compatibility matrix after stabilization.

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

Studio releases must pin the tested core Go module, CLI, frontend SDKs, and CI checkout to the recorded release/commit, never floating `main` or `latest`. Local source overrides are development-only. Studio and independently released SDKs keep their own version numbers; the release compatibility matrix, not identical numbers, establishes support. PostgreSQL is the currently implemented database backend; future database backends and new Studio features are outside this release freeze.

#### Performance Blocking and Approved Feature Costs

Performance review distinguishes request-path regressions from necessary compilation costs. Initialization on first use in each compilation is neither per-request allocation nor once-per-process initialization. Required features still need tests and benchmarks; necessity alone grants no exception.

Default CI rules remain unchanged: at least 10 samples per revision; significant time growth above 5% or significant allocation growth triggers a second sample group, and repeated regressions block. The 5% threshold is an automated detection threshold, not an expendable performance budget. Reflection, generic request-path serialization, and confirmed hot-path defects still require rejection during review.

Explicitly reviewed compilation allocation costs live in [approved-costs.json](scripts/benchgate/approved-costs.json). Each record specifies a reason, exact baseline SHA, package, benchmark, and absolute B/op and allocs/op increase limits. Only semantic/codegen are eligible; wildcards and time exceptions are forbidden. Approvals apply only after matching comparison sets from two sample groups, with both byte and allocation increases within budget in both groups. CI prints every applied approval. Over-budget changes, other benchmarks, runtime packages, and time regressions retain the default gates. A changed baseline SHA automatically expires an approval, preventing cumulative reuse across later versions.

The approved cost in this batch is the declaration index for strict fn/native argument checks in `AnalyzeDemoFile-2`: at most 272 B and 2 allocations per compilation over the recorded baseline. The 272 B ceiling includes the original 256 B cost and 16 B of explicitly approved measurement headroom; two local sample groups measured increases of 258 B and 253.5 B. The index is built on demand and reused, never on the generated service's request path. New approvals or expanded budgets require explicit maintainer confirmation; tools must not approve them automatically.

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
