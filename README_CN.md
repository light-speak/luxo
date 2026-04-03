# Luxo

**Build APIs at the speed of light.**

一个高性能、Schema 驱动的 Go 框架，用于构建现代 API。支持二进制编码和客户端按需字段选择。

[![Tests](https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg)](https://github.com/light-speak/luxo/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg)](https://codecov.io/gh/light-speak/luxo)
[![Go Report Card](https://goreportcard.com/badge/github.com/light-speak/luxo)](https://goreportcard.com/report/github.com/light-speak/luxo)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

[English](README.md)

---

## 为什么选 Luxo？

| | GraphQL | gRPC | Luxo |
|---|---|---|---|
| 字段选择 | 客户端自由查询，太松 | 无（固定 message） | 客户端按需选择，Schema 控制 |
| 传输格式 | 只有 JSON | 只有 Protobuf | JSON（开发）/ Binary（生产），自动切换 |
| Schema | GraphQL SDL | .proto | `.luxo`（可以写逻辑） |
| 耦合度 | 太松 | 太紧 | 适度 |
| 代码生成 | 需要额外工具 | protoc | 内置，一份 schema 生成一切 |

## 核心特性

- **Schema 驱动** — 定义一次 `.luxo` 文件，自动生成 API 层、数据库层、客户端 SDK
- **字段选择** — 客户端只请求需要的字段，减少传输体积，SQL 也只查需要的列
- **多档传输** — 开发环境 JSON，生产环境 Binary，零代码改动
- **All-in PostgreSQL** — 默认全部走 Postgres，按需切换专用驱动（Redis、NATS、S3）
- **自研协议** — Luxo 就是自己的协议，不依赖 gRPC，不依赖 GraphQL
- **零样板代码** — 标准 CRUD 零代码，只有复杂业务逻辑才写 Go
- **DataLoader 内置** — 所有关联关系自动解决 N+1 问题
- **空安全** — 编译期空值检查，借鉴 Kotlin
- **Federation** — 多服务架构，网关聚合，`extend` 扩展

## 快速开始

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
```

## 示例

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

/// 注册新用户
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

/// 复杂逻辑？用 Go 写。
api oauthLogin(provider: String, code: String): AuthResult @native
```

**客户端查询：**

```
getUser(1) {
  name email
  posts { title createdAt }
}
```

**响应（只返回请求的字段）：**

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

## 架构

```
客户端 ←HTTP/2→ Luxo Router ←HTTP/2→ 各服务
                    │
                    ├── Schema 组装（extend 聚合）
                    ├── 负载均衡
                    ├── 健康检查 + 熔断
                    └── Luxo Studio（内置监控面板）

单服务:  luxo dev
多服务:  luxo dev --all
```

## 开发进度

### Phase 1 — 核心（进行中）
- [x] 词法分析器（Lexer）
- [x] 语法分析器（Parser，Pratt Parser）
- [ ] 语义分析（类型检查、空安全窄化）
- [ ] Go 代码生成
- [ ] 查询构建器（pgx）
- [ ] DataLoader
- [ ] JSON 传输 + 字段选择
- [ ] 自动 CRUD 生成
- [ ] CLI（init / generate / dev / run）
- [ ] VS Code 扩展（LSP + 语法高亮）

### Phase 2 — 完善
- [ ] @native 函数 + resolver 智能合并
- [ ] 迁移引擎（声明式 diff）
- [ ] 事务支持
- [ ] @auth + Identity 上下文
- [ ] i18n 错误处理
- [ ] 校验注解（@email、@range 等）
- [ ] WebSocket 流式推送
- [ ] Batch 请求

### Phase 3 — 多服务
- [ ] Luxo Router
- [ ] Schema 组装 + extend 聚合
- [ ] 服务发现 + 负载均衡
- [ ] 健康检查 + 熔断
- [ ] 服务间 Luxo RPC
- [ ] Identity 上下文传递

### Phase 4 — 生态
- [ ] Binary 传输协议
- [ ] 客户端 SDK 生成（TypeScript / Dart）
- [ ] Luxo Studio（监控面板）
- [ ] 内置能力（Cache / Event / Task / Scheduler / Storage / Mail）
- [ ] 监控与追踪
- [ ] k3s 部署生成
- [ ] .luxo 测试运行器

## 贡献

Luxo 处于早期开发阶段，欢迎贡献代码、提出想法和反馈。

## 许可证

Copyright 2026 light-speak

基于 Apache License 2.0 授权。详见 [LICENSE](LICENSE) 文件。
