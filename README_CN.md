<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  一个 Schema 驱动的 Go 框架，用一种语言、一套协议、一套工具链<br/>
  取代 REST、GraphQL 和 gRPC。
</p>

<p align="center">
  <a href="https://github.com/light-speak/luxo/actions/workflows/test.yml"><img src="https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg" alt="Tests" /></a>
  <a href="https://codecov.io/gh/light-speak/luxo"><img src="https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg" alt="codecov" /></a>
  <a href="https://goreportcard.com/report/github.com/light-speak/luxo"><img src="https://goreportcard.com/badge/github.com/light-speak/luxo" alt="Go Report Card" /></a>
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go" alt="Go Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#示例">示例</a> ·
  <a href="#开发进度">开发进度</a>
</p>

---

## 问题

你在重复写同一件事三遍：**Schema**、**API Handler**、**数据库查询**。你在 REST、GraphQL、gRPC 之间纠结，每个都只解决了一半问题。你在用 10 个工具粘合，只是为了返回一个 JSON。

## 解决方案

**一个 `.luxo` 文件，其他全部生成。**

```luxo
model User : Base @crud {
  name:     String @varchar(100) @filterable
  email:    String @unique
  password: String @hidden @hash
  avatar:   String?
  posts:    [Post]
}
```

这一段定义自动生成：Go struct、数据库表、CRUD API、查询构建器、DataLoader、客户端 SDK、迁移文件。零样板代码。

## 为什么不用...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| 字段选择 | ✅ 太松散 | ❌ 没有 | ❌ 没有 | ✅ Schema 控制 |
| 二进制传输 | ❌ 只有 JSON | ✅ Protobuf | ❌ 只有 JSON | ✅ JSON + Binary 自动切换 |
| 一份 Schema | ❌ SDL + ORM + OpenAPI | ❌ .proto + ORM | ❌ 手写 | ✅ `.luxo` 生成一切 |
| 代码生成 | ❌ 需要额外工具 | ⚠️ protoc | ❌ 手写 | ✅ 内置 |
| N+1 防护 | ❌ 手写 DataLoader | N/A | ❌ 手写 | ✅ 自动 |
| 类型安全 | ⚠️ 运行时 | ✅ 编译期 | ❌ 没有 | ✅ 编译期 + 空安全 |
| 多服务 | ⚠️ Federation（复杂） | ✅ 原生 | ❌ 手写 | ✅ `extend` + 网关 |

## 核心特性

- **Schema 驱动** — `.luxo` DSL，Kotlin 风格语法，可以写逻辑
- **字段选择** — 客户端 → API → SQL，每一层只处理请求的字段
- **多档传输** — 开发 `JSON`，生产 `Binary`，Middleware 自动切换
- **All-in PostgreSQL** — 默认 Postgres，可插拔驱动（Redis、NATS、S3）
- **自研 RPC 协议** — Luxo 就是协议本身，不依赖 gRPC 和 GraphQL
- **零样板代码** — `@crud` 生成一切，只在 `@native` 写 Go
- **空安全** — 编译期检查，`?` 可空，`?:` 兜底，`?.` 安全访问
- **DataLoader 内置** — 所有关联关系自动解决 N+1
- **Federation** — `extend` 跨服务扩展，网关聚合，Identity 传递
- **Luxo Studio** — 内置监控面板，请求追踪，API Playground

## 快速开始

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
```

## 示例

**定义 API：**

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
  AuthResult { token: token, user: user }
}

/// 复杂逻辑？写 Go。
api oauthLogin(provider: String, code: String): AuthResult @native
```

**客户端查询（按需选字段）：**

```
getUser(1) {
  name email
  posts {
    title createdAt
    comments { content user { name } }
  }
}
```

**响应 — 只返回你要的：**

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
          { "content": "写得好！", "user": { "name": "alice" } }
        ]
      }
    ]
  }
}
```

## 架构

```
                        ┌──────────────────────────┐
                        │       Luxo Router        │
  客户端 ──HTTP/2──────►│  Schema · 负载均衡 · 熔断  │──HTTP/2──► 各服务
        ◄─WebSocket────│  Studio · Identity       │
                        └──────────────────────────┘
                              │          │
                        ┌─────┘          └─────┐
                        ▼                      ▼
                  ┌───────────┐          ┌───────────┐
                  │  User 服务 │          │ Order 服务 │
                  │           │──NATS───►│           │
                  └───────────┘          └───────────┘

  单服务:  luxo dev
  多服务:  luxo dev --all
```

## VS Code 扩展

语法高亮、自动补全、实时错误提示、跳转定义、悬停类型信息。

```bash
# 首次使用自动安装语言服务器
code --install-extension luxo-0.2.0.vsix
```

## 开发进度

### Phase 1 — 编译器 ✅
- [x] 词法分析 · 语法分析 · 语义分析 · LSP
- [x] 442 测试 · 99%+ 覆盖率 · Fuzz 测试
- [ ] 代码生成 · 查询构建器 · DataLoader

### Phase 2 — 框架
- [ ] JSON 传输 + 字段选择
- [ ] 自动 CRUD · @native 智能合并
- [ ] 迁移引擎 · 认证 · i18n · 校验
- [ ] WebSocket 流式 · Batch 请求

### Phase 3 — 多服务
- [ ] Luxo Router · 服务发现 · 熔断
- [ ] 服务间 RPC · Identity 传递

### Phase 4 — 生态
- [ ] Binary 协议 · 客户端 SDK（TS / Dart）
- [ ] Luxo Studio · Cache / Event / Task / Storage
- [ ] k3s 部署 · .luxo 测试运行器

## 贡献

Luxo 处于早期开发阶段，欢迎贡献代码、提出想法和反馈。

## 许可证

Apache-2.0 · Copyright 2026 light-speak
