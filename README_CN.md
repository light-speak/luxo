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
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.27.1+-00ADD8?style=flat&logo=go" alt="Go Version" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="README.md">English</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#语言特性">语言特性</a> ·
  <a href="#开发进度">开发进度</a>
</p>

---

1.0.0-beta.1 的 Go 核心与 CLI 仅支持 64 位平台；32 位 Go 目标不受支持，也不纳入发行验收。这不改变协议布局或 Int64/UInt64 数值范围。

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

DateTime 在 JSON 和 Binary 中都保留纳秒。JSON 要求带时区的 RFC3339、最多9位小数及 UTC 年份0000～9999；Binary 写入向下取整的 Unix 秒与非负纳秒。超精度或越界直接报错。此发布前变更要求服务与 SDK 配套重新生成，不降级为旧整秒编码；数据库存储精度和性能单独验收。当前内部 RPC 为版本4，另加入显式可空根响应；对应四 SDK 迁移尚未完成，不代表已冻结。

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

先在项目的 `luxo.cache.json` 中显式声明缓存行为：

```json
{"version":1,"policies":{"userPreview":{"mode":"ttl","ttl":"1m","scope":"private"}}}
```

Beta 支持显式 TTL 响应缓存，不提供自动写入失效。TTL 从缓存填充后计时，
不保证底层数据库快照的年龄；必须读取最新数据的决策查询不启用缓存。
结果按已验证身份隔离，启动时必须显式配置后端、部署命名空间和有界回源。
`invalidation` 模式直接报错，不会静默转换为 TTL。

```luxo
// 1. 零代码 — 框架自动生成
api getUser(id: Int): User @cache(policy: "userPreview")

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
type OrderSnapshot { id: Int }
event OrderCreated(order: OrderSnapshot, userId: Int) // 类型化事件声明

api placeOrder(id: Int): Order @auth {
  val order = Order.create(userId: my.id)
  emit OrderCreated(order: OrderSnapshot { id: order.id }, userId: my.id) // 类型化 emit
  order
}

on OrderCreated { order ->                       // 事件监听
  "order created".i                              // .i = info 日志
}
```

事件载荷使用显式 `type` 快照，不能引用数据库 `model` 或 `extend`，嵌套字段和列表
也遵守此规则。发布者明确选择事件字段，数据库模型新增字段不会自动扩大事件内容。
每条对象路径最多嵌套64层`type`：第一个type为第1层，事件封装和列表容器不计层数。
内存发布、网络编码与解码统一拒绝超深对象及native循环引用，返回
`event.ErrSnapshotDepth`，不截断内容。发布端与消费端须一起重新生成，合法wire字节不变。

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

事件载荷使用显式快照。鉴权示例使用应用中已声明的`User`认证主体。

```luxo
type DanmakuSnapshot { roomId: Int text: String }
type NotificationSnapshot { userId: Int text: String }
type ScoreEvent { matchId: Int score: Int }
event DanmakuSent(roomId: Int, danmaku: DanmakuSnapshot)
event NotificationCreated(userId: Int, notification: NotificationSnapshot)

// 事件驱动 + 过滤器
api watchDanmaku(roomId: Int): DanmakuSnapshot @stream(DanmakuSent) {
  it.roomId == roomId
}

// 身份过滤
api watchNotifications: NotificationSnapshot @stream(NotificationCreated) @auth {
  it.userId == my.id
}

// Go 完全控制
api watchLiveScore(matchId: Int): ScoreEvent @stream @native
```

## 快速开始

```bash
go install github.com/light-speak/luxo/cmd/luxo@v1.0.0-beta.1

luxo init my-app
cd my-app
luxo add user
cp .env.example .env
luxo gen
luxo run
```

上述 beta.1 坐标须等对应产物发布后使用。CLI 与适配器是同仓库的独立 module，
应与核心固定为同一候选版本。可选后端在 `luxo gen` 前明确选择，切换后须重新生成和构建。
例如使用 NATS 时，添加 `github.com/light-speak/luxo/pkg/lux/nats@v1.0.0-beta.1` 并设置
`LUXO_EVENT_BACKEND=nats`；运行时地址不决定选用哪个适配器。

