<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  A general-purpose language with a built-in API framework.<br/>
  One language, one protocol, one toolchain — replaces REST, GraphQL, and gRPC.
</p>

<p align="center">
  <a href="https://github.com/light-speak/luxo/actions/workflows/test.yml"><img src="https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg" alt="Tests" /></a>
  <a href="https://codecov.io/gh/light-speak/luxo"><img src="https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg" alt="codecov" /></a>
  <a href="https://goreportcard.com/report/github.com/light-speak/luxo"><img src="https://goreportcard.com/badge/github.com/light-speak/luxo" alt="Go Report Card" /></a>
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

Luxo is a **programming language** that compiles to Go — with a built-in API framework, its own protocol, and a complete toolchain from schema to deployment.

Write `.luxo` files. Get: API server, database layer, client SDKs, migrations, monitoring dashboard. No glue code.

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

This generates everything. One file, zero boilerplate.

## Why Not...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| **Field Selection** | ✅ Supported but too loose — clients can craft arbitrarily deep queries, needs extra depth/complexity limits to prevent abuse | ❌ None — response fixed to proto definition (`FieldMask` exists but requires manual handling) | ❌ None — each endpoint returns fixed fields, `?fields=` requires manual implementation | ✅ Schema-level field visibility, compile-time validation, propagated down to SQL — only selected columns are queried |
| **Binary Transport** | ❌ Spec is encoding-agnostic, but virtually all deployments use JSON — significant serialization overhead at scale | ✅ Protobuf binary encoding — compact and efficient | ❌ Any format via content negotiation in theory, but JSON dominates in practice with no standard binary option | ✅ JSON in dev for easy debugging, auto-switches to Binary in prod for throughput — zero configuration |
| **One Schema** | ❌ SDL defines the API, but ORM/DB mapping maintained separately — two sources of truth that drift apart (code-first tools can help) | ❌ `.proto` for wire format + ORM for database — two definitions to keep in sync | ❌ No schema-driven workflow — routes, models, and docs all written by hand | ✅ One `.luxo` file generates API, DB migrations, client SDK, and docs — single source of truth |
| **N+1 Prevention** | ❌ Resolver-per-field pattern naturally causes N+1 — requires manual DataLoader integration (standard practice; frameworks like Hasura auto-solve) | N/A — no nested field resolution model | ❌ Nested resources require manual query optimization or eager loading | ✅ Compiler auto-analyzes relations and generates DataLoader batching — no manual intervention needed |
| **Null Safety** | ⚠️ SDL `!` marks non-null with server-side runtime enforcement; client codegen can provide compile-time type safety | ✅ Protobuf fields have default zero values with compile-time type safety (but proto3 can't distinguish "zero" from "unset" without `optional`) | ❌ No null safety — null errors only surface at runtime | ✅ Language-level compile-time null safety: `?` nullable declaration, `?.` safe access, `?:` Elvis fallback — null errors caught before running |
| **Error Handling** | ❌ Loosely-typed `errors` array with message string + optional extensions — hard for clients to handle structurally | ✅ gRPC Status with 16 standard codes + rich error details — well-typed | ❌ HTTP status codes + custom JSON body — no unified error structure standard | ✅ `Result<T>` typed errors + `?` operator for auto-propagation — safe and concise |
| **Concurrency** | ⚠️ Most implementations auto-parallelize independent resolvers (gqlgen / Apollo / graphql-java), but no language-level concurrency primitives — complex orchestration still depends on the host language | ❌ Supports bidirectional streaming, but concurrency orchestration is entirely manual | ❌ No built-in concurrency — fully depends on framework or manual thread/goroutine management | ✅ Built-in `async` / `await` + `Channel` message passing — concurrency safety guaranteed by the compiler |
| **Multi-service** | ⚠️ Apollo Federation is mature but operationally complex — requires extra gateway layer and cross-service coordination | ⚠️ Native point-to-point RPC, but multi-service orchestration still needs service mesh/discovery (Istio, Consul, etc.) | ❌ Inter-service calls require hand-written HTTP clients or additional frameworks | ✅ `extend` for cross-service type composition + built-in gateway routing + native RPC — multi-service out of the box |

## The Language

Luxo is a real programming language — not just a schema DSL. **31 keywords**, 6 core types (`Int`, `Float`, `String`, `Boolean`, `DateTime`, `Duration`), clean syntax, compiles to Go.

### Null Safety

```luxo
val user = find(User, id: 1)       // User? (nullable)
val name = user?.name               // safe access
val sure = user ?: throw NotFound   // elvis — assert or throw
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
val user = findUser(1)?              // Ok → unwrap, Err → auto throw
val data = http.get(url)?            // errors propagate automatically
```

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

// Channels
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
  val user = create(User, name: input.name, email: input.email, password: input.password)
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
  val post = create(Post, title: title, userId: my.id)
  post
}
```

### Events — `event` / `emit` / `on`

```luxo
event OrderCreated(order: Order, userId: Int)    // typed event declaration

