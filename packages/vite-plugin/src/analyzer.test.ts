import { describe, it, expect } from 'vitest'
import { analyzeAndTransform } from './analyzer'
import type { LuxoSchema } from '@luxojs/client'

const schema: LuxoSchema = {
  models: {
    User: {
      name: 'User',
      fields: [
        { id: 1, name: 'id', type: 'Int' },
        { id: 2, name: 'name', type: 'String' },
        { id: 3, name: 'email', type: 'String' },
        { id: 4, name: 'phone', type: 'String', nullable: true },
        { id: 5, name: 'score', type: 'Float' },
      ],
    },
    Post: {
      name: 'Post',
      fields: [
        { id: 1, name: 'id', type: 'Int' },
        { id: 2, name: 'title', type: 'String' },
        { id: 3, name: 'content', type: 'String', nullable: true },
        { id: 4, name: 'authorId', type: 'Int' },
        { id: 5, name: 'status', type: 'Enum' },
        { id: 6, name: 'user', type: 'User' },
        { id: 7, name: 'comments', type: 'Comment', list: true },
      ],
    },
    Comment: {
      name: 'Comment',
      fields: [
        { id: 1, name: 'id', type: 'Int' },
        { id: 2, name: 'content', type: 'String' },
        { id: 3, name: 'user', type: 'User' },
      ],
    },
  },
  types: {
    AuthPayload: {
      name: 'AuthPayload',
      fields: [
        { id: 1, name: 'member', type: 'Model', typeName: 'User' },
        { id: 2, name: 'token', type: 'String' },
      ],
    },
    RecursiveNode: {
      name: 'RecursiveNode',
      fields: [
        { id: 1, name: 'value', type: 'String' },
        { id: 2, name: 'child', type: 'Model', typeName: 'RecursiveNode', nullable: true },
      ],
    },
  },
  apis: {
    getUser: {
      id: 1,
      name: 'getUser',
      module: 'user',
      returnType: 'User',
      params: [{ id: 1, name: 'id', type: 'Int' }],
    },
    listUsers: { id: 2, name: 'listUsers', module: 'user', returnType: 'User', returnList: true, paginated: true },
    allUsers: { id: 4, name: 'allUsers', module: 'user', returnType: 'User', returnList: true },
    me: { id: 5, name: 'me', module: 'user', returnType: 'User' },
    login: { id: 6, name: 'login', module: 'user', returnType: 'AuthPayload' },
    getRecursiveNode: { id: 7, name: 'getRecursiveNode', module: 'user', returnType: 'RecursiveNode' },
    liveAlerts: {
      id: 8,
      name: 'liveAlerts',
      module: 'user',
      returnType: 'User',
      stream: true,
      params: [{ id: 1, name: 'projectId', type: 'Int' }],
    },
    getPost: {
      id: 3,
      name: 'getPost',
      module: 'post',
      returnType: 'Post',
      params: [{ id: 1, name: 'id', type: 'Int' }],
    },
  },
}

