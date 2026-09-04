<p align="center">
  <img src="assets/logo.svg" alt="Luxo" width="360" />
</p>

<h3 align="center">Build APIs at the speed of light.</h3>

<p align="center">
  Schema-first 的编译型后端语言与平台。<br/>
  一种语言、一套协议、一套面向 API 与数据服务的工具链。
</p>

<p align="center">
  <a href="https://github.com/light-speak/luxo/actions/workflows/test.yml"><img src="https://github.com/light-speak/luxo/actions/workflows/test.yml/badge.svg" alt="Tests" /></a>
  <a href="https://codecov.io/gh/light-speak/luxo"><img src="https://codecov.io/gh/light-speak/luxo/branch/main/graph/badge.svg" alt="codecov" /></a>
  <a href="https://goreportcard.com/report/github.com/light-speak/luxo"><img src="https://goreportcard.com/badge/github.com/light-speak/luxo?v=2" alt="Go Report Card" /></a>
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

**Luxo** /lɑːkèsuǒ/ — 数据从数据库到达客户端，这条路本该很短。

但我们把它走成了迷宫。JSON 在每个字段前都重复一遍名字，像一份每行都写着抬头的公文。GraphQL 在运行时才去解析查询，而那些查询在编译期就已经写死了。ORM 用反射一遍遍翻译结构体，做着编译器早就做完的事。`SELECT *` 把整张表捞出来，再亲手丢掉大半。

每一层都在重新发现上一层已经知道的答案。

我们退回到最开始，只问一个问题：**从存储到屏幕，最少需要几步？每一步最少需要多少字节？**

沿着这个问题走到底，就是 Luxo。

**Lux**（拉丁语，*光*）— 这不是一个比喻，而是一个工程约束。二进制编码在编译期确定，查询计划在编译期生成，字段选择贯穿客户端到 SQL — 每一层都在逼近传输的物理极限。数据应该像光一样到达：没有绕路，没有损耗。

**O**（*origin*，起源）— 一切从 Schema 开始，也只从 Schema 开始。数据库表、类型定义、编解码器、客户端 SDK，全部从同一份 `.luxo` 文件生长出来。没有第二个 source of truth，没有需要手动同步的定义，改一个字段名不需要同时动三个文件。

> **Luxo — 一个起源，光速抵达。**

## Luxo 是什么？

Luxo 是一门 **Schema-first 的编译型后端语言与平台**。它把 `.luxo` 编译为 Go 服务、数据库访问、迁移、类型化 SDK 元数据和部署入口。Luvia 基于同一份 Schema，通过 HTTP、WebSocket 和原生 RPC 提供统一的 JSON 与 Luxo Binary 能力。

Luxo 的目标是统一通常分散在 REST、GraphQL、gRPC、ORM 和手写客户端胶水中的工作。当前已实现的数据库后端是 PostgreSQL；MySQL、SQLite、MongoDB 仍是规划目标。

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

一份源定义驱动服务端、数据库层、迁移、Schema 与 SDK 契约。

## 为什么不用...

