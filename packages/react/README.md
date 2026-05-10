# @luxo/react

> React hooks for Luxo API — useQuery, useMutation, Provider

React Hooks 集成 — useQuery 查询、useMutation 写操作、Provider 上下文

## Install / 安装

```bash
pnpm add @luxo/react @luxo/client
```

## Usage / 使用

```tsx
import { LuxoProvider } from '@luxo/react'
import { FetchTransport } from '@luxo/client'

const transport = new FetchTransport('http://localhost:4000/luvia')

function App() {
  return (
    <LuxoProvider transport={transport}>
      <UserList />
    </LuxoProvider>
  )
}
```

### useQuery

```tsx
import { useLuxoQuery } from '@luxo/react'

function UserList() {
  const { data: users, loading, error, refetch } = useLuxoQuery(
    () => client.listUsers(),
    []
  )
  if (loading) return <div>Loading...</div>
  return <ul>{users?.map(u => <li key={u.id}>{u.name}</li>)}</ul>
}
```

### useMutation

```tsx
import { useLuxoMutation } from '@luxo/react'

function LoginForm() {
  const { mutate: login, loading, error } = useLuxoMutation(
    (params: { email: string; password: string }) => client.login(params)
  )

  const handleSubmit = async () => {
    const { token } = await login({ email, password })
    transport.setToken(token)
  }
}
```
