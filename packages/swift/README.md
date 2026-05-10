# LuxoClient (Swift)

> Swift/iOS client for Luxo API — URLSession HTTP/2, WebSocket, Binary codec, SwiftSyntax analyzer

Swift/iOS 客户端 — URLSession HTTP/2 传输、WebSocket 订阅、Binary 编解码、SwiftSyntax 字段追踪

## Install / 安装

```swift
// Package.swift
dependencies: [
    .package(url: "https://github.com/light-speak/luxo-swift.git", from: "0.1.0"),
]
```

## Usage / 使用

```swift
let transport = URLSessionTransport(endpoint: "http://localhost:4000/luvia", token: "your-jwt-token")
let result = try await transport.call("getUser", params: ["id": 1])
```

## WebSocket / 订阅

```swift
let ws = WebSocketTransport(url: "ws://localhost:4000/luvia/ws")
ws.connect()
ws.subscribe("liveTraces", params: ["projectId": 1]) { data in
    print("Trace: \(data)")
}
```

## Code Generation / 代码生成

```bash
# Generate typed client from schema
swift run LuxoCodegen --endpoint http://localhost:4000/luvia --key YOUR_KEY --output Sources/Generated/

# Analyze field access patterns
swift run LuxoAnalyze --source-dir Sources/ --output Sources/Generated/SelectHints.swift
```

## Features / 功能

- **URLSession Transport** — HTTP/2 multiplexed / URLSession HTTP/2 多路复用
- **WebSocket** — URLSessionWebSocketTask / WebSocket 订阅
- **Binary Codec** — Encoder/Decoder (varint, svarint, fixed64) / 二进制编解码
- **SwiftSyntax Analyzer** — AST field tracking / SwiftSyntax AST 字段追踪
- **Code Generation** — Models + Client from schema / 从 schema 生成模型 + 客户端
- **Enum codegen** — Swift enum with CaseIterable / Swift 枚举
- **Nested $select** — Deep relation tracking / 嵌套关系追踪
- **Depth Warning** — > 5 levels warning / 深度超 5 层警告