| | GraphQL | gRPC | REST | **Luxo** |
|---|---|---|---|---|
| **字段选择** | ✅ 支持但过于松散，客户端可任意构造深层嵌套查询，需额外配置深度/复杂度限制防止滥用 | ❌ 不支持，响应固定为 proto 定义的完整结构（`FieldMask` 可实现但需手动处理） | ❌ 不支持，每个端点返回固定字段，需手动实现 `?fields=` 参数 | ✅ Schema 级别声明字段可见性，编译期校验选择合法性，贯穿到 SQL 只查所选列 |
| **二进制传输** | ❌ 规范不限编码格式，但实践中几乎只用 JSON，大数据量场景序列化开销显著 | ✅ Protobuf 二进制编码，高效紧凑 | ❌ 可通过 Content Negotiation 支持任意格式，但实践中以 JSON 为主，无标准二进制方案 | ✅ JSON 与 Luxo Binary 共用一份 Schema，由 SDK 模式或 `X-Luxo-Mode` 明确选择 wire 格式 |
| **一份 Schema** | ❌ SDL 定义 API 接口，但仍需独立维护 ORM 数据库映射，两处定义容易不同步（code-first 工具可缓解） | ❌ `.proto` 定义接口 + ORM 定义数据库，两套定义需手动保持一致 | ❌ 无 Schema 驱动，路由 / 模型 / 文档全部手写 | ✅ 一份 `.luxo` 生成 API 接口、数据库迁移、客户端 SDK 和文档，单一事实来源 |
| **N+1 防护** | ❌ Resolver 模式天然引发 N+1，需手动集成 DataLoader（已是标准实践，部分框架如 Hasura 可自动解决） | N/A 不涉及嵌套字段解析场景 | ❌ 嵌套资源需手动优化查询或引入预加载逻辑 | ✅ 编译器自动分析关联关系，生成 DataLoader 批量加载，无需手动干预 |
| **空安全** | ⚠️ SDL `!` 标记非空，服务端运行时校验；客户端可通过 codegen 获得编译期类型安全 | ✅ Protobuf 字段有默认零值，编译期类型安全（但 proto3 无法区分"零值"和"未设置"） | ❌ 无任何空安全保障，null 错误只能运行时发现 | ✅ 语言级编译期空安全：`?` 可空声明、`?.` 安全访问、`?:` Elvis 兜底，空值错误编译阶段拦截 |
| **错误处理** | ❌ `errors` 数组结构松散，仅有 message 字符串 + 可选 extensions，客户端难以结构化处理 | ✅ gRPC Status 标准 16 种状态码 + 富错误详情，类型明确 | ❌ 依赖 HTTP 状态码 + 自定义 JSON body，无统一错误结构规范 | ✅ 统一的结构化传输错误 envelope；native `Result<T>` 降级为 Go `(T, error)`，由 `?` 自动传播失败 |
| **并发** | ⚠️ 多数实现自动并行执行独立 resolver（gqlgen / Apollo / graphql-java），但无语言级并发原语，复杂编排仍依赖宿主语言 | ❌ 支持双向流式传输，但并发编排逻辑需开发者手动管理 | ❌ 无内建并发支持，完全依赖框架或手写线程/协程管理 | ✅ 语言内建 `async` / `await` + `Channel` — 编译到 Go goroutine 和 channel，零成本并发 |
| **多服务** | ⚠️ Apollo Federation 成熟但运维复杂，需额外网关层和服务间协调 | ⚠️ 原生点对点 RPC 调用，但多服务编排仍需服务网格/发现（Istio、Consul 等） | ❌ 服务间调用需手写 HTTP 客户端或引入额外框架 | ✅ `extend` 跨服务扩展类型 + 内建网关路由 + 原生 RPC 调用，多服务协作开箱即用 |
| **扩展性** | ❌ 单体迁移到 Federation 需要重写 resolver、添加 `@key`/`@external` 注解、部署 Apollo Router | ❌ 服务拆分需要重新定义 `.proto`、重新生成 stub、重写调用代码 | ❌ 每次拆分都要新建路由、新写 HTTP 客户端、新建部署配置 | ✅ 编译器从同一份 Schema 契约生成 embedded/cluster 入口、RPC 路由和 Federation loader |

## 语言特性

Luxo 是后端编程语言，不只是 Schema DSL。标量类型包括 `Int`、`Float`、`String`、`Boolean`、`DateTime`、`Duration`、`UUID`、`Decimal`、`Bytes`、`JSON`；泛型运行时类型包括 `Result<T>`、`Channel<T>`、`Page<T>`、`Cursor<T>`。Luxo 编译到 Go。

### 空安全

```luxo
val user = User.find(id: 1)        // User?（可空）
val name = user?.name               // 安全访问
val sure = user ?: throw NotFound   // Elvis — 断言或抛错
```

### 模式匹配 — `when`

`when` 取代 if/else 多分支逻辑：

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
fn loadUser(id: Int): Result<User> @native

api getUser(id: Int): User {
  loadUser(id)?                      // 成功时解包，失败时传播 error
}
```

`Result<T>` 是 Go-backed native 函数的 ABI。公开 API 声明的是响应 payload 类型（上例为 `User`）；传输错误使用统一的结构化错误 envelope。

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

// Channel — 编译到 Go channel
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

### 变量 — `val` + `var`

```luxo
val name = "不可变"                   // 不可变（全局 + 局部）
var count = 0                        // 可变（仅限局部）
count += 1
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
  input.password.length >= 8 ?: throw PasswordTooShort
  val user = User.create(name: input.name, email: input.email, password: input.password)
  val token = generateToken(user, expires: 7d)
  AuthResult { token, user }         // 简写 — 字段名 = 变量名
}

// 3. 复杂逻辑 — 用 Go 写
api oauthLogin(provider: String, code: String): AuthResult @native
```

### 模块 — `use`

```luxo
use http                             // 标准库导入
use common.{ Base, Page }           // 展开导入
```

### 当前用户 — `my`

```luxo
api createPost(title: String): Post @auth {
  val post = Post.create(title: title, userId: my.id)
  post
}
```

### 事件 — `event` / `emit` / `on`

```luxo
event OrderCreated(order: Order, userId: Int)    // 类型化事件声明

