<p align="center">
  <img src="https://raw.githubusercontent.com/light-speak/luxo/main/assets/logo.svg" alt="Luxo" width="200" />
</p>

<h3 align="center">@luxojs/react</h3>

<p align="center">
  React hooks for Luxo — declarative data fetching with compile-time field tracking.
</p>

<p align="center">
  <a href="https://www.npmjs.com/package/@luxojs/react"><img src="https://img.shields.io/npm/v/@luxojs/react.svg" alt="npm" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License" /></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#api">API</a> ·
  <a href="https://github.com/light-speak/luxo/blob/main/README_CN.md">中文文档</a>
</p>

---

## Why Luxo + React?

The path from database to screen should be short. With Luxo, it is.

Use the generated, strongly typed client inside React hooks. Luxo carries explicit field selections through the transport to server-side projection. The server may also load primary keys, relationship keys and fields required by business logic; those internal requirements are not extra public response fields.

```tsx
import { useLuxoQuery } from '@luxojs/react'
import { LuxoClient } from './luxo/client.js'

const client = LuxoClient.create('http://localhost:4000/luvia')

export function Profile({ id }: { id: bigint }) {
  const { data: user, loading, error } = useLuxoQuery(
    ({ signal }) => client.getUser({ id, $select: 'id, name' }, { signal }),
    [id],
    { queryKey: ['user', id, 'id, name'] },
  )
  if (loading) return <p>Loading...</p>
  if (error) return <p>{error.message}</p>
  return <h1>{user?.name ?? 'No user'}</h1>
}
```

The Vite plugin infers selections for supported, locally analyzable generated-client calls. It does not perform whole-program data-flow analysis through arbitrary hooks, callbacks or JSX. At those boundaries, use an explicit `$select` as above. Raw `transport.call(...)` does not provide generated API signatures or model decoding.

## Install

```bash
pnpm add @luxojs/react @luxojs/client
```

Use TypeScript 7. Luxo `Int` and `Duration` values are `bigint`, including IDs and pagination metadata. Do not convert them to `number`. Unselected output fields are `undefined`; selected nullable values remain `null`.

## Quick Start

These examples assume your generated schema exposes `getUser(id: Int): User` and a paginated `listUsers`, with `User.id: Int` and `User.name: String`. The Vite plugin writes the generated client to `src/luxo`; the examples live beside that directory.

```tsx
import { useLuxoQuery } from '@luxojs/react'
import { LuxoClient } from './luxo/client.js'

const client = LuxoClient.create('http://localhost:4000/luvia')

export function UserList() {
  const { data: users, loading, error, refetch } = useLuxoQuery(
    ({ signal }) => client.listUsers({ page: 1n, pageSize: 20n, $select: 'id, name' }, { signal }),
    [],
    { queryKey: ['users', 1n, 20n, 'id, name'] },
  )

  if (loading) return <p>Loading...</p>
  if (error) return <p>Error: {error.message}</p>
  if (!users) return <p>No data</p>

  return (
    <section>
      <p>Total: {users.total.toString()}</p>
      <ul>
        {users.items.map(user => {
          if (user.id === undefined) throw new Error('The response omitted a selected user ID')
          return <li key={user.id.toString()}>{user.name}</li>
        })}
      </ul>
      <button type="button" onClick={() => { void refetch() }}>Refresh</button>
    </section>
  )
}
```

## API

### `useLuxoQuery<T>(queryFn, deps, options)`

**Declarative** — auto-executes on mount, re-fetches when `deps` change. Concurrent requests with the same key share one Promise. React StrictMode effect replay never duplicates a request.

```tsx
const { data, loading, error, refetch } = useLuxoQuery(
  ({ signal }) => client.listUsers({ page, $select: 'id, name' }, { signal }),
  [page],
  {
    queryKey: ['users', page, 'id, name'],
    staleTime: 5_000,
    gcTime: 60_000,
  },
)
```

Here `page` is a `bigint`, and `data` is a paginated result rather than a plain array. Without a `queryKey`, the hook still deduplicates its own StrictMode replay. Add a structural key to share in-flight or fresh data across components. Include inputs and field selection in shared keys so different projections cannot share an incomplete cached response. Object key order does not affect identity; distinct `bigint` IDs stay distinct.

| Option | Default | Description |
|--------|---------|-------------|
| `queryKey` | hook-local | Structural cache and deduplication key |
| `enabled` | `true` | Disable automatic execution while false |
| `staleTime` | `0` | Time in milliseconds that completed data stays fresh |
| `gcTime` | `0` | Time in milliseconds to retain an unobserved query |
| `queryClient` | context client | Override the nearest QueryClient |

| Return | Type | Description |
|--------|------|-------------|
| `data` | `T \| null` | Response data, null while loading |
| `loading` | `boolean` | `true` during fetch |
| `error` | `Error \| null` | Error object if request failed |
| `refetch` | `() => Promise<void>` | Manually trigger re-fetch |

