# luxo_client (Dart)

> Dart/Flutter client for Luxo API — HTTP, WebSocket, Binary codec, AST field tracking

Dart/Flutter 客户端 — HTTP 传输、WebSocket 订阅、Binary 编解码、AST 字段追踪

## Install / 安装

```yaml
dependencies:
  luxo_client: ^0.1.0
```

## Usage / 使用

```dart
import 'package:luxo_client/luxo_client.dart';

final transport = HttpTransport('http://localhost:4000/luvia',
  options: TransportOptions(token: 'your-jwt-token'),
);
final client = LuxoClient(transport);
final user = await client.getUser(1);
print(user.name);
```

## Features / 功能

- **HTTP Transport** — Dart http package / Dart HTTP 传输
- **WebSocket** — dart:io WebSocket / WebSocket 订阅
- **Binary Codec** — LuxoEncoder/LuxoDecoder / 二进制编解码
- **Code Generation** — `dart run luxo_client:generate` / 代码生成
- **AST Field Tracking** — `package:analyzer` based / AST 字段追踪
- **Enum + Type codegen** — typedef + Values class / 枚举 + 类型代码生成
- **Nested $select** — Deep relation tracking / 嵌套关系追踪
- **Depth Warning** — > 5 levels warning / 深度超 5 层警告
