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

### 编译函数与 Native 边界

普通函数会编译成模块内可直接调用的 Go 方法。它既能调用其他编译函数，也能通过 `@native` 进入 Go 实现；两条路径都不使用反射或运行时名称查找。

```luxo
fn normalizeScore(score: Int, ceiling: Int = 100): Int {
  when(score) {
    in 0..ceiling -> score
    else -> ceiling
  }
}

fn verifyRelease(projectId: Int): Result<Boolean> @native
fn recordRelease(projectId: Int) @native

api releaseReady(projectId: Int, score: Int): Boolean {
  val normalized = normalizeScore(ceiling: 100, score: score)
  recordRelease(projectId)             // Go error 自动传播
  verifyRelease(projectId)? && normalized >= 80
}
```

命名参数在编译期按声明匹配，默认值必须是类型匹配的编译期常量；未知参数、重复参数、缺少必填参数和类型不匹配都会在语义分析阶段阻止生成。
有返回值的 native 函数使用 `Result<T>` 和显式 `?`；无返回值的 native 函数降级为 Go `error`，失败会自动传播。
函数不定参数使用 `...values: T`，必须位于最后且不能声明默认值；普通编译函数与 `@native` 函数生成一致的 Go 可变参数 ABI。公开 API 必须使用 `[T]`，以保持 wire 上命名参数 ID 的唯一与稳定。

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

所有返回结构化数据的公开 API 都必须携带非空 `$select`；缺少字段选择是协议错误，不会
隐式退化为全字段查询。Vite 分析器会为生成客户端的普通请求和流式订阅回调在编译期
自动注入选择；动态或逃逸用法会生成显式的安全投影，直接使用底层 transport 时则必须
自行传入 `$select`。显式选择返回
`Foo<true>`，精确保留未选择、已选择且为 `null`、已选择且有值三种状态。输入 DTO 保持
严格；同一个 Schema 类型同时用于输入和输出时，codegen 分别生成 `Foo` 和 `FooInput`。

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

### 请求追踪与 SQL

Luxo 的追踪是请求级、跨网关、服务、DataLoader 与数据库的执行 DAG。Studio Playground
主动调试会携带项目调试密钥并显式请求详细追踪；PostgreSQL 在服务端记录连接池等待、SQL
操作、资源、耗时、参数数量、影响行数和错误码。SQL 正文会先删除普通及嵌套注释，脱敏
字符串、转义字符串、dollar-quoted 与数字字面量，并限制为 4 KiB；参数值和数据库凭据
永不进入 trace。

生产环境的普通请求没有 tracer session，数据库热路径仅做常数次 context 查询，不读取时钟、
不构造元数据且保持零分配。`LUXO_TRACE_SAMPLE_RATE` 命中的线上样本只保留归一化 SQL
结构指纹和结构元数据，注释、字面量与参数序号不会影响聚合键，也不会构造或保存 SQL 正文。
详细 trace 使用流式、版本化 Luxo Binary response envelope，避免 HTTP header 大小限制，
同时不让网关缓存或复制整份业务响应；最多保留 128 个 span，超出时明确标记截断。

### Wire 兼容性

`luxo.lock` v2 固定 model/type/event 字段 ID、API ID、参数 ID 及其 wire 类型。`luxo gen` 会在改写 lock 前拒绝破坏性变更；只有确定所有已部署生产者与消费者会同步重新生成时，才使用 `--allow-breaking`。

这里的“兼容”指新服务端仍能接受旧客户端，因此兼容发布应先部署服务端，再发布重新生成的客户端。在这个方向上，新增 model/type 字段或可选 API 参数属于兼容变更。删除或修改字段/参数、增加必填参数、修改 API 返回类型、删除 API、修改事件 payload 都属于破坏性变更。已删除 ID 永久保留，不会复用。

### 发布与稳定性规范