### `useLuxoMutation<T>(mutationFn, options)`

**Imperative** — only runs when you call `mutate()`. For writes: create, update, delete, login.

```tsx
const { mutate, loading, error, reset } = useLuxoMutation(
  (params: { name: string }) => client.createProject({ ...params, $select: 'id, name' }),
  { invalidateQueries: [['projects']] },
)
```

This example assumes `createProject(name: String)` returns a project with `id` and `name`. Mutations are never deduplicated or cached. `loading` remains true until every concurrent mutation settles. `invalidateQueries` accepts static keys or a function derived from the mutation result. The hook names describe React execution behavior; Luxo does not split its API protocol into GraphQL query/mutation operation types.

| Return | Type | Description |
|--------|------|-------------|
| `mutate` | `(params) => Promise<T>` | Execute the mutation |
| `loading` | `boolean` | `true` during mutation |
| `error` | `Error \| null` | Error if mutation failed |
| `reset` | `() => void` | Clear error state |

### `QueryClient`

`QueryClient` owns in-flight requests and optional memory caching. Completed responses are not retained by default.

```tsx
const queryClient = new QueryClient({ staleTime: 0, gcTime: 0 })

queryClient.prefetchQuery({
  queryKey: ['project', id],
  queryFn: ({ signal }) => fetchProject(id, signal),
  staleTime: 10_000,
})

queryClient.invalidateQueries({ queryKey: ['project'] })
queryClient.cancelQueries({ queryKey: ['project', id], exact: true })
queryClient.setQueryData(['project', id], updatedProject)
```

Supported operations include `fetchQuery`, `prefetchQuery`, `getQueryData`, `getQueryState`, `setQueryData`, `invalidateQueries`, `cancelQueries`, `removeQueries`, and `clear`.

### `LuxoProvider` + `useLuxoClient` + `useQueryClient`

Optional context-based transport injection:

```tsx
import { LuxoProvider, QueryClient, useLuxoClient, useQueryClient } from '@luxojs/react'

const queryClient = new QueryClient()

// Root
<LuxoProvider transport={transport} queryClient={queryClient}>
  <App />
</LuxoProvider>

// Any child component
function Dashboard() {
  const client = useLuxoClient()
  const queryClient = useQueryClient()
}
```

Use a separate QueryClient per server-rendered request. Browser applications may rely on the default client when no provider is present.

## Full Example — With Vite Plugin

For compile-time `$select` injection, add `@luxojs/vite-plugin`:

```bash
pnpm add -D @luxojs/vite-plugin
```

```ts
// vite.config.ts
import { luxo } from '@luxojs/vite-plugin'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react(), luxo({ schema: './luxo.schema.json' })],
})
```

The plugin generates the client and injects explicit selections for supported local access patterns. Returning a model through a callback may require a bounded default projection because JSX usage outside that callback cannot be inferred reliably. Use explicit selections in hook callbacks when you want a specific projection; do not rely on implicit server-side all-field selection.

## Package verification

`pnpm build` emits JavaScript with tsdown and generates declarations from implementation source using the TypeScript 7 CLI. The published package contains `dist`, not a separately maintained handwritten declaration file. Tests install actual client/React tarballs, compile strict ESM/CJS consumers and check full-width integer cache keys and values. They reuse already installed third-party React/TypeScript tooling, but never link Luxo source into the consuming project. The profile and quick-start examples are also compiled against a generated client. Build `../client` before building this package from the repository.

`pnpm typecheck` checks production source, tests, benchmarks and tool configuration without `skipLibCheck`. Node and React DOM declarations are development-only dependencies; they are not bundled into runtime JavaScript. Run cache benchmarks separately with `pnpm exec vitest bench --run`. Results from different Vitest/Tinybench versions are not interchangeable performance baselines.

Hook tests explicitly select jsdom with per-file environment directives. Pure cache tests, package consumers and cache benchmarks run in Node without a global DOM environment.

## Ecosystem

| Package | Description |
|---------|-------------|
| [`@luxojs/client`](https://www.npmjs.com/package/@luxojs/client) | Core transport + binary codec |
| **[`@luxojs/react`](https://www.npmjs.com/package/@luxojs/react)** | React hooks |
| [`@luxojs/vite-plugin`](https://www.npmjs.com/package/@luxojs/vite-plugin) | Compile-time `$select` + typed client codegen |
| [`luxo_client`](https://pub.dev/packages/luxo_client) | Dart/Flutter SDK |

## Links

- [Luxo Framework](https://github.com/light-speak/luxo) · [中文文档](https://github.com/light-speak/luxo/blob/main/README_CN.md)
- [Why "Luxo"?](https://github.com/light-speak/luxo#why-luxo) — *Lux (light) + O (origin)*
- [The Language](https://github.com/light-speak/luxo#the-language) — 32 keywords, compiles to Go

## License

Apache-2.0 · Copyright 2026 [light-speak](https://github.com/light-speak)
