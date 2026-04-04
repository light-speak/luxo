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

Every API request is, at its core, data traveling from origin to destination. But the path is bloated — redundant JSON field names, GraphQL runtime parsing overhead, ORM reflection costs, `SELECT *` fetching fields that get thrown away.

We went back to the origin and asked: what is the minimum number of steps from database to client? What is the minimum number of bytes at each step?

The answer is Luxo.

**Lux** (Latin, *light*) — data should arrive at the speed of light. Not a metaphor. An engineering goal. Binary encoding, compile-time query plans, full-pipeline field selection — every layer pushing toward the physical limit.

**O** (*origin*) — back to the origin of API communication. Schema is the single source of truth. From it, database tables, type definitions, codecs, client SDKs — everything grows from one origin. No redundant layers, no duplicate definitions.

> **Luxo — Data at the speed of light, from a single origin.**

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
| Field Selection | ✅ Too loose | ❌ None | ❌ None | ✅ Schema-controlled |
| Binary Transport | ❌ JSON only | ✅ Protobuf | ❌ JSON only | ✅ JSON + Binary, auto-switch |
| One Schema | ❌ SDL + ORM + OpenAPI | ❌ .proto + ORM | ❌ Manual | ✅ `.luxo` generates everything |
| N+1 Prevention | ❌ Manual | N/A | ❌ Manual | ✅ Automatic DataLoader |
| Null Safety | ⚠️ Runtime | ✅ Compile-time | ❌ None | ✅ Compile-time `?` `?:` `?.` |
| Error Handling | ❌ Untyped | ✅ Status codes | ❌ HTTP codes | ✅ `Result<T>` + `?` operator |
| Concurrency | ❌ N/A | ❌ Manual | ❌ Manual | ✅ `async` `await` `Channel` |
| Multi-service | ⚠️ Federation | ✅ Native | ❌ Manual | ✅ `extend` + gateway + RPC |

## The Language

Luxo is a real programming language — not just a schema DSL. **28 keywords**, clean syntax, compiles to Go.

### Null Safety

```luxo
val user = find(User, id: 1)       // User? (nullable)
val name = user?.name               // safe access
val sure = user ?: throw error.not_found  // elvis — assert or throw
```

### Pattern Matching

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
  input.password.length >= 8 ?: throw error.password_too_short
  val user = create(User, name: input.name, email: input.email, password: input.password)
  val token = generateToken(user, expires: 7d)
  AuthResult { token: token, user: user }
}

// 3. Complex — write in Go
api oauthLogin(provider: String, code: String): AuthResult @native
```

### Field Selection — All The Way Down

```
getUser(1) {
  name email
  posts { title comments { content user { name } } }
}
```

Client selects fields → API serializes only those → SQL queries only those. End to end.

### Events + Real-time

```luxo
// Emit events — framework handles WebSocket + message queue
emit("order.created", order, userId: order.userId)

// Subscribe — clients get real-time updates
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
                        │       Luxo Router        │
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
- [x] Lexer · Parser (28 keywords, Pratt parser)
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
- [ ] Luxo Router · Service discovery · Circuit breaker
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