## 架构

> [查看完整架构图](assets/architecture.svg)

**Luvia 始终在线。** embedded 模式把所有模块和网关放在同一进程；cluster 模式使用生成的模块服务二进制和独立网关。RPC 路由与 Federation loader 都来自同一份已分析的 Schema，不需要手写传输客户端。

JSON 与 Luxo Binary 都是生产传输。HTTP 客户端通过 SDK transport mode 或 `X-Luxo-Mode: json|binary` 选择；WebSocket 支持 JSON 文本帧与 canonical binary 帧，原生 RPC 使用自己的二进制帧。`APP_ENV` 不会静默改变 wire 契约。

两种编码保持相同的数据语义。业务 `Int/Duration` 为有符号 int64；WebSocket 调用编号（JSON `$id`、Binary `sequence`）为无符号 uint64。JSON ID 必须是 `0…18446744073709551615` 范围内不带引号的十进制整数 token，不能用布尔值、字符串、小数或指数。非法、缺失或溢出的 ID 在业务执行前直接丢弃，不能强转为零；成功与失败响应都原样返回合法 ID。SDK 计数器耗尽时须明确失败，不得舍入、窄化或覆盖待响应请求；四 SDK 的完整边界验收仍是 beta 发布门禁。

### 网关 HTTP 实现

网关默认使用 `APP_HTTP_TRANSPORT=luxo`：TLS ALPN 和明文 HTTP/2 prior-knowledge
连接进入 Luxo 专用 HTTP/2 服务端，同一监听端口的 HTTP/1.1 与 WebSocket 继续使用
Go 标准 HTTP 路径。显式设置 `APP_HTTP_TRANSPORT=standard` 可让 HTTP/2 也使用
标准实现；未知配置值在启动时拒绝。此启动配置选择服务端实现，不选择 JSON/Binary
编码，也不会替客户端改变 HTTP 版本。

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

### Studio 注册传输安全

配置 `LUXO_STUDIO_URL`、`LUXO_API_KEY` 和公开项目 UUID `LUXO_PROJECT_ID` 后，运行时向 Studio 注册并定期上报心跳。远端 URL 默认要求 HTTPS；`localhost` 和回环 IP 允许 HTTP，供本地开发使用。仅在明确可信的内网链路上，才设置 `LUXO_STUDIO_ALLOW_INSECURE_HTTP=true` 显式允许远端 HTTP；这不会为链路提供加密。URL 不允许内嵌凭据、查询参数或 fragment。

注册、心跳、注销及指标/追踪上报都不跟随 HTTP 重定向，避免重放包含凭据的请求体。网关关闭时取消进行中的注册、心跳及依赖探测，等待注册任务退出后，再以独立的 2 秒超时尽力注销；这些操作不进入业务请求热路径。

### 发布与稳定性规范

Swift 跨仓库 CI 已固定候选提交 `c0fa9cc3334045ce488bd5d97d1a3a8c82ff8071`，并检查其 `VERSION` 与核心一致，不再跟随浮动 `main`。本地固定提交联测不替代推送后的远端 checkout、完整发行门禁或正式发布；每次候选升级都必须重新记录受测提交和兼容矩阵。

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

SDK 便利能力可以后续补充，但协议正确性不能延后：`Int`、`Duration` 必须保留完整有符号 64 位范围，拒绝溢出或截断的二进制值，并验证 JSON/Binary、nullable、数组、列式和流式行为一致。使用共用的精确线格式 fixtures，不能只测同一个 SDK 的编码解码往返。Dart VM 与 JavaScript 目标分别验收，原生测试通过不能证明 Web 精度正确；SDK 尚未固定版本也不能成为推迟这些发布阻断项的理由。

