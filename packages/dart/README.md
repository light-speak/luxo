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
                     Binary (prod, 3x smaller)
```

## Install

```yaml
dependencies:
  luxo_client: ^0.1.0
```

```bash
dart pub get
```

## Quick Start

```dart
import 'package:luxo_client/luxo_client.dart';

final transport = HttpTransport(
  'http://localhost:4000/luvia',
  token: 'your-jwt-token',
  timeout: Duration(seconds: 30),
  onTokenExpired: () async {
    // Auto-refresh on 401 — return new token to retry
    final res = await refreshToken();
    return res.token;
  },
);

// Every API call is one line
final user = await transport.call('getUser', {'id': 1});
final posts = await transport.call('listPosts', {'page': 1, 'pageSize': 20});
```

## Features

### HTTP Transport

Full-featured HTTP client with JSON and binary modes:

```dart
final transport = HttpTransport(
  'https://api.example.com/luvia',
  token: 'jwt-token',
  mode: TransportMode.json,    // or TransportMode.binary
  timeout: Duration(seconds: 30),
);
```

### WebSocket — Real-time Subscriptions

Auto-reconnect with exponential backoff (1s → 2s → 4s → ... → 30s max):

```dart
final ws = WsTransport(
  'wss://api.example.com/ws',
  token: 'jwt-token',
  autoReconnect: true,
);

ws.onMessage = (data) => print('Received: $data');
await ws.connect();
```

### Binary Mode — 3x Smaller Than JSON

JSON in dev for easy debugging. Switch to binary in prod — same API, zero code changes:

```dart
final transport = HttpTransport(
  'https://api.example.com/luvia',
  mode: TransportMode.binary,
);
transport.setSchema(LUXO_SCHEMA); // from codegen
```

### 401 Auto-Refresh

Token expires? The SDK calls your callback, gets a new token, retries automatically:

```dart
final transport = HttpTransport(
  endpoint,
  onTokenExpired: () async {
    final newToken = await myAuthService.refresh();
    return newToken; // null = give up
  },
);
```

### Field Selection — End to End

The magic of Luxo: your client selects fields, the server only serializes those fields, and the SQL only queries those columns.

```dart
final user = await transport.call('getUser', {
  'id': 1,
  '\$select': 'name, email, posts { title }',
});
// SQL: SELECT name, email FROM users WHERE id = 1
// + DataLoader: SELECT title FROM posts WHERE user_id IN (1)
```

### Binary Codec

Low-level varint/svarint/fixed64 encoding for custom protocols:

```dart
final enc = LuxoEncoder();
enc.writeVarint(42);
enc.writeString('hello');
enc.writeSvarint(-100);
enc.writeFixed64(3.14);

final dec = LuxoDecoder(enc.toBytes());
dec.readInt();    // 42
dec.readString(); // 'hello'
dec.readInt();    // -100 (zigzag decoded)
dec.readFloat();  // 3.14
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
- **schema.dart** — API schema map for binary transport
- **client.dart** — typed client with methods per API

```dart
import 'package:your_app/src/luxo/client.dart';

final client = LuxoClient.create(
  'http://localhost:4000/luvia',
  token: 'your-jwt-token',
);

final user = await client.getUser(1);
print(user.name);

final posts = await client.listPosts(page: 1, pageSize: 20);
```

## Compile-time Field Tracking

Using `build_runner`, Luxo analyzes your Dart source code at compile time to detect which fields you access, and generates `$select` hints automatically:

```yaml
# build.yaml
targets:
  $default:
    builders:
      luxo_client|select_hints:
        enabled: true
```

```bash
dart run build_runner build
```

Your code:
```dart
final user = await client.getUser(1);
print(user.name);  // ← detected at compile time
```

Generated hint: `getUser → "name"` — only `name` is fetched from the database.

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