api placeOrder(id: Int): Order @auth {
  val order = create(Order, userId: my.id)
  emit OrderCreated(order: order, userId: my.id) // typed emit
  order
}

on OrderCreated { order ->                       // event listener
  "order created".i                              // .i = info log
}
```

### Debug Chain — `.d`

```luxo
val user = find(User, id: 1).d       // .d prints and returns self
```

### Field Selection — All The Way Down

```
getUser(1) {
  name email
  posts { title comments { content user { name } } }
}
```

Client selects fields → API serializes only those → SQL queries only those. End to end.

### Real-time Streams

```luxo
api watchComments(postId: Int): stream Comment
```

## Quick Start

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
```

## Architecture

```
                        ┌──────────────────────────┐
                        │         Luvia            │
  Client ──HTTP/2──────►│  Schema · LB · Breaker   │──HTTP/2──► Services
         ◄─WebSocket────│  Studio · Identity       │
                        └──────────────────────────┘
                              │          │
                        ┌─────┘          └─────┐
                        ▼                      ▼
                  ┌───────────┐          ┌───────────┐
                  │   User    │          │   Order   │
                  │  Service  │──NATS───►│  Service  │
                  └───────────┘          └───────────┘

  Single service:  luxo dev
  Multi service:   luxo dev --all
```

## Compiler

Complete compiler pipeline with IDE support:

```
.luxo source → Lexer → Parser → Semantic Analyzer → Go Code Generator
                                       ↓
                                 LSP Server
                                       ↓
                    Real-time errors · completion · hover · go-to-definition
```

- **510 tests** · Coverage at maximum achievable limit
- **Fuzz testing** on Lexer, Parser, and Semantic Analyzer
- **Bilingual error messages** (English + Chinese)

## Roadmap

### Phase 1 — Compiler ✅
- [x] Lexer · Parser (31 keywords, Pratt parser)
- [x] Semantic Analyzer (type checking, null safety, field injection)
- [x] LSP Server (diagnostics, completion, hover, go-to-definition)
- [x] 510 tests · max coverage · fuzz testing
- [ ] Go code generator

### Phase 2 — Framework
- [ ] JSON transport + field selection
- [ ] Auto CRUD · @native resolver merge
- [ ] Migration engine (declarative diff)
- [ ] Auth · i18n · Validation
- [ ] WebSocket stream · Batch requests
- [ ] Runtime: error handling, DB pool, logging, messaging

### Phase 3 — Multi-service
- [ ] Luvia /lɑːviyɑː/ gateway · Service discovery · Circuit breaker
- [ ] Inter-service Luxo RPC
- [ ] Identity context propagation

### Phase 4 — Ecosystem
- [ ] Binary protocol · Client SDK (TypeScript / Dart)
- [ ] Luxo Studio (monitoring dashboard)
- [ ] Cache / Event / Task / Storage / Mail
- [ ] k3s deployment · .luxo test runner

## Contributing

Luxo is in early development. Contributions, ideas, and feedback are welcome.

## License

Apache-2.0 · Copyright 2026 light-speak