首轮候选统一使用 [`VERSION`](VERSION) 中的 `1.0.0-beta.1`：核心、四 SDK、React/Vite 配套包与 Kotlin 插件共享发行编号。TS 配套包精确依赖同版 client；Swift 消费示例使用精确版本。版本一致性测试防止旧包号混入，但相同版本号不代替协议联测或受测提交 SHA。当前只是准备候选元数据，未据此宣称已经发布或完成冻结。

Go 适配器和 CLI 仍在同一仓库，但各自是独立 module。首轮候选的以下标签必须指向同一个已验收的仓库提交；仅创建根标签不会发布子模块：

| 模块目录 | 候选标签 |
| --- | --- |
| 仓库根目录 | `v1.0.0-beta.1` |
| `cmd/luxo` | `cmd/luxo/v1.0.0-beta.1` |
| `pkg/lux/pg` | `pkg/lux/pg/v1.0.0-beta.1` |
| `pkg/lux/cache/redis` | `pkg/lux/cache/redis/v1.0.0-beta.1` |
| `pkg/lux/nats` | `pkg/lux/nats/v1.0.0-beta.1` |
| `pkg/lux/storage/s3` | `pkg/lux/storage/s3/v1.0.0-beta.1` |

此表定义发行坐标，不代表已经发布。通过门禁并取得授权后才能发布；发布后还须在外部消费者中关闭 go.work、移除本地 replace，验证真实版本可解析，不能用工作区构建代替发行依赖验收。应用只依赖已选择的适配器。所有 Luxo 模块依赖，包括 CLI 对 PG 的依赖，首轮必须与候选版本一致。

Studio 发布时必须将核心 Go module、CLI、前端 SDK 和 CI checkout 固定到记录的版本/提交，不跟随浮动 `main` 或 `latest`；本地源码替换仅用于开发。Studio 保留独立版本；后续 SDK 是否独立发版仍须维护兼容矩阵，不凭版本数字猜兼容。当前已实现数据库后端为 PostgreSQL，未来数据库后端和 Studio 新功能不纳入本次冻结。

#### Beta 前的内部 RPC 升级（验收中）

`fn @service`、自动批量加载、关系解析与命名跨服务加载通过
`Router.HandleInternal`注册。HTTP/WebSocket只查询公开路由表，RPC在启动时
导出公开API和内部函数；Schema携带`internal`元数据，启动时拒绝注册作用域
冲突。`svc:`前缀不是授权规则，服务和网关需要配套重新生成。Studio注册、心跳、
注销和遥测改为以项目API Key认证的显式控制API，不保留旧svc别名。四SDK生成器
已按显式元数据过滤内部接口；Studio保留内部Schema文档，但调试台及恢复请求
不能执行内部项。完整部署联测和性能尚在验收中，
不代表已冻结，也不替代RPC对等方认证。

尚未发布的内部协议不保留旧网关兼容或自动降级。网关和服务按同一已验证核心提交重新生成、构建、配套部署；公开 SDK 的 HTTP/WebSocket JSON/Binary 格式不因此改变。

每条新 TCP 连接用固定 preface 双向核验 `rpc.ProtocolVersion`，不匹配即拒绝业务请求；复用连接不重复握手。新建、重连和新建流连接增加一次网络 RTT，性能验收单独进行。请求统一使用 kind/flags/token/可选 trace/body，删除旧 trace 请求版本兼容分支。版本核验不替代身份认证或 Schema/lock ID 一致性检查。

内部关系 resolver 现在要求 `keys`（字段 1）和 `shape`（字段 2：bit 0 为列表，bit 1 为 nullable）。每个响应分组已经是包含 presence、长度及对象/列式列表内容的标准字段值，由目标服务生成；网关校验后直接拼接，不解码模型再重编码。必填对象缺失或单值关系多条记录均报错，空列表与选中的 null 保持不同。网关和服务必须一起重新生成。

新格式的嵌套回归已通过。Unary RPC 错误现在保留原始 JSON data、差异插值文本与 internal 标志，不将数字转换成浮点数；生成的 TS、Dart、Kotlin、Swift 消费者覆盖经网关的 HTTP/WebSocket × JSON/Binary 错误数据。Native 流正常完成及结构化错误终止也已有四 SDK 直连/网关消费者回归；完整发行、设备及剩余性能门禁仍未关闭，不代表 Beta 冻结。