下一发布目标为 **`v1.0.0-beta.1`**，不是已经发布的稳定版。Luxo 遵循 [语义化版本规范](https://semver.org/lang/zh-CN/)：Git tag 使用 `v` 前缀，包版本采用各生态要求的格式。

| 阶段 | 版本序列 | 范围 |
| --- | --- | --- |
| Beta | `v1.0.0-beta.1`、`v1.0.0-beta.2`…… | 建立兼容基线，修复正确性、安全和性能缺陷。 |
| 发布候选 | `v1.0.0-rc.1`、`v1.0.0-rc.2`…… | 功能冻结，只修复阻碍发布的问题。 |
| 正式版 | `v1.0.0` | 发布验证完成的公开契约。 |
| 后续维护 | `v1.0.1` / `v1.1.0` / `v2.0.0` | 兼容修复 / 兼容新增 / 不兼容的公开契约变更。 |

Beta 是预发布版，不代表生产稳定性承诺。从 beta.1 起默认保持兼容；确实无法避免的 beta 契约破坏必须经过明确评审，提供迁移说明，同步 SDK 和兼容测试，不能仅凭递增 beta 序号视为安全升级。已经发布的 tag 和产物不可覆盖。

公开契约覆盖 DSL 语义、支持的 CLI/配置、生成代码与 native Go 接口、SDK 行为、显式字段选择、Schema/lock ID 和 wire 类型、JSON/Binary 与 RPC/stream 帧、错误、Studio 注册和遥测。内部实现及追踪耗时数值不属于稳定 API。产品版本、wire envelope 版本和 `luxo.lock` 版本相互独立；产品升级不应修改未变化的编码版本，也不允许复用已删除 ID。

每次发布必须记录核心提交和验证过的 TypeScript、Dart、Kotlin、Swift SDK 版本/提交。门禁包括编译器与运行时测试、race 检查、lint 和可达路径覆盖率审查、相对上一基线的 benchmark 对比、跨 SDK 协议 fixtures、干净外部消费项目构建。保留基线 fixtures 和已生成的消费者；只把客户端重新生成后连接新服务端，不算向后兼容验证。首个正式版发布前，还必须让 Studio 脱离开发者 Go workspace 和未固定的同级源码，独立验证候选版。

Studio 发布时必须将核心 Go module、CLI、前端 SDK 和 CI checkout 固定到记录的版本/提交，不跟随浮动 `main` 或 `latest`；本地源码替换仅用于开发。Studio 和独立发布的 SDK 保留各自版本号，以发布兼容矩阵建立支持关系，不要求版本数字相同。当前已实现数据库后端为 PostgreSQL，未来数据库后端和 Studio 新功能不纳入本次冻结。

#### 性能阻断与已批准的功能成本

性能审查区分请求热路径回退与必要的编译期功能成本。“每次编译首次使用时初始化”不等于“每次请求分配”，也不是整个进程只初始化一次。新增功能仍需测试和 benchmark，不因功能必要就自动获得豁免。

默认 CI 规则不变：每个版本至少 10 次采样，显著耗时增长超过 5% 或显著分配增长进入二次采样，两组确认后阻断。5% 是自动检测阈值，不是允许人为消耗的性能预算；反射、请求热路径通用序列化和已确认的热路径缺陷仍须人工阻止。

经明确评审的编译期分配成本记录在 [approved-costs.json](scripts/benchgate/approved-costs.json)。记录必须指定原因、精确基线 SHA、包、benchmark 和绝对 B/op、allocs/op 增量上限；仅 semantic/codegen 可配置，不允许通配符或耗时豁免。审批只能在两组采样一致、且两组的字节数与分配次数均未超预算时使用，CI 明确打印命中记录。超预算、其他 benchmark、运行时包和耗时回退仍按默认规则处理。基线 SHA 更新后记录自动失效，不能把同一个增量滚动累加到后续版本。

本批批准的是 `AnalyzeDemoFile-2` 中 fn/native 严格参数校验的声明索引：相对记录的基线，每次编译最多增加 256 B 和 2 次分配；索引按需创建并复用，不进入生成服务的请求热路径。新增审批或扩大预算必须重新获得维护者确认，不能由工具自动批准。


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
