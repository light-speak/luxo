import { describe, it, expect } from 'vitest'
import { analyzeAndTransform } from './analyzer'
import type { LuxoSchema } from '@luxo/client'

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
  apis: {
    getUser: { id: 1, name: 'getUser', module: 'user', returnType: 'User' },
    listUsers: { id: 2, name: 'listUsers', module: 'user', returnType: 'User', returnList: true, paginated: true },
    getPost: { id: 3, name: 'getPost', module: 'post', returnType: 'Post' },
  },
}

describe('analyzeAndTransform', () => {
  it('should inject $select for simple field access', () => {
    const code = `
const user = await client.getUser(1)
console.log(user.name)
console.log(user.email)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select: 'name,email'")
  })

  it('should inject $select for optional chain access', () => {
    const code = `
const user = await client.getUser(1)
const name = user?.name
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select:")
    expect(result).toContain('name')
  })

  it('should handle destructuring', () => {
    const code = `
const { name, email } = await client.getUser(1)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("$select: 'name,email'")
  })

  it('should handle destructure with rename', () => {
    const code = `
const { name: userName, email } = await client.getUser(1)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('name')
    expect(result).toContain('email')
  })

  it('should not inject when all fields are used', () => {
    const code = `
const user = await client.getUser(1)
console.log(user.id, user.name, user.email, user.phone, user.score)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    // All 5 fields used → no optimization needed
    expect(result).toBeNull()
  })

  it('should not inject when $select already present', () => {
    const code = `
const user = await client.getUser(1, { $select: 'name' })
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toBeNull()
  })

  it('should not modify code without field access', () => {
    const code = `
const user = await client.getUser(1)
doSomething(user)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toBeNull()
  })

  it('should handle multiple API calls independently', () => {
    const code = `
const user = await client.getUser(1)
const post = await client.getPost(2)
console.log(user.name)
console.log(post.title)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain("getUser(1, { $select: 'name' })")
    expect(result).toContain("getPost(2, { $select: 'title' })")
  })

  it('should handle template literal field access', () => {
    const code = `
const user = await client.getUser(1)
const msg = \`Hello \${user.name}\`
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('name')
  })

  it('should track nested relation fields', () => {
    const code = `
const post = await client.getPost(1)
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
const post = await client.getPost(1)
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
const post = await client.getPost(1)
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
const post = await client.getPost(1)
console.log(post.comments[0].content)
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).not.toBeNull()
    expect(result).toContain('comments{content}')
  })

  it('should skip non-model field access', () => {
    const code = `
const user = await client.getUser(1)
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
})