#### Beta 前的错误数据接口迁移

开发主线的 Go `errors.ErrorData` 使用静态的 `AppendJSON(dst []byte) ([]byte, error)` 与 `AppendText(dst []byte, field string) ([]byte, bool)`，替代 `I18nData()/MapData` 的动态映射和反射格式化。`.luxo` 错误声明及构造参数不变，重新运行 `luxo gen`；手写 native 错误需要实现新接口。`ResourceError.ID` 使用 `errors.IntID(value)`、`errors.StringID(value)` 或 `errors.UUIDID(value)`，避免动态 ID 类型。

AppendJSON 成功必须返回合法 JSON，失败保留原始 dst 前缀并返回 error；可复用 `errors.DataWriter` 静态写入。AppendText 只提供请求字段的文本，缺失或失败时返回原始 dst 和 false；直接调用 i18n 时也可使用仅含字符串的 `i18n.Values`。Int/Duration 保留完整有符号 64 位整数，通用 JSON 保留数字原文，不经过浮点转换。内部错误不编码或插值 data，公开消息固定为 `error.internal`；开发模式才返回 cause。

这是冻结前的 Go 源码接口变更，不改变公开 JSON/Binary 的数值契约或二进制布局。Unary RPC 错误 data 已有上面的集成回归，不代表所有 SDK 路径与性能门禁全部通过。Float 写入错误传播已单独获得批准并实现，性能验收仍独立进行。

#### Beta 前的静态请求参数接口迁移

`Request.ParamJSON`、`ParamJSONOptional`、`ParamJSONNullable`、`ParamJSONOptionalNullable` 已删除。服务重新运行 `luxo gen`，普通 `.luxo` 与 `@native` 业务签名、合法 wire 布局不变。手写 Request 处理器改用 `ParamInt`、`ParamUUID`、`ParamDuration` 等具体类型 getter 及对应数组入口。通用 JSON 使用 `ParamRawJSON` / `ParamRawJSONArray` 保留数字原文，不再接受任意 Go 目标。本地结构化 JSON 参数由生成处理器通过 `ParamInput` 和 `ReadJSONInput` / `ReadJSONInput<Type>List` 直接读取；Binary 参数使用 `ParamMessage` / `ParamMessageArray`、生成的 `ReadLuxoInput` 和 `Decoder.Finish()`。

生成处理器区分缺省、显式 null 和具体值：仅缺省采用默认值，显式 null 清空可空默认值，非空参数收到 null 则报错。手写处理器需用 `HasParam` / `ParamIsNull` 保持同一语义。Binary Bytes/JSON 在同步处理器内借用请求存储，异步保留前必须复制。历史反射读取器仅存在于测试对照，正常构建不会加载。

`api.EncodeBinaryRequest` 的 JSON 参数也必须显式使用 `json.RawMessage` 文档。列表支持 `[]json.RawMessage`、原始 JSON 数组文档或仅含原始文档的 `[]any`；拒绝 map、struct、自定义 `MarshalJSON` 对象及标量的隐式转换。例如传入 ``json.RawMessage(`{"count":123456789012345678901234567890}`)``，而不是 Go map。合法文档及数字文本保持原始字节，非法输入返回 error 和 nil 请求正文。这是 Go 编码 API 的约束，不改变 wire 布局。

本地结构化 JSON 输入不再经过“JSON 转 Binary 再读模型”的中间往返。网关转发仍需校验参数并编码内部 Binary RPC 消息，这是网络边界编码，与本地模型读取不同。本项移除了任意目标的反射回退，不代表所有动态边界都已静态化，也不代表零编码、零分配或整体性能验收完成。

#### 请求日志采样

请求日志默认关闭；`LOG_REQUESTS=true` 开启后，成功请求默认每 100 条记录 1 条。开发或排障时再设置 `LOG_REQUESTS_FULL=true` 显式选择全量；单独设置它不会开启日志。错误绕过采样，但有界异步队列仍可丢弃并计数，不是可靠审计通道。指标和 Trace 采样独立。