api placeOrder(id: Int): Order @auth {
  val order = Order.create(userId: my.id)
  emit OrderCreated(order: order, userId: my.id) // 类型化 emit
  order
}

on OrderCreated { order ->                       // 事件监听
  "order created".i                              // .i = info 日志
}
```

### 调试链 — `.d`

```luxo
val user = User.find(id: 1).d        // .d 打印并返回自身
```

### 字段选择 — 贯穿全链路

```
getUser(1) {
  name email
  posts { title comments { content user { name } } }
}
```

客户端选字段 → API 只序列化这些 → SQL 只查这些。端到端。

生成的 SDK 输出模型精确保留三种字段状态：未选择、已选择且为 `null`、已选择且有值；
解码器不会为缺失字段伪造零值。输入 DTO 保持严格；同一个 Schema 类型同时用于输入和输出时，
codegen 会生成用于选择输出的 `Foo` 和用于输入的 `FooInput`。

### 实时流

```luxo
event DanmakuSent(danmaku: Danmaku)
event NotificationCreated(notification: Notification)

// 事件驱动 + 过滤器
api watchDanmaku(roomId: Int): Danmaku @stream(DanmakuSent) {
  it.roomId == roomId
}

// 身份过滤
api watchNotifications: Notification @stream(NotificationCreated) @auth {
  it.userId == my.id
}

// Go 完全控制
api watchLiveScore(matchId: Int): ScoreEvent @stream @native
```

## 快速开始

```bash
go install github.com/light-speak/luxo/cmd/luxo@latest

luxo init my-app
cd my-app
luxo add user
cp .env.example .env
luxo gen
luxo run
```

## 架构

> [查看完整架构图](assets/architecture.svg)

**Luvia 始终在线。** embedded 模式把所有模块和网关放在同一进程；cluster 模式使用生成的模块服务二进制和独立网关。RPC 路由与 Federation loader 都来自同一份已分析的 Schema，不需要手写传输客户端。

JSON 与 Luxo Binary 都是生产传输。HTTP 客户端通过 SDK transport mode 或 `X-Luxo-Mode: json|binary` 选择；WebSocket 与原生 RPC 使用各自的 canonical binary framing。`APP_ENV` 不会静默改变 wire 契约。

### Wire 兼容性

`luxo.lock` v2 固定 model/type/event 字段 ID、API ID、参数 ID 及其 wire 类型。`luxo gen` 会在改写 lock 前拒绝破坏性变更；只有确定所有已部署生产者与消费者会同步重新生成时，才使用 `--allow-breaking`。

这里的“兼容”指新服务端仍能接受旧客户端，因此兼容发布应先部署服务端，再发布重新生成的客户端。在这个方向上，新增 model/type 字段或可选 API 参数属于兼容变更。删除或修改字段/参数、增加必填参数、修改 API 返回类型、删除 API、修改事件 payload 都属于破坏性变更。已删除 ID 永久保留，不会复用。


## AI 原生设计

Luxo 不只是代码更短 — 它是 **AI 写后端最可靠的语言**。

| | 传统方案 (Go/TS) | Luxo |
|---|---|---|
| **AI 需要的上下文** | 50+ 文件，10K+ tokens | 1 个 `.luxo` 文件，~500 tokens |
| **AI 输出正确性** | 能编译 ≠ 正确，靠测试 | 编译器检查类型、空安全、穷举 |
| **代码风格一致性** | AI 每次选不同写法 | 一件事一种写法 |
| **理解业务** | AI 要从代码反推 | Schema 就是业务 |

```
"加一个商品收藏功能"

→ AI 生成 15 行 .luxo
→ 编译器校验类型、空安全、必填字段
→ 搞定，API 跑起来了

