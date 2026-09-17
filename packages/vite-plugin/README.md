# @luxojs/vite-plugin

> Compile-time field tracking + code generation for Luxo

编译期字段追踪 + 代码生成 — 自动注入 $select，零冗余数据传输

## Install / 安装

```bash
pnpm add @luxojs/client
pnpm add -D @luxojs/vite-plugin typescript@7
```

Requires Node.js `^22.18.0 || >=24.11.0` and TypeScript 7. Field analysis uses
Babel 8's typed AST; the generated consumers are checked with the native TS 7
compiler. Node.js requirements apply to build tools, not to the browser bundle.

要求 Node.js `^22.18.0 || >=24.11.0`、TypeScript 7。构建期使用 Babel 8 AST
追踪字段，生成客户端使用原生 TS 7 编译器验收；Node.js 门槛不等同于浏览器门槛。

## Vite Config / 配置

```ts
import { defineConfig } from 'vite'
import { luxo } from '@luxojs/vite-plugin'

export default defineConfig({
  plugins: [luxo({ schema: './luxo.schema.json' })],
})
```

Export `luxo.schema.json` from the Luxo project first. Both `schema` and `outDir`
(default `src/luxo`) resolve relative to Vite's `root`, not the shell's working
directory. Alternatively configure `endpoint` and `introspectionKey`; the key is
sent only in `X-Introspection-Key`. If both sources are configured, a local file
is an explicit fallback and produces a warning when used. With no usable schema,
the build fails instead of silently skipping generation and field selection.

Both sources decode UTF-8 strictly before parsing JSON or writing generated files.
One document-leading BOM is accepted; U+FEFF/U+FFFD inside values is preserved.
Malformed encoding, invalid JSON, or a root without `models`/`apis` object maps
stops generation; these envelope checks are not a complete Schema semantic validator.
Endpoint query parameters are preserved, and discovery sets `$schema` exactly once.
A failed rebuild clears the active Schema instead of transforming with stale field
selections. File errors include the path and original cause. This work runs only
during generation, not on the application's request path.

先从 Luxo 项目导出 `luxo.schema.json`。`schema` 和 `outDir`（默认 `src/luxo`）
均相对于 Vite 的 `root`。也可配置 `endpoint`、`introspectionKey` 获取 Schema；
密钥只通过 `X-Introspection-Key` 请求头发送。两种来源同时配置时，本地文件是
显式备用来源，启用时会告警；没有可用 Schema 则构建失败，不静默跳过字段选择。

两种来源都先严格解码 UTF-8，再解析 JSON、写生成文件；接受一个文档前导 BOM，
保留值内的 U+FEFF/U+FFFD。坏编码、非法 JSON 或缺少 `models`/`apis` 对象映射
的顶层结构会阻断生成；这个边界检查不等于完整 Schema 语义校验。获取 Schema 时
保留 endpoint 原有查询参数，`$schema` 只设置一次。重建失败后清除活动 Schema，
不再用旧字段选择转换源码；文件错误保留路径与原始原因。以上只发生在生成期，
不进入应用请求热路径。

## Features / 功能

### Standalone SDK generation

For scripts that do not start Vite, use the same Schema loader and generator:

```js
import { generateSDK } from '@luxojs/vite-plugin'

await generateSDK({ schema: 'luxo.schema.json', outDir: 'sdk/src' }, import.meta.dirname)
```

The optional second argument is the project root (default: `process.cwd()`).
The function returns the loaded Schema and writes `types.ts`, `schema.ts`, and
`client.ts`. Errors reject the promise; no Vite server or API service is started.
This only generates the SDK: automatic `$select` injection still requires the
Vite plugin on application source. Other consumers must supply `$select` explicitly.

### Compile-time $select / 编译期字段选择

```ts
// Source / 源代码
const post = await client.getPost({ id: 1n })
console.log(post.title)
console.log(post.user.name)

// Compiled / 编译后（自动注入）
const post = await client.getPost({ id: 1n, $select: 'title,user{name}' })
```

### Nested field tracking / 嵌套字段追踪

```ts
post.comments.forEach(c => {
  c.user.avatar  // → $select: "comments{user{avatar}}"
})
```

### Supported patterns / 支持的模式

- Direct: `user.name`
- Optional chain: `user?.name`
- Nested relations: `post.user.name`
- Lambda params: `arr.forEach(item => item.field)`
- Variable alias: `const author = post.user; author.name`
- Paginated and list results: `page.items.map(user => user.name)`
- Stream callback payloads: `subscribeLiveUsers(params, user => user.name)`
- Destructuring: `const { name, email } = await client.getUser({ id: 1n })`
- Type wrappers: `as`, `satisfies`, non-null assertions and parentheses preserve field tracking
- Source modules: `.ts`, `.tsx`, `.mts`, `.cts`; JSX is parsed only in `.tsx`
- Surrounding syntax: decorators, auto-accessors, const type parameters and resource declarations
- Conservative escape fallback: dynamic or externally consumed results receive an explicit safe projection
- Depth warning: nesting > 5 levels triggers compile-time warning

Unsupported or invalid source syntax reports its filename and stops the build;
it never silently disables analysis. These patterns do not imply whole-program
type analysis: escaping results use the explicit bounded projection described
below. Supply `$select` explicitly when a helper's field usage is not visible.