显式 Go 配置中，`RouterOptions.RequestLogSampleEvery` 为 0 时使用默认 100，1 表示全量，其他 N 表示每 N 条成功请求记录 1 条。需要环境配置时先调用 `api.RouterOptionsFromEnv()`，再覆盖选项。生成的缓存与非缓存服务遵循同一环境策略。

#### NATS 任务重试与死信

`queue.NATSQueue` 使用 JetStream，不等同于非持久化的 Core NATS 事件广播。
`MaxRetries` 限制业务处理的投递次数；耗尽后只尝试转存到 `<queue>.dlq`，
不会因为转存失败或 worker 重启而重新执行业务 handler。转存的创建/发布失败
不 ACK 原消息，按 `RetryDelay` 指数退避、最多间隔一分钟继续尝试；只有
收到 JetStream 发布确认后才确认原消息。延迟 NAK 由服务端调度，不为每个
等待任务启动 goroutine。

这是至少一次转存，不是恰好一次：发布确认或原消息 ACK 丢失时，死信可能
重复，消费方仍需按业务任务标识幂等处理。队列流采用 `DiscardNew`，容量
限制生效时拒绝新写入，不通过删除未确认消息腾空间；存储容量和积压必须
由部署方监控。不得随意删除 durable consumer 或重置其投递状态来“恢复”任务。

#### 有限 Float 写入接口迁移（验收中）

`codec.LuxoMarshaler.MarshalLuxo` 现在返回 `([]byte, error)`；事件重新运行 `luxo gen`，手写实现同步适配。Encoder 的 Float 单值、字段、数组写入和 ColumnarWriter 的 Float 列写入返回 `error`，拒绝 NaN/Infinity，失败不提交部分字段或列。`rpc.EncodeParams` 同样返回 `([]byte, error)`，不支持的值不再静默丢弃。NATS 编码失败时不发布消息，已有 `Emit` 的 error 返回不变。

生成模型/type 的 `WriteLuxo` 和全部 `WriteColumnar` 变体现在返回 `error`；`codec.AppendFixed64` 返回 `([]byte,error)`，`ResponseBuf.AppendFloat` 返回 `error`。嵌套事件、编译/native handler 和内部 RPC 均传递写入失败；直接使用 ResponseBuf 的调用方遇错必须丢弃本次输出。

`codec.ColumnarWriter.Bytes()` 现在返回 `([]byte, error)`，在修改输出缓冲前拒绝非空零列、非法行数/arena 大小、非正字段 ID 和列值数量不一致；失败返回 nil 字节，调用方必须检查错误。空列表仍为 `00 00 00`，选中全 null 列仍合法。列长度不一致或字段 ID 非法后需 `Reset()`；生成的直写代码已有 error 返回，不使用此缓冲辅助类型。本项接口迁移不等于未测负载的性能已通过。

流编码回调与 `StreamPayloadJSON` 返回 `([]byte,error)`，`TypedStream.Send` 和 `Dispatch*` 报告失败，不发送错误载荷。Schema 的五种 Binary→JSON 转换也返回 `([]byte,error)`，HTTP/WS/CLI 接住错误，不用整数、null 或默认分页掩盖坏响应。重新运行 `luxo gen`，并适配手写编码回调；普通 `.luxo` 和 native 业务方法的返回类型不变。

Native 流 handler 已接收生成的强类型参数快照并返回 `error`，在订阅 ACK 前完成鉴权、默认值和参数校验。正常返回先排空已接收消息再通知完成；业务错误携带结构化 data 终止当前订阅。网关传播 RPC 终止结果及载荷转换错误，不再依靠 void handler 静默结束。每次订阅使用独立 UInt64 编号；SDK 在断线时终止订阅，由调用方决定重新订阅，不承诺补发或连续交付。上述已实现契约不替代最终候选、设备及性能验收。