同样的事用 Go？200+ 行，7 个文件
同样的事用 TypeScript？150+ 行，4 个包，零编译期安全
```

**Luxo + AI = 可靠的后端工程师。** 其他语言给 AI 太多自由度，Luxo 给 AI 刚好够用的约束。

## 开发进度

### Phase 1 — 编译器 ✅
- [x] 词法分析 · 语法分析（Pratt Parser）
- [x] 语义分析（声明/类型/函数体/后分析分层 pass、类型检查、空安全、字段注入、注解校验）
- [x] LSP 服务器（诊断、补全、悬停、跳转定义、引用查找）
- [x] VS Code 扩展（语法高亮、LSP 集成）

### Phase 2 — 代码生成 + 运行时 ✅
- [x] 代码生成（11 类 Schema 驱动模块产物 + embedded/service/gateway 入口）
- [x] 类型安全查询构建器（pgx，零反射 scanner，`$select` → SQL）
- [x] CRUD handler 生成（`@crud` → get/list/create/update/delete）
- [x] DataLoader 运行时（2ms 批量窗口、字段合并、`@soft` 过滤）
- [x] 关联自动解析（`$select` 嵌套字段 → DataLoader → SQL）
- [x] `@hash` bcrypt 自动加盐，`VerifyPassword` 登录验证
- [x] `@hidden` 默认 SELECT 排除（API 响应不含 password）
- [x] `@by` 关联注解 + 自动推断（belongsTo/hasMany/hasOne）
- [x] 迁移引擎（声明式 diff · 改名检测 · 安全警告 · `--dry-run`）
- [x] 并发锁 + 校验和验证 + `CREATE INDEX CONCURRENTLY` 自动拆分
- [x] 认证运行时（JWT 签发/验证/刷新、Luvia `AuthMiddleware`、`Identity(ctx)`）
- [x] 标准库（str · slice · math · datetime · crypto · jsonutil · httputil · convert）
- [x] 项目骨架（`luxo init` + `luxo add` + `luxo gen` + `luxo run`）
- [x] Dialect 接口可插拔（PostgreSQL 已实现；MySQL、SQLite、MongoDB 为规划目标）
- [x] `luxo deploy compose` — Dockerfile + docker-compose.yml 生成

### Phase 3 — 多服务 + Binary ✅
- [x] Binary 协议（varint · svarint · fixed64 · field mask · 列式编码）
- [x] Luvia Gateway — schema 驱动 Binary↔JSON（handler 全程 binary，Luvia 翻译）
- [x] `fn @service` — 函数暴露为 RPC 端点（Luxo 协议）
- [x] `extend` + `Model.load()` — 跨模块 DataLoader + 可见性控制
- [x] 多条件 DataLoader（FK · 复合键 · 静态分析）
- [x] 集群模式 — 模块独立 binary、Gateway 路由、`DEPLOY_MODE` 切换
- [x] `luxo deploy compose` — Dockerfile + docker-compose.yml 生成
- [x] 自动建库迁移（`EnsureDatabase` + `migrate.Up`）
- [x] RPC 默认地址推断（`模块名:9000` Docker DNS）
- [x] Schema introspection（`GET /luvia?$schema`，使用 `X-Introspection-Key` 请求头）
- [x] `luxo.schema.json` 导出（SDK 工具链使用）
- [x] Graceful Shutdown · CORS · XSS 安全头
- [x] 事件系统（emit / on · ChanBus + NATSBus · Luxo binary 编码）
- [x] WebSocket 传输 — JSON/Binary 双模式，并发 dispatch
- [x] `@stream` 订阅 — 类型安全 SDK 方法 + 服务端确认/错误 + 事件/native 数据源 + 字段选择与背压
- [x] 纯 Schema SDK 签名 — 分页、字段选择、nullable 参数与 stream 不依赖 API 命名约定

### Phase 4 — 客户端 SDK ✅
- [x] `@luxo/client` — Transport 接口 + FetchTransport + WxTransport + LuxoError
- [x] `@luxo/vite-plugin` — 编译期字段追踪 + 自动 `$select` 注入
- [x] `@luxo/react` — `useLuxoQuery` hook + `LuxoProvider`
- [x] Dart SDK（`luxo_client` — HttpTransport + WsTransport + Luxo binary 编解码 + build_runner 字段追踪）
- [x] Kotlin SDK（`com.luxo.client` — OkHttp + 协程 + Luxo binary 编解码 + Gradle 插件隔离执行编译器 AST 字段追踪）
- [x] [Swift SDK](https://github.com/light-speak/luxo-swift)（独立 SPM 仓库；`LuxoClient` + URLSession + async/await + Luxo binary codec + SwiftSyntax 字段追踪）

### Phase 5 — 生产 + 生态（进行中）
- [ ] Luxo Studio（正在开发；后端与 React 前端基础已落地）
- [ ] HTTP/3 (QUIC) 支持
- [ ] MCP Server（AI 直接读写 .luxo 项目）
- [ ] luxo-ai（自然语言 → .luxo → 运行中的 API）
- [x] `luxo deploy helm` — Helm Chart 生成

## 贡献

Luxo 处于早期开发阶段，欢迎贡献代码、提出想法和反馈。

## 许可证

Apache-2.0 · Copyright 2026 light-speak
