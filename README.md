<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  A schema-first Go framework that replaces REST, GraphQL, and gRPC<br/>
  with one language, one protocol, one toolchain.
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
  <a href="#example">Example</a> ·
  <a href="#roadmap">Roadmap</a>
</p>

---

## The Problem

You're writing the same thing three times: **schema**, **API handlers**, **database queries**. You're choosing between protocols that each solve half the problem. You're gluing together 10 different tools just to serve a JSON response.

## The Solution

**One `.luxo` file. Everything else is generated.**

```luxo
model User : Base @crud {
  name:     String @varchar(100) @filterable
  email:    String @unique
  password: String @hidden @hash
  avatar:   String?
  posts:    [Post]
}
```

This single definition generates: Go structs, database tables, CRUD APIs, query builders, DataLoaders, client SDKs, migration files. Zero boilerplate.

## Why Not...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| Field Selection | ✅ Too loose | ❌ None | ❌ None | ✅ Schema-controlled |
| Binary Transport | ❌ JSON only | ✅ Protobuf | ❌ JSON only | ✅ JSON + Binary, auto-switch |
| One Schema | ❌ SDL + ORM + OpenAPI | ❌ .proto + ORM | ❌ Manual | ✅ `.luxo` generates everything |
| Code Gen | ❌ Separate tools | ⚠️ protoc | ❌ Manual | ✅ Built-in |
| N+1 Prevention | ❌ Manual DataLoader | N/A | ❌ Manual | ✅ Automatic |
| Type Safety | ⚠️ Runtime | ✅ Compile-time | ❌ None | ✅ Compile-time + null safety |
| Multi-service | ⚠️ Federation (complex) | ✅ Native | ❌ Manual | ✅ `extend` + gateway |

## Features

- **Schema-first** — `.luxo` DSL with Kotlin-inspired syntax, logic included
- **Field selection** — Client → API → SQL, only requested fields at every layer
- **Multi-mode transport** — `JSON` for dev, `Binary` for prod, middleware auto-switch
- **All-in PostgreSQL** — Postgres by default, pluggable drivers (Redis, NATS, S3)
- **Built-in RPC** — Luxo IS the protocol. No gRPC, no GraphQL dependency
- **Zero boilerplate** — `@crud` generates everything. Write Go only for `@native` logic
- **Null safety** — Compile-time checks. `?` for nullable, `?:` for fallback, `?.` for safe access
- **DataLoader built-in** — N+1 solved automatically for all relationships
- **Federation** — `extend` for multi-service, gateway aggregation, identity propagation
- **Luxo Studio** — Built-in monitoring dashboard, request tracing, API playground

## Quick Start

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
```

## Example

**Define your API:**

```luxo
model User : Base @crud {
  name:     String @varchar(100) @filterable
  email:    String @unique
  password: String @hidden @hash
  role:     Role = Role.USER
  avatar:   String?
  posts:    [Post]
}

enum Role { USER ADMIN }

/// Register a new user
api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw error.password_too_short

  val exists = find(User, where: email == input.email)
  exists == null ?: throw error.email_exists

  val user = create(User,
    name: input.name,
    email: input.email,
    password: input.password
  )

  val token = generateToken(user, expires: 7d)
  AuthResult { token: token, user: user }
}

/// Complex logic? Write Go.
api oauthLogin(provider: String, code: String): AuthResult @native
```

**Query with field selection:**

```
getUser(1) {
  name email
  posts {
    title createdAt
    comments { content user { name } }
  }
}
```

**Response — only what you asked for:**

```json
{
  "data": {
    "name": "lin",
    "email": "lin@test.com",
    "posts": [
      {
        "title": "Hello Luxo",
        "createdAt": "2026-04-03",
        "comments": [
          { "content": "Great!", "user": { "name": "alice" } }
        ]
      }
    ]
  }
}
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

## VS Code Extension

Syntax highlighting, auto-completion, real-time error reporting, go-to-definition, and hover info for `.luxo` files.

```bash
# Auto-installs language server on first use
code --install-extension luxo-0.2.0.vsix
```

## Roadmap

### Phase 1 — Compiler ✅
- [x] Lexer · Parser · Semantic Analyzer · LSP
- [x] 442 tests · 99%+ coverage · Fuzz testing
- [ ] Code generator · Query builder · DataLoader

### Phase 2 — Framework
- [ ] JSON transport + field selection
- [ ] Auto CRUD · @native resolver merge
- [ ] Migration engine · Auth · i18n · Validation
- [ ] WebSocket stream · Batch requests

### Phase 3 — Multi-service
- [ ] Luxo Router · Service discovery · Circuit breaker
- [ ] Inter-service RPC · Identity propagation

### Phase 4 — Ecosystem
- [ ] Binary protocol · Client SDK (TS / Dart)
- [ ] Luxo Studio · Cache / Event / Task / Storage
- [ ] k3s deployment · .luxo test runner

## Contributing

Luxo is in early development. Contributions, ideas, and feedback are welcome.

## License

Apache-2.0 · Copyright 2026 light-speak
