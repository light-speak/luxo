# @luxo/client

> TypeScript/JavaScript client for Luxo API — HTTP/2, WebSocket, Binary codec

TypeScript/JavaScript 客户端 — HTTP/2 传输、WebSocket 订阅、Binary 编解码

## Install / 安装

```bash
pnpm add @luxo/client
```

## Usage / 使用

```ts
import { FetchTransport } from '@luxo/client'

const transport = new FetchTransport('http://localhost:4000/luvia', {
  token: 'your-jwt-token',
  timeout: 30000, // 30s timeout
  onTokenExpired: async () => {
    // Auto-refresh on 401
    const { token } = await refreshToken()
    return token
  },
})

const result = await transport.call('getUser', { id: 1 })
```

## Features / 功能

- **HTTP/2 Transport** — Single connection, multiplexed requests / 单连接多路复用
- **WebSocket** — Streaming subscriptions with auto-reconnect / 流式订阅 + 自动重连
- **Binary Codec** — Varint/svarint/fixed64 wire format / 二进制编解码
- **Field Mask** — Compile-time field selection / 编译期字段选择
- **Request Timeout** — Configurable with AbortController / 可配置超时
- **401 Auto-Refresh** — Token expired callback / Token 过期自动刷新
- **WeChat Mini Program** — WxTransport / 微信小程序支持

## Binary Mode / 二进制模式

```ts
transport.setMode('binary')
transport.setSchema(LUXO_SCHEMA) // from codegen
```
