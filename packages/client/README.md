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
const user = await transport.call('getUser', { id: 1 })
const posts = await transport.call('listPosts', { page: 1, pageSize: 20 })
```

## Features

### HTTP/2 Transport

Single TCP connection, multiplexed requests. Uses native `fetch()` — works in browsers, Node.js 18+, Deno, Bun, Cloudflare Workers.

```ts
const transport = new FetchTransport('https://api.example.com/luvia')
```

### WebSocket — Real-time Subscriptions

Auto-reconnect with exponential backoff (1s → 2s → 4s → ... → 30s max):

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
const user = await transport.call('getUser', { id: 1 })
```

### 401 Auto-Refresh

Token expires? The SDK calls your callback, gets a new token, retries automatically:

```ts
const transport = new FetchTransport(endpoint, {
  onTokenExpired: async () => {
    const res = await fetch('/auth/refresh')
    return res.json().then(r => r.token) // null = give up
  },
})
```

### Field Selection — End to End

The magic of Luxo: your client selects fields, the server only serializes those fields, and the SQL only queries those columns.

```ts
// With @luxojs/vite-plugin, this happens automatically at compile time
const user = await transport.call('getUser', {
  id: 1,
  $select: 'name, email, posts { title }',
})
// SQL: SELECT name, email FROM users WHERE id = 1
// + DataLoader: SELECT title FROM posts WHERE user_id IN (1)
```

### Codec Utilities

Low-level binary encoding for custom protocols or advanced use:

```ts
import { Encoder, Decoder, fieldMaskSet, fieldMaskHas } from '@luxojs/client'

const enc = new Encoder()
enc.writeVarint(42)
enc.writeString('hello')
enc.writeSvarint(-100)
enc.writeFixed64(3.14)

const dec = new Decoder(enc.finish())
dec.readVarint()   // 42
dec.readString()   // 'hello'
dec.readSvarint()  // -100
dec.readFixed64()  // 3.14
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

## Links

- [Luxo Framework](https://github.com/light-speak/luxo) · [中文文档](https://github.com/light-speak/luxo/blob/main/README_CN.md)
- [Why "Luxo"?](https://github.com/light-speak/luxo#why-luxo) — The origin story
- [Full comparison table](https://github.com/light-speak/luxo#why-not) — vs GraphQL, gRPC, REST

## License

Apache-2.0 · Copyright 2026 [light-speak](https://github.com/light-speak)
