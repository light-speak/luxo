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

Write a React component. Access `user.name` and `user.email`. At compile time, Luxo traces those field accesses and injects `$select: "name, email"` into the API call. The server only serializes those two fields. The SQL only queries those two columns.

```tsx
// You write:
function Profile({ id }) {
  const { data: user } = useLuxoQuery(
    () => transport.call('getUser', { id }),
    [id],
  )
  return <h1>{user.name}</h1>  // ← Luxo sees this at compile time
}

// What actually runs:
// → API: getUser(id: 1, $select: "name")
// → SQL: SELECT name FROM users WHERE id = 1

// Not SELECT * — just the one column you used.
```

No manual optimization. No over-fetching. No `useCallback` wrapper around field lists.

**Every layer knows exactly what the next layer needs, because the compiler already figured it out.**

## Install

```bash
pnpm add @luxojs/react @luxojs/client
```

## Quick Start

```tsx
import { FetchTransport } from '@luxojs/client'
import { useLuxoQuery } from '@luxojs/react'

const transport = new FetchTransport('http://localhost:4000/luvia', {
  token: localStorage.getItem('token'),
})

function UserList() {
  const { data: users, loading, error, refetch } = useLuxoQuery(
    () => transport.call('listUsers', { page: 1 }),
    [],
    { queryKey: ['users', 1] },
  )

  if (loading) return <p>Loading...</p>
  if (error) return <p>Error: {error.message}</p>

  return (
    <ul>
      {users.map(u => <li key={u.id}>{u.name}</li>)}
      <button onClick={refetch}>Refresh</button>
    </ul>
  )
}
```

## API

### `useLuxoQuery<T>(queryFn, deps, options)`

**Declarative** — auto-executes on mount, re-fetches when `deps` change. Concurrent requests with the same key share one Promise. React StrictMode effect replay never duplicates a request.

```tsx
const { data, loading, error, refetch } = useLuxoQuery<User[]>(
  () => transport.call('listUsers', { page }),
  [page],
  {
    queryKey: ['users', page],
    staleTime: 5_000,
    gcTime: 60_000,
  },
)
```

Without a `queryKey`, the hook still deduplicates its own StrictMode replay. Add a structural key to share in-flight or fresh data across components. Object key order does not affect identity.

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
const { mutate, loading, error, reset } = useLuxoMutation<Project, CreateProject>(
  (params) => client.createProject(params),
  { invalidateQueries: [['projects']] },
)
```

Mutations are never deduplicated or cached. `loading` remains true until every concurrent mutation settles. `invalidateQueries` accepts static keys or a function derived from the mutation result.

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

export default defineConfig({
  plugins: [react(), luxo({ schema: './luxo.schema.json' })],
})
```

Now every `useLuxoQuery` call automatically gets `$select` based on which fields your JSX renders. Zero config, zero manual work.

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
