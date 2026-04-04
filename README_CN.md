<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  一门自带 API 框架的通用编程语言。<br/>
  一种语言、一套协议、一套工具链 — 取代 REST、GraphQL 和 gRPC。
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
  <a href="#语言特性">语言特性</a> ·
  <a href="#开发进度">开发进度</a>
</p>

---

## 为什么叫 "Luxo"？

每一个 API 请求的本质，就是数据从起源到达它该去的地方。但这条路上被塞满了冗余 — JSON 重复的字段名、GraphQL 运行时解析的开销、ORM 的反射损耗、`SELECT *` 查出来又扔掉的字段。

我们回到起源，问一个最基本的问题：数据从数据库到客户端，最少需要经过多少步？每一步最少需要多少字节？

答案就是 Luxo。

**Lux**（拉丁语，*光*）— 数据应该以光速到达。不是比喻，是工程目标。二进制编码、编译期查询计划、全链路字段选择，每一层都在逼近物理极限。

**O**（*origin*，起源）— 回归 API 通信的起源。Schema 是唯一的 source of truth，从它出发，数据库表、类型定义、编解码器、客户端 SDK 全部生成。没有冗余层，没有重复定义，一切从起源生长出来。

> **Luxo — 让数据以光速，从一个起源出发。**

## Luxo 是什么？

Luxo 是一门**编程语言** — 编译到 Go，自带 API 框架、自研协议和完整工具链。

写 `.luxo` 文件，得到：API 服务、数据库层、客户端 SDK、迁移文件、监控面板。不需要粘合代码。

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

一段定义生成一切。零样板代码。

## 为什么不用...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| 字段选择 | ✅ 太松散 | ❌ 没有 | ❌ 没有 | ✅ Schema 控制 |
| 二进制传输 | ❌ 只有 JSON | ✅ Protobuf | ❌ 只有 JSON | ✅ JSON + Binary 自动切换 |
| 一份 Schema | ❌ SDL + ORM + OpenAPI | ❌ .proto + ORM | ❌ 手写 | ✅ `.luxo` 生成一切 |
| N+1 防护 | ❌ 手写 | N/A | ❌ 手写 | ✅ 自动 DataLoader |
| 空安全 | ⚠️ 运行时 | ✅ 编译期 | ❌ 没有 | ✅ 编译期 `?` `?:` `?.` |
| 错误处理 | ❌ 无类型 | ✅ 状态码 | ❌ HTTP 码 | ✅ `Result<T>` + `?` 操作符 |
| 并发 | ❌ 无 | ❌ 手写 | ❌ 手写 | ✅ `async` `await` `Channel` |
| 多服务 | ⚠️ Federation | ✅ 原生 | ❌ 手写 | ✅ `extend` + 网关 + RPC |

## 语言特性

Luxo 是一门真正的编程语言 — 不只是 Schema DSL。**28 个关键字**，简洁语法，编译到 Go。

### 空安全

```luxo
val user = find(User, id: 1)       // User?（可空）
val name = user?.name               // 安全访问
val sure = user ?: throw error.not_found  // Elvis — 断言或抛错
```

### 模式匹配

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

### Result 类型 + `?` 操作符

错误用 `?` 传播 — 不需要 try/catch，不需要 async/await 传染。

```luxo
val user = findUser(1)?              // Ok → 取值，Err → 自动 throw
val data = http.get(url)?            // 错误自动传播
```

### 并发 — 没有 async/await 传染

```luxo
// 所有调用看起来都是同步的 — Go runtime 自动调度
val user = fetchUser(1)              // 不需要 await

// 并发执行 — 只在需要时用
val (user, posts) = await {
  fetchUser(1)                       // 同时跑
  fetchPosts(1)                      // 同时跑
}

// 启动后台任务
async {
  sendEmail(user.email, "Welcome!")
}

// Channel
val ch = Channel<Int>(10)
ch <- 42                             // 发送
val value = <-ch                     // 接收
```

### yield — for 循环作为表达式

```luxo
val found = for item in items {
  if item.special { yield item }     // 退出循环并返回值
}
// found: Item? — yield 的值，未找到则 null
```

### 集合操作

```luxo
val total = items.sumOf { it.price * it.quantity }
val active = users.filter { it.status == "active" }
val names = users.map { it.name }.joinToString(", ")
val vip = users.any { it.role == Role.VIP }
```

### API 定义 — 三个层级

```luxo
// 1. 零代码 — 框架自动生成
api getUser(id: Int): User @cache(ttl: 60)

// 2. 带逻辑 — 用 Luxo 写
api register(input: RegisterInput): AuthResult {
  input.password.length >= 8 ?: throw error.password_too_short
  val user = create(User, name: input.name, email: input.email, password: input.password)
  val token = generateToken(user, expires: 7d)
  AuthResult { token: token, user: user }
}

// 3. 复杂逻辑 — 用 Go 写
api oauthLogin(provider: String, code: String): AuthResult @native
```

### 字段选择 — 贯穿全链路

```
getUser(1) {
  name email
  posts { title comments { content user { name } } }
}
```

客户端选字段 → API 只序列化这些 → SQL 只查这些。端到端。

### 事件 + 实时推送

```luxo
// 发送事件 — 框架自动处理 WebSocket + 消息队列
emit("order.created", order, userId: order.userId)

// 订阅 — 客户端实时收到更新
api watchComments(postId: Int): stream Comment
```

## 快速开始

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo dev
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

## 编译器

完整的编译器管线 + IDE 支持：

```
.luxo 源码 → 词法分析 → 语法分析 → 语义分析 → Go 代码生成
                                       ↓
                                 LSP 服务器
                                       ↓
                    实时错误 · 自动补全 · 悬停信息 · 跳转定义
```

- **510 个测试** · 覆盖率达极限
- **Fuzz 测试** — 词法、语法、语义分析器
- **双语错误信息**（English + 中文）

## 开发进度

### Phase 1 — 编译器 ✅
- [x] 词法分析 · 语法分析（28 关键字，Pratt Parser）
- [x] 语义分析（类型检查、空安全、字段注入）
- [x] LSP 服务器（诊断、补全、悬停、跳转定义）
- [x] 510 个测试 · 极限覆盖率 · Fuzz 测试
- [ ] Go 代码生成

### Phase 2 — 框架
- [ ] JSON 传输 + 字段选择
- [ ] 自动 CRUD · @native 智能合并
- [ ] 迁移引擎（声明式 diff）
- [ ] 认证 · i18n · 校验
- [ ] WebSocket 流式 · Batch 请求
- [ ] 运行时：错误处理、连接池、日志、消息系统

### Phase 3 — 多服务
- [ ] Luxo Router · 服务发现 · 熔断
- [ ] 服务间 Luxo RPC
- [ ] Identity 上下文传递

### Phase 4 — 生态
- [ ] Binary 协议 · 客户端 SDK（TypeScript / Dart）
- [ ] Luxo Studio（监控面板）
- [ ] Cache / Event / Task / Storage / Mail
- [ ] k3s 部署 · .luxo 测试运行器

## 贡献

Luxo 处于早期开发阶段，欢迎贡献代码、提出想法和反馈。

## 许可证

Apache-2.0 · Copyright 2026 light-speak