不支持或非法的源码语法会携带文件名报错并阻止构建。上述支持不等于全程序类型分析；
无法静态追踪的结果使用下述有界显式投影。辅助函数内的字段使用不可见时，请显式提供
`$select`。Luxo `Int`、`Duration` 参数使用 `bigint`，不要先转成 `Number`。

### Code generation / 代码生成

Generates typed client from schema introspection:
- `types.ts` — Interfaces for all models, enums (union types), type declarations
- `schema.ts` — API schema for binary transport
- `client.ts` — Typed methods with async/await

Calls without a manual `$select` expose the model's default projection:
persistent fields are required and unloaded relations remain optional. A manual
selection changes the return type to `Foo<true>`, preserving unselected,
selected-null, and selected-value as distinct states. Automatic selection is
also injected for generated stream subscriptions. If a result escapes static
analysis, the plugin emits a bounded explicit projection instead of relying on
implicit full-field behavior.

Generated Binary scalar and scalar-list responses check decoder errors and
trailing bytes before returning or calling a stream's `onData`. These checks
are emitted in the existing method/callback, without a runtime type-dispatch
layer or an additional decoding closure. Named enum returns remain enum types,
not selectable models. Regenerate clients to receive these fixes; upgrading the
runtime alone cannot replace previously generated method bodies.

生成的 Binary 标量及标量列表返回，在返回结果或调用流的 `onData` 前检查解码
错误和尾随字节。检查直接生成在原方法/回调内，不增加运行时类型分发或解码闭包。
命名枚举仍是枚举类型，不被当成可选字段的模型。需要重新生成客户端；仅更新 SDK
运行时不能替换已有的生成方法。

Generated JSON model/type lists require every element to contain at least one
known Schema field, including paginated `items`, nested lists and stream payloads.
Empty lists and selected nullable fields remain valid; unknown-only records,
non-object elements and sparse arrays are rejected before delivery. Generic
`JSON` values such as `{}` are not subject to model projection rules. Regenerate
clients to receive this validation; public API call signatures are unchanged.

生成的 JSON 模型/type 列表要求每条记录至少含一个 Schema 字段，覆盖分页 items、
嵌套列表和流。空列表、已选择的 null 字段仍合法；仅含未知字段、非对象或稀疏数组
会在交付前拒绝。通用 JSON 中的 `{}` 不受模型投影规则限制。客户端须重新生成，
公开 API 调用方式不变。

## Package verification / 包验证

`pnpm build` uses tsdown to bundle CJS/ESM JavaScript and the TypeScript 7 CLI to emit
declarations from implementation source. The package ships only `dist`, not a
second handwritten public declaration. `pnpm typecheck` checks source, tests
and build configuration with `skipLibCheck: false`. Node declarations are a
development-only dependency and do not enter the plugin's runtime bundle.

`pnpm build` 构建 CJS/ESM，并通过 TypeScript 7 CLI 从实现生成声明；发布包仅含
`dist`，不再维护重复的手写声明。`pnpm typecheck` 严格检查源码、测试与构建配置，
保留 `skipLibCheck: false`。Node 类型声明只用于开发检查，不进入插件运行代码。

`src/go-consumer.test.ts` compiles the shared `.luxo` interoperability fixture
into a real Go service, fetches live introspection and uses TS 7 to compile and
execute generated CJS/ESM clients. Both HTTP/WebSocket and JSON/Binary exercise
full-width Int/Duration, arrays, nullable/structured inputs, columnar results,
partial selection and native streams. Build `packages/client` first, then run
`pnpm exec vitest run src/go-consumer.test.ts` here. It also runs in `pnpm test`;
CI supplies the repository Go version and Node 26. Only one temporary loopback
service runs at a time and its generated files/server are cleaned afterward.
Pagination, multi-service routing, browsers and performance are separate gates.

`src/go-consumer.test.ts` 从共用 `.luxo` fixture 编译真实 Go 服务，读取在线
introspection，经 TS 7 编译并执行生成的 CJS/ESM 客户端。HTTP/WebSocket 与
JSON/Binary 均覆盖全宽 Int/Duration、数组、nullable/结构化输入、列式、部分选择
和 native 流。先构建 `packages/client`，再在本包运行上述命令；`pnpm test` 也会
执行，CI 配置仓库要求的 Go 和 Node 26。每次只启动一个临时回环服务，结束清理
服务及生成文件。分页、多服务、浏览器和性能仍需独立验收。

`src/int64-consumer.test.ts` additionally compiles and runs eleven scalar kinds,
single/list returns and stream callbacks as independent CJS/ESM consumers. The
malformed-payload cases use a controlled transport to verify truncation, invalid
UTF-8/Boolean/varint values and trailing bytes never reach user callbacks. They
do not substitute for live Go transport or device tests. Run the isolated native
completion-check benchmark with `LUXO_BENCH_SCALAR_COMPLETION=1 pnpm exec vitest run
src/int64-consumer.test.ts --disableConsoleIntercept`; it compares generated
callbacks with/without completion checks, not full network requests or the
separate cost of strict UTF-8 decoding.

该消费者测试另覆盖 11 类标量的单值、列表和流回调，实际通过 TS 7 编译并执行
CJS/ESM。坏消息用可控 transport 注入，验证截断、非法 UTF-8/Boolean/varint 和
尾随字节不会进入业务回调，不冒充真实 Go 或设备验收。上述原生基准仅对照生成
回调有无完成检查，不包含网络，也不抵扣严格 UTF-8 解码自身的成本。