合法 wire、负零及有限次正规数保持不变，只检查实际写出的字段，没有反射或新增序列化往返。跨 SDK 重验、整链性能验收及冻结仍未完成。异步订阅者失败不能追溯到已完成的发布调用，也不意味着撤回先前有效消息或回滚数据库写入。

#### 积极采用最新稳定工具链

开发主线积极采用最新稳定工具链和有实际价值的稳定语言特性，不为兼容旧编译器本身而保留旧实现。正式 SDK 不依赖实验开关、nightly 编译器或预览 API。工具链升级不改变 wire 版本、字段 ID、整数精度和字段选择语义。

当前迁移目标为 Go 1.27.1+、TypeScript 7、Dart 3.13+、Kotlin 2.4.20；Swift 通过独立验收的 SDK 改动跟进最新稳定编译器。编译器版本与设备/运行时最低版本分开决策：升级 Kotlin、Swift 编译器，不等于必须淘汰旧 Android、iOS 设备。

JavaScript 开发与 SDK 联测的 Node 精确版本由 [`.node-version`](.node-version) 统一指定；TS 和 Dart Web CI 共用此文件，不使用浮动主版本。安装依赖前通过 `sh scripts/check-node.sh` 核验实际 Node 版本；Studio 的联动构建同样调用核心脚本。旧版本、预览版本、无效版本文件或工具执行失败都阻断，不自动下载或安装。TS CI 的 pnpm 同样固定精确版本，并输出实际 Node、pnpm、TS 编译器版本。三个 TS 包的 Node 类型声明仅用于构建/测试，锁文件固定实际解析版本；不进入浏览器运行代码。client/React 继续输出 ES2022，Vite 插件的最低 Node 要求独立声明，不能把开发工具升级等同于浏览器或插件运行时兼容性验收。

Dart CI 在[工作流](.github/workflows/test.yml)中分别锁定最低支持版本 3.13.0 和当前选定的稳定版本 3.13.4，共用同一套 analyze、VM、JavaScript/Wasm 与生成消费者检查。启动时核对实际编译器版本，版本漂移、预览渠道或工具执行失败都阻断，不把浮动 `stable` 当作可复现的验收基线。升级这个精确版本不提高 SDK 的最低要求，也不自动放行编译器变化带来的性能成本。

Go 生成器与生成项目一起升级：数据库查询入口采用 `DB.QueryRows[T]`、`DB.QueryRow[T]`、`DB.QueryScalar[T]` 泛型方法，并保留已有包级调用兼容性，共用唯一实现。现有 `Query[T]`、创建/更新基类和 DataLoader 已复用泛型算法；模型 scanner、字段选择与编解码仍按 Schema 专门生成。`NativeResolver` 保留具体签名，不能改成声明独立类型参数的接口方法。

Go 分析工具本身也必须用理解项目语法的编译器构建。升级 Go 后，在本仓库根目录运行 `go install github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0` 重编固定版本，再用 `go version -m "$(command -v gocyclo)"` 查看构建工具链。仓库内的提交钩子、`bash scripts/lint.sh` 和 CI 共用 [check-go.sh](scripts/check-go.sh)，检查 `pkg/`、`cmd/`、`scripts/`、`internal/`；工具缺失、解析失败、异常诊断、未格式化和复杂度超过 25 都会阻断，保留错误详情。本地不自动安装、不跳过；若已复制旧版提交钩子，需要重新安装仓库中的钩子。

独立克隆本仓库后，先执行一次 `sh scripts/setup-modules.sh .`，建立候选模块的本地工作区；已有完整父工作区时无需重复创建。此脚本拒绝覆盖现有 `go.work`，不修改发行模块清单。`go test ./pkg/...` 不会进入嵌套模块，因此本地 lint 与 CI 会另外检查 CLI、PG、Redis、NATS 和 S3；适配器集成测试仍需对应服务配置。

macOS使用Colima时，testcontainers清理容器需要虚拟机内的Docker socket路径，而不是宿主机的路径。
先用`colima ssh -- test -S /var/run/docker.sock`核验；成功后可在测试命令前设置
`TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock`。不要通过禁用清理容器绕过挂载错误。