describe('analyzeAndTransform', () => {
  it('should inject $select for simple field access', () => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.name)
console.log(user.email)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("getUser({ $select: 'name,email', id: 1 })")
  })

  it('should inject $select for optional chain access', () => {
    const code = `
const user = await client.getUser({ id: 1 })
const name = user?.name
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select:")
    expect(result).toContain('name')
  })

  it('should handle destructuring', () => {
    const code = `
const { name, email } = await client.getUser({ id: 1 })
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select: 'name,email'")
  })

  it('should handle destructure with rename', () => {
    const code = `
const { name: userName, email } = await client.getUser({ id: 1 })
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('name')
    expect(result).toContain('email')
  })

  it('should keep an explicit selection when all fields are used', () => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.id, user.name, user.email, user.phone, user.score)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select: 'id,name,email,phone,score'")
  })

  it('should not inject when $select already present', () => {
    const code = `
const user = await client.getUser({ id: 1, $select: 'name' })
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toBeNull()
  })

  it('should use a safe projection when usage is opaque', () => {
    const code = `
const user = await client.getUser({ id: 1 })
doSomething(user)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id,name,email,phone,score', id: 1 })")
  })

  it('should handle multiple API calls independently', () => {
    const code = `
const user = await client.getUser({ id: 1 })
const post = await client.getPost({ id: 2 })
console.log(user.name)
console.log(post.title)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
    expect(result).toContain("getPost({ $select: 'title', id: 2 })")
  })

  it('should handle template literal field access', () => {
    const code = `
const user = await client.getUser({ id: 1 })
const msg = \`Hello \${user.name}\`
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('name')
  })

  it('should track nested relation fields', () => {
    const code = `
const post = await client.getPost({ id: 1 })
console.log(post.title)
console.log(post.user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('title')
    expect(result).toContain('user{name}')
  })

  it('should track forEach lambda params', () => {
    const code = `
const post = await client.getPost({ id: 1 })
console.log(post.title)
post.comments.forEach(c => {
  console.log(c.content)
  console.log(c.user.name)
})
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('title')
    expect(result).toContain('comments{content,user{name}}')
  })

  it('should track variable alias', () => {
    const code = `
const post = await client.getPost({ id: 1 })
const author = post.user
console.log(post.title)
console.log(author.name)
console.log(author.email)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('title')
    expect(result).toContain('user{name,email}')
  })

  it('should track index access on nested relations', () => {
    const code = `
const post = await client.getPost({ id: 1 })
console.log(post.comments[0].content)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('comments{content}')
  })

  it('should skip non-model field access', () => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.name)
console.log(user.toString())
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    // toString is not a model field, should not be in $select
    expect(result).toContain("$select: 'name'")
    // $select should only contain 'name', not 'toString'
    expect(result).not.toContain("$select: 'name,toString'")
  })

  it('preserves CallOptions while merging selection into params', () => {
    const code = `
const user = await client.getUser({ id: 1 }, { signal })
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 }, { signal })")
  })

  it('tracks repeated calls to the same API independently', () => {
    const code = `
const first = await client.getUser({ id: 1 })
const second = await client.getUser({ id: 2 })
console.log(first.name)
console.log(second.email)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
    expect(result).toContain("getUser({ $select: 'email', id: 2 })")
  })

  it('tracks paginated item fields through array callbacks', () => {
    const code = `
const page = await client.listUsers({ page: 1, pageSize: 20 })
const names = page.items.map(user => user.name)
console.log(names, page.total)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("listUsers({ $select: 'name', page: 1, pageSize: 20 })")
  })

  it('injects an explicit safe projection when a response escapes static analysis', () => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.name)
consume(user)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name,id,email,phone,score', id: 1 })")
  })

  it('lets dynamic or spread params override an inferred selection', () => {
    const code = `
const params = getParams()
const user = await client.getUser(params, { signal })
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', ...(params) }, { signal })")
  })

  it('injects params for a structured API called without arguments', () => {
    const code = `
const user = await client.me()
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("client.me({ $select: 'name' })")
  })

  it('tracks non-paginated list items in for-of loops', () => {
    const code = `
const users = await client.allUsers()
for (const user of users) console.log(user.email)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("client.allUsers({ $select: 'email' })")
  })

  it('keeps destructured scalar values safe after extraction', () => {
    const code = `
const { name } = await client.getUser({ id: 1 })
consume(name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
  })

  it('uses an explicit safe projection for dynamic property access', () => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.name)
console.log(user[fieldName])
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name,id,email,phone,score', id: 1 })")
  })

  it('injects a safe projection for promise callback consumption', () => {
    const code = `
client.me().then(member => setUser(member))
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("client.me({ $select: 'id,name,email,phone,score' })")
  })

  it('completes a selected structured leaf with its safe scalar projection', () => {
    const code = `
const result = await client.login({ username, password })
saveToken(result.token)
setUser(result.member)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("login({ $select: 'token,member{id,name,email,phone,score}', username, password })")
  })

  it('bounds a recursive structured leaf at its scalar projection', () => {
    const code = `
const node = await client.getRecursiveNode()
consume(node.child)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getRecursiveNode({ $select: 'child{value}' })")
  })

  it('injects a minimal projection when a structured result is unused', () => {
    const code = `
const ignored = await client.getUser({ id: 1 })
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id', id: 1 })")
  })

  it('tracks structured fields consumed by a generated stream callback', () => {
    const code = `
client.subscribeLiveAlerts({ projectId }, user => console.log(user.name, user.email))
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("subscribeLiveAlerts({ $select: 'name,email', projectId }, user =>")
  })

  it('uses a safe explicit projection for an external stream callback', () => {
    const code = `
client.subscribeLiveAlerts({ projectId }, handleAlert)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("subscribeLiveAlerts({ $select: 'id,name,email,phone,score', projectId }, handleAlert)")
  })

  it('uses lexical bindings when result names are shadowed', () => {
    const code = `
const user = await client.getUser({ id: 1 })
function inspect() {
  const user = { email: 'local' }
  consume(user)
}
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
  })
})
