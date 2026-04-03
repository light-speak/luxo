# Luxo

**Build APIs at the speed of light.**

A high-performance, schema-first Go framework for building modern APIs with binary encoding and client-driven field selection.

[![Tests](https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg)](https://github.com/light-speak/luxo/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg)](https://codecov.io/gh/light-speak/luxo)
[![Go Report Card](https://goreportcard.com/badge/github.com/light-speak/luxo)](https://goreportcard.com/report/github.com/light-speak/luxo)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

[中文文档](README_CN.md)

---

## Why Luxo?

| | GraphQL | gRPC | Luxo |
|---|---|---|---|
| Field Selection | Client-driven, too loose | None (fixed message) | Client-driven, schema-controlled |
| Transport | JSON only | Protobuf only | JSON (dev) / Binary (prod), auto-switch |
| Schema | GraphQL SDL | .proto | `.luxo` (with logic) |
| Coupling | Too loose | Too tight | Just right |
| Code Generation | Separate tooling | protoc | Built-in, one schema generates everything |

## Features

- **Schema-first** — Define your API in `.luxo` files, generate API layer, database layer, and client SDKs
- **Field selection** — Clients request only the fields they need, all the way down to SQL queries
- **Multi-mode transport** — JSON for development, Binary for production, zero code change
- **All-in PostgreSQL** — Postgres by default, pluggable drivers (Redis, NATS, S3) when you need them
- **Built-in RPC** — Luxo is its own protocol. No gRPC, no GraphQL, no extra dependency
- **Zero boilerplate** — Standard CRUD with zero code. Only write Go for complex business logic
- **DataLoader built-in** — N+1 problem solved automatically for all relationships
- **Null safety** — Compile-time null checks, inspired by Kotlin
- **Federation** — Multi-service architecture with gateway aggregation via `extend`

## Quick Start

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
```

## Example

```luxo
model User : Base @crud {
  name:     String @varchar(100) @filterable
  email:    String @unique
  password: String @hidden @hash
  role:     Role = Role.USER
  avatar:   String?
  posts:    [Post]
}

enum Role {
  USER
  ADMIN
}

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
  return AuthResult { token: token, user: user }
}

/// Complex logic? Use Go.
api oauthLogin(provider: String, code: String): AuthResult @native
```

**Client query:**

```
getUser(1) {
  name email
  posts { title createdAt }
}
```

**Response (only requested fields):**

```json
{
  "data": {
    "name": "lin",
    "email": "lin@test.com",
    "posts": [
      { "title": "Hello Luxo", "createdAt": "2026-04-03" }
    ]
  }
}
```

## Architecture

```
Client ←HTTP/2→ Luxo Router ←HTTP/2→ Services
                    │
                    ├── Schema composition (extend)
                    ├── Load balancing
                    ├── Health check & circuit breaker
                    └── Luxo Studio (built-in dashboard)

Single service:  luxo dev
Multi service:   luxo dev --all
```

## Roadmap

### Phase 1 — Core (In Progress)
- [x] Lexer (tokenizer)
- [x] Parser (Pratt parser, AST)
- [ ] Semantic analyzer (type checking, null safety)
- [ ] Go code generator
- [ ] Query builder (pgx)
- [ ] DataLoader
- [ ] JSON transport + field selection
- [ ] Auto CRUD generation
- [ ] CLI (init / generate / dev / run)
- [ ] VS Code extension (LSP)

### Phase 2 — Complete
- [ ] @native functions + resolver smart merge
- [ ] Migration engine (declarative diff)
- [ ] Transaction support
- [ ] @auth + Identity context
- [ ] i18n error handling
- [ ] Validation annotations (@email, @range, etc.)
- [ ] WebSocket stream
- [ ] Batch requests

### Phase 3 — Multi-service
- [ ] Luxo Router
- [ ] Schema composition + extend aggregation
- [ ] Service discovery + load balancing
- [ ] Health check + circuit breaker
- [ ] Inter-service Luxo RPC
- [ ] Identity context propagation

### Phase 4 — Ecosystem
- [ ] Binary transport protocol
- [ ] Client SDK generation (TypeScript / Dart)
- [ ] Luxo Studio (monitoring dashboard)
- [ ] Built-in capabilities (Cache / Event / Task / Scheduler / Storage / Mail)
- [ ] Monitoring & tracing
- [ ] k3s deployment generation
- [ ] .luxo test runner

## Contributing

Luxo is in early development. Contributions, ideas, and feedback are welcome.

## License

Copyright 2026 light-speak

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