本地按 CI 方式测试数据库时，将 `LUXO_TEST_DATABASE_URL` 指向专用 PostgreSQL 测试库；每个用例创建并清理独立 Schema。未配置时会逐个启动容器，累计启动/清理时间可能超过本地 lint 的 60 秒包级超时。不要指向业务数据库，也不能通过跳过数据库测试获得通过结果。

Dart 字段提示在构建期使用已解析类型，支持 primary constructor 字段、局部别名、嵌套关联、列表/分页和内联回调；跨文件及 `part` 合并时隔离变量与客户端库。`Selected.value` 等 SDK 包装不会成为协议字段，显式 `select` 始终优先。默认生成目录与静态推断边界见 [Dart SDK](packages/dart/README.md#compile-time-field-tracking)；不会在请求热路径分析源码。

升级覆盖生成器、生成消费者、构建/声明工具、理解语言语法的字段选择分析、CI 和文档，不只是修改 manifest。不支持的源码语法必须明确诊断，不能静默扩大字段选择。新特性应改善正确性、性能或可维护性；代码更短不代表运行更快。反射和热路径通用序列化仍然禁止。

构建锁定解析后的精确工具版本，发布证据记录这些版本。最低支持版本与当前稳定版本不同则分别验证，预览版放入独立定期兼容检查。基线与候选代码必须在相同工具链比较；编译器升级另用同一份代码做前后对比，不能靠重置基线隐藏回退。已发布产物保留原有环境要求；提高已发布包的最低编译器版本必须明确评审兼容性、选择版本号并提供迁移说明，不能在维护补丁中悄悄提高。

参考：[Go 1.27](https://go.dev/doc/go1.27)、[Dart 3.13](https://dart.dev/blog/announcing-dart-3-13)、[TypeScript 7](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/)。

#### 性能阻断与已批准的功能成本

性能审查区分请求热路径回退与必要的编译期功能成本。“每次编译首次使用时初始化”不等于“每次请求分配”，也不是整个进程只初始化一次。新增功能仍需测试和 benchmark，不因功能必要就自动获得豁免。

默认 CI 规则不变：每个版本至少 10 次采样，显著耗时增长超过 5% 或显著分配增长进入二次采样，两组确认后阻断。5% 是自动检测阈值，不是允许人为消耗的性能预算；反射、请求热路径通用序列化和已确认的热路径缺陷仍须人工阻止。

必要功能的已批准成本以 [approved-costs.json](scripts/benchgate/approved-costs.json) 为唯一机器可读清单，不在此复制样例预算。每项必须记录原因、精确基线 SHA、包、benchmark，以及获批的绝对耗时或分配增量上限；不允许通配符。批准内存不等于批准耗时，批准冷路径不等于批准请求路径。

仅当两组比较项目一致、两组结果都满足对应批准上限时，CI 才应用并打印批准记录。未经批准的回退、超预算或清单外样例仍按默认门禁处理。基线 SHA 改变后原批准不再匹配，不能滚动累加预算。新增或扩大批准必须由维护者明确确认，工具不得自动放行；局部样例不能替代完整请求验收。

当基线为零、候选为正数且差异显著时，benchstat 将相对变化显示为 `?`。门禁依据合法的绝对测量值识别回退并要求第二组确认，不将其忽略、当作零增长或自动放行；非零基线的 `?`、非法数值和不足的采样仍拒绝。

CI 保留原始 benchmark 样本和 benchstat 对比结果 14 天，失败运行也保留。门禁日志输出精确的基线值、候选值和绝对增量，不再只显示舍入后的百分比。队列集成测试要求 JetStream 已启用并健康，而不只是 NATS 端口可连接。Lexer、Parser、Semantic 各执行 200 万次 Fuzz，使用 4 个 worker 和 180 秒硬超时；失败或超时仍然阻断 CI。Fuzz 日志、工具链信息及已保存的失败输入保留 14 天。


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
