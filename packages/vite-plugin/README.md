# @luxo/vite-plugin

> Compile-time field tracking + code generation for Luxo

编译期字段追踪 + 代码生成 — 自动注入 $select，零冗余数据传输

## Install / 安装

```bash
pnpm add -D @luxo/vite-plugin
```

## Vite Config / 配置

```ts
import { defineConfig } from 'vite'
import { luxo } from '@luxo/vite-plugin'

export default defineConfig({
  plugins: [luxo()],
})
```

## Features / 功能

### Compile-time $select / 编译期字段选择

```ts
// Source / 源代码
const post = await client.getPost({ id: 1 })
console.log(post.title)
console.log(post.user.name)

// Compiled / 编译后（自动注入）
const post = await client.getPost({ id: 1, $select: 'title,user{name}' })
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
- Destructuring: `const { name, email } = await client.getUser({ id: 1 })`
- Conservative escape fallback: dynamic or externally consumed results receive an explicit safe projection
- Depth warning: nesting > 5 levels triggers compile-time warning

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
