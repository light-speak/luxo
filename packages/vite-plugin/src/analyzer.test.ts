import { describe, it, expect, vi } from 'vitest'
import { parse } from '@babel/parser'
import traverse from '@babel/traverse'
import { traverseFast } from '@babel/types'
import { analyzeAndTransform, analyzeVerifiedEdits } from './analyzer'
import type { LuxoSchema } from '@luxojs/client'
import { Encoder, prepareSelection } from '@luxojs/client'

const finiteSealed: LuxoSchema = { models: {}, sealeds: { Outcome: { name: 'Outcome', variants: [
  { id: 1, name: 'A', fields: [{ id: 1, name: 'value', type: 'Int' }] },
  { id: 2, name: 'B', fields: [{ id: 1, name: 'value', type: 'Int' }] },
  { id: 3, name: 'C', fields: [] },
] } }, apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Outcome' },
  rows: { id: 2, name: 'rows', module: 'test', returnType: 'Outcome', returnList: true } } }

it('retains sealed short-circuit and terminating switch refinements', () => {
  for (const [body, expected] of [
    ["if (!(row.$case !== 'A')) console.log(row.value);", ['A{value}']],
    ["if(row.$case === 'A' || row.$case === 'B') console.log(row.value);", ['A{value}', 'B{value}']],
    ["if(row.$case !== 'C' && 'A' === row.$case) console.log(row.value);", ['A{value}']],
    ["switch(row.$case){case 'A': {return row.value} case 'B': {if(flag)return row.value;else throw Error()} default:return 0}", ['A{value}', 'B{value}']],
    ["switch(row.$case){case 'A': case 'B': console.log(row.value);break;default:break}", ['A{value}', 'B{value}']],
  ] as const) {
    const output = analyzeAndTransform(`async function view(flag){const row=await client.read();${body}}`, 'sealed-control.ts', finiteSealed)
    for (const field of expected) expect(output, body).toContain(field)
    if (expected.length === 1) expect(output, body).not.toContain('B{value}')
  }
})

it('retains sealed conditional call origins', () => {
  const conditional = analyzeAndTransform(`async function view(flag){
    const first=await client.read();const second=await client.read();const row=flag?first:second;
    if(row.$case==='A')console.log(row.value);}`, 'sealed-choices.ts', finiteSealed)
  expect(conditional).toContain('A{value}')
  expect(conditional).not.toContain('B{value}')
  expect(conditional?.match(/\$select:/g)).toHaveLength(2)
  const direct = analyzeAndTransform(`async function view(flag){
    const row=flag?await client.read():await client.read();if(row.$case==='A')console.log(row.value);
  }`, 'sealed-direct-choices.ts', finiteSealed)
  expect(direct).toContain('A{value}')
  expect(direct).not.toContain('B{value}')
  expect(direct?.match(/\$select:/g)).toHaveLength(2)
  for (const use of ['consume(flag?first:second)', "const row=flag?first:opaque;if(row.$case==='A')console.log(row.value)", "const row=flag?opaque:second;if(row.$case==='A')console.log(row.value)"]) {
    const opaque = analyzeAndTransform(`async function view(flag,opaque){
      const first=await client.read();const second=await client.read();${use}
    }`, 'sealed-opaque-choices.ts', finiteSealed)
    expect(opaque).toContain('B{value}')
  }
})

it('retains sealed native filter/find callback origins', () => {
  const found = analyzeAndTransform(`const rows=await client.rows();
    const selected=rows.filter(function(row){return row.$case==='A'});
    const value=selected.find(row=>row.value>0);console.log(value?.value);`, 'sealed-find.ts', finiteSealed)
  expect(found).toContain('A{value}')
  expect(found).not.toContain('B{value}')
})

it('covers sealed finite selection cold controls through public source', () => {
  const common: LuxoSchema = { ...finiteSealed, sealeds: { Outcome: { name: 'Outcome', variants: finiteSealed.sealeds!.Outcome.variants.slice(0, 2) } } }
  const cases = [
    "console.log(row.value);",
    "const bag=row as unknown as Readonly<Record<number,unknown>>;console.log(bag[0]);",
    "if(getTag().$case==='A')console.log(row.value);",
    "if(other.label==='A')console.log(row.value);",
    "if(row.value===other.value)console.log(row.value);",
    "if(flag || row.$case==='A')console.log(row.value);",
    "if(flag && row.$case==='A')console.log(row.value);",
    "switch(other.label){case 'A':console.log(row.value);break;default:break}",
    "switch(row.$case){case label:console.log(row.value);break;default:console.log(row.value);}",
    "switch(row.$case){case 'A':if(flag)return row.value;else throw Error();case 'B':return row.value;}",
  ]
  for (const body of cases) {
    const output = analyzeAndTransform(`async function view(flag,other,label){const row=await client.read();${body}}`, 'sealed-cold-controls.ts', common)
    expect(output, body).toContain('A{value}')
    if (!body.startsWith('if(flag &&')) expect(output, body).toContain('B{value}')
  }
  const mutable = analyzeAndTransform(`async function view(flag){let row=await client.read();if(row.$case==='A')console.log(row.value);if(flag)row=await client.read();}`, 'sealed-cold-mutable.ts', finiteSealed)
  expect(mutable).toContain('B{value}')
  const filtered = analyzeAndTransform(`const rows=await client.rows();const visible=rows.filter(row=>row.$case!=='C');visible.map(row=>console.log(row.value));`, 'sealed-cold-filter.ts', finiteSealed)
  expect(filtered).toContain('A{value}'); expect(filtered).toContain('B{value}'); expect(filtered).not.toContain('C{')
  const found = analyzeAndTransform(`const rows=await client.rows();const row=rows.find(item=>item.$case==='A');console.log(row?.value);`, 'sealed-cold-find.ts', finiteSealed)
  expect(found).toContain('A{value}'); expect(found).not.toContain('B{value}')
  const opaque = analyzeAndTransform(`const rows=await client.rows();const visible=rows.filter(row=>{log(row.$case);return flag});visible.map(row=>console.log(row.value));`, 'sealed-cold-block.ts', common)
  expect(opaque).toContain('A{value}'); expect(opaque).toContain('B{value}')
})

it('covers sealed transparent choices, nullable origins and recursive escape rejection', () => {
  const nullable: LuxoSchema = { ...finiteSealed, apis: { ...finiteSealed.apis, read: { ...finiteSealed.apis.read, returnNullable: true } } }
  const presence = analyzeAndTransform(`async function view(flag){const first=await client.read();const second=await client.read();const row=flag?(first as Outcome):(second as Outcome);if((row as Outcome|null)!==null)console.log(row.$case);}`, 'sealed-cold-presence.ts', nullable)
  expect(presence).toContain('A,B,C'); expect(presence).not.toContain('{value}')
  const narrowed = analyzeAndTransform(`const row=await client.read();if(row.$case==='A'){if((row as A)!==null)console.log(row.value);}`, 'sealed-cold-case-presence.ts', finiteSealed)
  expect(narrowed).toContain('A{value}'); expect(narrowed).not.toContain('B{value}')
  const ordinary = analyzeAndTransform('const row=await client.getUser({id:1n});console.log(row.id);', 'sealed-cold-no-sums.ts', schema)
  expect(ordinary).toContain("$select: 'id'")
  const intersection = analyzeAndTransform("const row=await client.read();if(row.$case==='A' && flag)console.log(row.value);", 'sealed-cold-intersection.ts', finiteSealed)
  expect(intersection).toContain('A{value}');expect(intersection).not.toContain('B{value}')
  const mixedSchema: LuxoSchema = { ...finiteSealed, models: { User: { name: 'User', fields: [{ id: 1, name: 'id', type: 'Int' }] } }, apis: { ...finiteSealed.apis, user: { id: 3, name: 'user', module: 'test', returnType: 'User' } } }
  const mixed = analyzeAndTransform(`const row=await client.read();const user=await client.user();const mixed=[row,user,{$case:'A',embedded:row}];const matches=mixed.filter(item=>(item as unknown as {readonly $case?:string}).$case==='A');console.log(matches.length);`, 'sealed-cold-mixed.ts', mixedSchema)
  expect(mixed).toContain('A');expect(mixed).toContain("user({ $select: 'id' })")
  const recursive: LuxoSchema = { models: {}, sealeds: { Loop: { name: 'Loop', variants: [{ id: 1, name: 'More', fields: [{ id: 1, name: 'child', type: 'Sealed', typeName: 'Loop' }] }] } }, apis: { read: { id: 1, name: 'read', module: 'test', returnType: 'Loop' } } }
  expect(() => analyzeAndTransform('const row=await client.read();consume(row);', 'sealed-cold-recursion.ts', recursive)).toThrow(/recursive sealed selection/)
})

it.each([
  { name:'if', body:`if (result.$case === 'Success') console.log(result.value);` },
  { name:'switch', body:`switch (result.$case) { case 'Success': console.log(result.value); break; case 'Pending': break; }` },
  { name:'destructured branch', body:`if (result.$case === 'Success') { const {value}=result; console.log(value); }` },
])('selects sealed payloads through native $name branches', async ({body}) => {
  const { readFileSync } = await import('node:fs')
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/sealed-v1.json', import.meta.url), 'utf8')) as { schema:LuxoSchema }
  const result = analyzeAndTransform(`const result=await client.echo({value:{$case:'Pending'}}); ${body}`, 'sealed.ts', fixture.schema)
  expect(result).toContain('Success{value}')
  expect(result).not.toContain('Failure{reason}')
  expect(result).not.toContain('Nested{')
})

it('keeps different calls and nested negative guards separate for sealed payloads', async () => {
  const { readFileSync } = await import('node:fs')
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/sealed-v1.json', import.meta.url), 'utf8')) as { schema:LuxoSchema }
  const result = analyzeAndTransform(`const first=await client.echo({value:{$case:'Pending'}});
    const second=await client.echo({value:{$case:'Pending'}});
    if(first.$case!=='Success') {
      if(first.$case==='Failure') console.log(first.reason);
    }
    if(second.$case==='Success') console.log(second.value);`, 'sealed-calls.ts', fixture.schema)
  expect(result).toContain('Failure{reason}')
  expect(result).toContain('Success{value}')
  expect(result).not.toContain('Nested{')
})

it('narrows a sealed choice before selecting a shared payload field', () => {
  const fixture:LuxoSchema = {models:{},sealeds:{Result:{name:'Result',variants:[
    {id:1,name:'A',fields:[{id:1,name:'value',type:'Int'}]},
    {id:2,name:'B',fields:[{id:1,name:'value',type:'Int'}]},
    {id:3,name:'C',fields:[]},
  ]}},apis:{read:{id:1,name:'read',module:'unit',returnType:'Result'}}}
  const result = analyzeAndTransform(`const row=await client.read();
    if(row.$case!=='C') { if(row.$case==='A') console.log(row.value); }`, 'sealed-choice.ts', fixture)
  expect(result).toContain('A{value}')
  expect(result).not.toContain('B{value}')
})

it('keeps a sealed switch default limited to remaining real variants', () => {
  const fixture:LuxoSchema = {models:{},sealeds:{Result:{name:'Result',variants:[
    {id:1,name:'A',fields:[{id:1,name:'value',type:'Int'}]},
    {id:2,name:'B',fields:[{id:1,name:'value',type:'Int'}]},
    {id:3,name:'C',fields:[]},
  ]}},apis:{read:{id:1,name:'read',module:'unit',returnType:'Result'}}}
  const result = analyzeAndTransform(`const row=await client.read();
    switch(row.$case) { case 'C': break; default: console.log(row.value); }`, 'sealed-default.ts', fixture)
  expect(result).toContain('A{value}')
  expect(result).toContain('B{value}')
  expect(result).not.toContain('C{')
})

it('selects sealed nullable and list metadata without loading case payloads', async () => {
  const { readFileSync } = await import('node:fs')
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/sealed-v1.json', import.meta.url), 'utf8')) as { schema:LuxoSchema }
  const result = analyzeAndTransform(`const row=await client.echo({value:{$case:'Nested'}});
    if(row.$case==='Nested') { if(row.child===null) console.log('nil'); console.log(row.history?.length); }`, 'sealed-presence.ts', fixture.schema)
  expect(result).toContain('child{')
  expect(result).toContain('history{')
  expect(result).not.toContain('Failure{reason}')
  expect(result).not.toContain('Success{value}')
})

it('selects sealed filter/map payloads without flattening case provenance', async () => {
  const { readFileSync } = await import('node:fs')
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/sealed-v1.json', import.meta.url), 'utf8')) as { schema:LuxoSchema }
  fixture.schema.apis.many = {id:4,name:'many',module:'unit',returnType:'Outcome',returnList:true}
  const result = analyzeAndTransform(`const rows=await client.many();
    const successes=rows.filter(row=>row.$case==='Success');
    successes.map(row=>console.log(row.value));`, 'sealed-list.ts', fixture.schema)
  expect(result).toContain('Success{value}')
  expect(result).not.toContain('Failure{reason}')
  expect(result).not.toContain('Nested{')
})

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

async function expectPrivateCachesReleased(action: () => Promise<void>): Promise<void> {
  const foreign = parse('const outside = 1', { sourceType: 'module' })
  traverse(foreign, { Identifier(path) { path.scope.getBinding(path.node.name) } })
  const paths = traverse.cache.path
  const scopes = traverse.cache.scope
  const foreignPaths = paths.get(foreign)
  const foreignScope = scopes.get(foreign.program)
  expect(foreignPaths).toBeDefined()
  expect(foreignScope).toBeDefined()
  const pathWrites = vi.spyOn(paths, 'set')
  const scopeWrites = vi.spyOn(scopes, 'set')
  try {
    await action()
    expect(pathWrites.mock.calls.length).toBeGreaterThan(0)
    expect(scopeWrites.mock.calls.length).toBeGreaterThan(0)
    for (const [node] of pathWrites.mock.calls) expect(paths.has(node)).toBe(false)
    for (const [node] of scopeWrites.mock.calls) expect(scopes.has(node)).toBe(false)
    expect(traverse.cache.path.get(foreign)).toBe(foreignPaths)
    expect(traverse.cache.scope.get(foreign.program)).toBe(foreignScope)
  } finally {
    pathWrites.mockRestore()
    scopeWrites.mockRestore()
    paths.delete(foreign)
    traverseFast(foreign, node => { scopes.delete(node) })
  }
}

describe('analyzeAndTransform', () => {
  it('preserves Unicode API and model field wire names in inferred selections', () => {
    const fixture: LuxoSchema = { models: { Café: { name: 'Café', fields: [
      { id: 1, name: 'café', type: 'String' }, { id: 2, name: '𐐀_name', type: 'String' },
    ] } }, apis: { café: { id: 1, name: 'café', module: 'test', returnType: 'Café' } } }
    const source = 'async function view() { const row = await client.café(); console.log(row.café, row.𐐀_name) }'
    expect(analyzeAndTransform(source, 'unicode.ts', fixture)).toContain("$select: 'café,𐐀_name'")
  })
  it.each(['sync', 'verified', 'no-candidates', 'rejected', 'sync-error', 'verified-error'])('releases only private AST caches after %s analysis', async mode => {
    const code = 'async function view() { const users = await client.allUsers(); return users.find(user => user.score > 0)?.name } void view'
    await expectPrivateCachesReleased(async () => {
      const rejectedFields: LuxoSchema = { ...schema, models: { ...schema.models, User: {
        name: 'User', get fields(): never { throw new Error('fields rejected') },
      } } }
      if (mode === 'sync-error') {
        expect(() => analyzeAndTransform(code, 'cache.ts', rejectedFields)).toThrow('fields rejected')
      } else if (mode === 'verified-error') {
        await expect(analyzeVerifiedEdits(code, 'cache.ts', rejectedFields, async candidates =>
          new Set(candidates.map(candidate => candidate.memberStart)))).rejects.toThrow('fields rejected')
      } else if (mode === 'sync') {
        expect(analyzeAndTransform(code, 'cache.ts', schema)).toContain('$select:')
      } else if (mode === 'no-candidates') {
        const verify = vi.fn(async () => new Set<number>())
        expect(await analyzeVerifiedEdits('function outer(value) { return value.name }; outer({ name: "local" })', 'cache.ts', schema, verify)).toBeNull()
        expect(verify).not.toHaveBeenCalled()
      } else if (mode === 'rejected') {
        await expect(analyzeVerifiedEdits(code, 'cache.ts', schema, async () => {
          throw new Error('verification rejected')
        })).rejects.toThrow('verification rejected')
      } else {
        const edits = await analyzeVerifiedEdits(code, 'cache.ts', schema, async candidates =>
          new Set(candidates.map(candidate => candidate.memberStart)))
        expect(edits?.length).toBeGreaterThan(0)
      }
    })
  })

  it('preserves the cache keys of an overlapping verification until it finishes', async () => {
    const code = 'const users = await client.allUsers(); consume(users.find(user => user.score > 0)?.name)'
    const paths = traverse.cache.path
    const scopes = traverse.cache.scope
    const pathWrites = vi.spyOn(paths, 'set')
    const scopeWrites = vi.spyOn(scopes, 'set')
    let proceed!: () => void
    const gate = new Promise<void>(resolve => { proceed = resolve })
    const pending = analyzeVerifiedEdits(code, 'pending.ts', schema, async candidates => {
      await gate
      return new Set(candidates.map(candidate => candidate.memberStart))
    })
    const pathKeys = pathWrites.mock.calls.map(([node]) => node)
    const scopeKeys = scopeWrites.mock.calls.map(([node]) => node)
    pathWrites.mockRestore()
    scopeWrites.mockRestore()
    try {
      expect(pathKeys.length).toBeGreaterThan(0)
      expect(scopeKeys.length).toBeGreaterThan(0)
      expect(analyzeAndTransform(code, 'other.ts', schema)).toContain('$select:')
      for (const node of pathKeys) expect(paths.has(node)).toBe(true)
      for (const node of scopeKeys) expect(scopes.has(node)).toBe(true)
    } finally {
      proceed()
      await pending
    }
    for (const node of pathKeys) expect(paths.has(node)).toBe(false)
    for (const node of scopeKeys) expect(scopes.has(node)).toBe(false)
  })

  it.each([false, true].flatMap(prepared => [1, 2].map(depth => ({ prepared, depth }))))
  ('preserves nested request edits (prepared=$prepared, depth=$depth)', async ({ prepared, depth }) => {
    let request = 'await client.getUser({ id: 1n })'
    for (let i = 0; i < depth; i++) {
      request = `await client.getUser(await (async () => {
        const seed${i} = ${request}
        return { id: seed${i}.id }
      })())`
    }
    const source = `async function view(client) { const user = ${request}; return user.name }`
    const result = analyzeAndTransform(source, 'nested.ts', schema, prepared)
    expect(result).not.toBeNull()
    type Params = { id: bigint; $select: string | { text: string } }
    type Client = { getUser(params: Params): Promise<{ id: bigint; name: string }> }
    const create = new Function('$luxoSelectionPrepare', result!.replace(/^import .*\n/gm, '') + '\nreturn view') as
      (prepare: typeof prepareSelection) => (client: Client) => Promise<string>
    const selections: string[] = []
    const value = await create(prepareSelection)({ async getUser(params) {
      expect(params.id).toBe(1n)
      const selected = params.$select
      selections.push(typeof selected === 'string' ? selected : selected.text)
      return { id: params.id, name: 'nested' }
    } })
    expect(value).toBe('nested')
    expect(selections).toEqual([...Array<string>(depth).fill('id'), 'name'])
  })

  it('retains comments inside an otherwise empty parameter object', () => {
    const source = 'const user = await client.me({ /* keep 中文😀 */ }); console.log(user.name)'
    const result = analyzeAndTransform(source, 'comment.ts', schema)
    expect(result).toContain('/* keep 中文😀 */')
    expect(result).toContain("$select: 'name'")
  })

  it('reports source syntax errors instead of silently skipping field analysis', () => {
    expect(() => analyzeAndTransform('const user = await client.me(); const broken = ;', 'broken.ts', schema))
      .toThrow(/broken\.ts/)
  })

  it('tracks satisfies expressions without falling back to all fields', () => {
    const code = `
const user = (await client.getUser({ id: 9007199254740993n })) satisfies { name: string }
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'consumer.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 9007199254740993n })")
    expect(result).toContain('satisfies { name: string }')
  })

  it('keeps aliases through satisfies and type assertions precise', () => {
    const code = `
const user = await client.me()
const alias = (user satisfies { email: string }) as { email: string }
console.log(alias.email)
`
    expect(analyzeAndTransform(code, 'consumer.ts', schema)).toContain("client.me({ $select: 'email' })")
  })

  it('supports angle-bracket assertions in TS and JSX only in TSX', () => {
    const code = 'const user = <{ name: string }> await client.me(); console.log(user.name)'
    expect(analyzeAndTransform(code, 'consumer.ts', schema)).toContain("client.me({ $select: 'name' })")
    const jsx = 'const user = await client.me(); const view = <div>{user.name}</div>'
    expect(analyzeAndTransform(jsx, 'view.tsx?import', schema)).toContain("client.me({ $select: 'name' })")
    expect(() => analyzeAndTransform(jsx, 'view.ts', schema)).toThrow(/view\.ts/)
    for (const extension of ['mts', 'cts']) {
      expect(() => analyzeAndTransform(code, `consumer.${extension}`, schema)).toThrow(`Cannot analyze consumer.${extension}:`)
    }
  })

  it('handles decorators, const type parameters and resource declarations around API calls', () => {
    const code = `
@decorate
class View {
  accessor title = ''
  identity<const T>(value: T): T { return value }
}
await using resource = acquireResource()
const user = await client.me()
console.log(user.name)
`
    const result = analyzeAndTransform(code, 'modern.ts', schema)
    expect(result).toContain("client.me({ $select: 'name' })")
    expect(result).toContain('identity<const T>')
    expect(result).toContain('await using resource')
  })

  it('handles missing AST nodes and array pattern holes with Babel 8 paths', () => {
    const code = `
let later: string
const [, user] = await client.allUsers()
console.log(user.name)
`
    expect(analyzeAndTransform(code, 'consumer.ts', schema)).toContain("client.allUsers({ $select: 'name' })")
  })

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

  it.each([
    'consume([user])',
    'const rows = [user]; consume(rows)',
    'consume({ user })',
    'const row = { user }; consume(row)',
    'consume(() => user)',
  ])('keeps the safe projection for aggregate escape: %s', usage => {
    const code = `
const user = await client.getUser({ id: 1 })
console.log(user.name)
${usage}
`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name,id,email,phone,score', id: 1 })")
  })

  it.each([
    ['object element', 'const packed = { first: users[0] }; consume(packed.first.name)'],
    ['non-null element', 'const packed = { first: users[0]! }; consume(packed.first.name)'],
    ['computed property', "const packed = { first: users[0]! }; consume(packed['first'].name)"],
    ['numeric-string element', "const packed = { first: users['0']! }; consume(packed.first.name)"],
    ['nested object', 'const packed = { inner: { first: users[0]! } }; consume(packed.inner.first.name)'],
    ['array element', 'const packed = [users[0]!]; consume(packed[0]!.name)'],
    ['container alias', 'const packed = { first: users[0]! }; const alias = packed; consume(alias.first.name)'],
    ['destructured element', 'const packed = { first: users[0]! }; const { first } = packed; consume(first.name)'],
    ['readonly object', 'const packed = ({ first: users[0]! } as const); consume(packed.first.name)'],
    ['array destructure', 'const packed = [users[0]!]; const [first] = packed; consume(first.name)'],
    ['nested destructure', 'const packed = [users[0]!]; const [{ name }] = packed; consume(name)'],
    ['array rest', 'const packed = [users[0]!]; const [...rest] = packed; consume(rest[0]!.name)'],
    ['array rest offset', 'const packed = [0, users[0]!]; const [, ...rest] = packed; consume(rest[0]!.name)'],
    ['object rest', 'const packed = { local: 0, first: users[0]! }; const { local, ...rest } = packed; consume(rest.first.name)'],
    ['object default', 'const packed = { first: users[0]! }; const { first = users[1]! } = packed; consume(first.name)'],
    ['array default', 'const packed = [users[0]!]; const [first = users[1]!] = packed; consume(first.name)'],
    ['inline container alias', 'const first = ({ first: users[0]! }).first; consume(first.name)'],
    ['parenthesized reference', 'const packed = { first: users[0]! }; consume((packed).first.name)'],
    ['numeric object key', 'const packed = { 0: users[0]! }; consume(packed[0].name)'],
    ['unrelated scalar', 'const packed = { first: users[0]!, label: "local" }; consume(packed.first.name, packed.label)'],
    ['array callback alias', 'const packed = [users[0]!]; consume(packed.map(user => user.name))'],
    ['array string index', "const packed = [users[0]!]; consume(packed['0'].name)"],
    ['sparse array slot', 'const packed = [, users[0]!]; consume(packed[1]!.name)'],
  ])('tracks statically stored model elements without widening selection: %s', (_name, usage) => {
    const code = `const users = await client.allUsers(); ${usage}`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'name' })")
  })

  it('keeps calls stored in the same container independent', () => {
    const result = analyzeAndTransform(`
const packed = { left: await client.getUser({ id: 1 }), right: await client.getUser({ id: 2 }) }
consume(packed.left.name, packed.right.email)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
    expect(result).toContain("getUser({ $select: 'email', id: 2 })")
  })

  it('tracks both model sources of a destructuring default', () => {
    const result = analyzeAndTransform(`
const first = await client.getUser({ id: 1 })
const fallback = await client.getUser({ id: 2 })
const packed = { first }
const { first: chosen = fallback } = packed
consume(chosen.name)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
    expect(result).toContain("getUser({ $select: 'name', id: 2 })")
  })

  it('retains only remaining API origins in object and array rest', () => {
    const result = analyzeAndTransform(`
const left = await client.getUser({ id: 1 })
const right = await client.getUser({ id: 2 })
const { left: discarded, ...rest } = { left, right }
const [, ...tail] = [left, right]
consume(rest.right.email, tail[0].name)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id', id: 1 })")
    expect(result).toContain("getUser({ $select: 'email,name', id: 2 })")
  })

  it('selects a stored relation without selecting unrelated root fields', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
const packed = { author: post.user }
const { author } = packed
consume(author.name)
`, 'test.ts', schema)
    expect(result).toContain("getPost({ $select: 'user{name}', id: 1 })")
  })

  it('completes a partially read relation when the whole value escapes', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
consume(post.user.name)
opaque(post.user)
`, 'test.ts', schema)
    expect(result).toContain("getPost({ $select: 'user{name,id,email,phone,score}', id: 1 })")
  })

  it('completes a partially read nested list for an opaque list operation', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
consume(post.comments.map(comment => comment.content))
post.comments.sort(compare)
`, 'test.ts', schema)
    expect(result).toContain("getPost({ $select: 'comments{content,id,user{id,name,email,phone,score}}', id: 1 })")
  })

  it('retains explicit deeper escapes while completing an escaped parent', () => {
    const model = schema.models.User!
    const local: LuxoSchema = { ...schema, models: { ...schema.models, User: { ...model, fields: [
      ...model.fields, { id: 6, name: 'manager', type: 'Model', typeName: 'User', relation: true },
    ] } } }
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
consume(post.user.manager.name)
opaque(post.user)
opaque(post.user.manager)
`, 'test.ts', local)
    expect(result).toContain("getPost({ $select: 'user{manager{name,id,email,phone,score},id,name,email,phone,score}', id: 1 })")
  })

  it('keeps a model and a literal fallback on their original field paths', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
const user = await client.getUser({ id: 2 })
const { first: chosen = { user } } = { first: post }
consume(chosen.user.name)
`, 'test.ts', schema)
    expect(result).toContain("getPost({ $select: 'user{name}', id: 1 })")
    expect(result).toContain("getUser({ $select: 'name', id: 2 })")
  })

  it.each([
    'consume(chosen.filter(user => user.score > 0).map(user => user.name))',
    'for (const user of chosen) consume(user.score, user.name)',
    'const [first, ...rest] = chosen; consume(first.score, rest[0].name)',
  ])('tracks both list origins through default usage: %s', usage => {
    const result = analyzeAndTransform(`
const one = await client.allUsers()
const two = await client.allUsers()
const { users: chosen = two } = { users: one }
${usage}
`, 'test.ts', schema)
    const fields = [...result!.matchAll(/allUsers\(\{ \$select: '([^']+)'/gu)].map(match => match[1]!.split(',').sort())
    expect(fields).toEqual([['name', 'score'], ['name', 'score']])
  })

  it.each([
    ['first callback rest', 'users.map((...args) => args[0].name)', ['name']],
    ['second callback rest', 'users.map((user, ...tail) => user.name + tail[1][0].email)', ['email', 'name']],
    ['third callback rest', 'users.map((user, index, ...tail) => user.name + tail[0][0].email)', ['email', 'name']],
  ])('tracks array callback rest arguments: %s', (_name, usage, expected) => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); consume(${usage})`, 'test.ts', schema)
    expect(result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',').sort()).toEqual(expected)
  })

  it('tracks a rest subscription payload as the first argument', () => {
    const result = analyzeAndTransform('client.subscribeLiveAlerts({ projectId }, (...args) => consume(args[0].name))', 'test.ts', schema)
    expect(result).toContain("subscribeLiveAlerts({ $select: 'name', projectId }")
  })

  it('handles a long immutable aggregate alias chain without recursive overflow', () => {
    const aliases = Array.from({ length: 4096 }, (_, index) => `const a${index + 1} = a${index}`).join('\n')
    const result = analyzeAndTransform(`const users = await client.allUsers(); const a0 = { first: users[0]! }; ${aliases}; consume(a4096.first.name)`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'name' })")
  })

  it('tracks an API call inside a missing-property default', () => {
    const result = analyzeAndTransform(`
const unused = await client.getUser({ id: 1 })
const { missing: chosen = await client.getUser({ id: 2 }) } = { unused }
consume(chosen.name)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id', id: 1 })")
    expect(result).toContain("getUser({ $select: 'name', id: 2 })")
  })

  it('flattens repeated defaults without merging independent call sites', () => {
    const result = analyzeAndTransform(`
const one = await client.getUser({ id: 1 })
const two = await client.getUser({ id: 2 })
const three = await client.getUser({ id: 3 })
const { value: first = two } = { value: one }
const { value: second = first } = { value: three }
const { value: third = second } = { value: first }
consume(third.name)
`, 'test.ts', schema)
    for (const id of [1, 2, 3]) expect(result).toContain(`getUser({ $select: 'name', id: ${id} })`)
  })

  it('keeps an unused rest empty and an excluded model unselected', () => {
    const result = analyzeAndTransform(`
const user = await client.getUser({ id: 1 })
const { user: ignored, ...rest } = { user, label: 'local' }
consume(rest)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id', id: 1 })")
  })

  it('does not read a model through an out-of-range literal-container index', () => {
    const result = analyzeAndTransform(`
const user = await client.getUser({ id: 1 })
const packed = [user]
consume(packed[99]?.name)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id', id: 1 })")
  })

  it('keeps distinct relation paths of one API origin in a default', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
const { value: chosen = post.comments[0]!.user } = { value: post.user }
consume(chosen.name)
`, 'test.ts', schema)
    expect(result).toContain("getPost({ $select: 'user{name},comments{user{name}}', id: 1 })")
  })

  it('keeps equally deep relation paths of one origin distinct', () => {
    const model = schema.models.User!
    const local: LuxoSchema = { ...schema, models: { ...schema.models, User: { ...model, fields: [
      ...model.fields, { id: 6, name: 'manager', type: 'Model', typeName: 'User' },
      { id: 7, name: 'mentor', type: 'Model', typeName: 'User' },
    ] } } }
    const result = analyzeAndTransform(`
const user = await client.getUser({ id: 1 })
const { value: chosen = user.mentor } = { value: user.manager }
consume(chosen.name)
`, 'test.ts', local)
    expect(result).toContain("getUser({ $select: 'manager{name},mentor{name}', id: 1 })")
  })

  it('preserves opaque model-rest behavior through default alternatives', () => {
    const result = analyzeAndTransform(`
const one = await client.getUser({ id: 1 })
const two = await client.getUser({ id: 2 })
const { first: chosen = two } = { first: one }
const { id, ...rest } = chosen
consume(rest)
`, 'test.ts', schema)
    for (const id of [1, 2]) expect(result).toContain(`getUser({ $select: 'id,name,email,phone,score', id: ${id} })`)
  })

  it.each([
    '[user].map(item => item.name)',
    '([user].map)(item => item.name)',
    '[user].filter(item => item.score > 0).map(item => item.name)',
    '[user].find(item => item.score > 0)?.name',
    '[user].filter(item => item.score > 0)[0]?.name',
    '(() => { const rows = [user].filter(item => item.score > 0); const [first, ...rest] = rows; return [first?.name, rest[0]?.name] })()',
  ])('tracks native array literals without guessing object methods: %s', usage => {
    const result = analyzeAndTransform(`const user = await client.getUser({ id: 1 }); consume(${usage})`, 'test.ts', schema)
    const expected = usage.includes('score') ? ['name', 'score'] : ['name']
    expect(result?.match(/getUser\(\{ \$select: '([^']+)'/u)?.[1]?.split(',').sort()).toEqual(expected)
  })

  it('keeps unrelated object map methods at the existing opaque boundary', () => {
    const result = analyzeAndTransform(`
const user = await client.getUser({ id: 1 })
consume(({ first: user, map: customMap }).map(callback))
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id,name,email,phone,score', id: 1 })")
  })

  it.each([
    'users.sort((left, right) => left.name.localeCompare(right.name))',
    'users.reduce((names, user) => [...names, user.name], [])',
  ])('keeps data available for opaque list methods: %s', usage => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); consume(${usage})`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'id,name,email,phone,score' })")
  })

  it('preserves an opaque external filter callback', () => {
    const result = analyzeAndTransform('const users = await client.allUsers(); consume(users.filter(predicate).map(user => user.name))', 'test.ts', schema)
    expect(result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',').sort()).toEqual(['email', 'id', 'name', 'phone', 'score'])
  })

  it.each(['const [first] = user', 'const [...rest] = user'])('bounds an unsupported model array pattern: %s', pattern => {
    const result = analyzeAndTransform(`const user = await client.getUser({ id: 1 }); ${pattern}`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'id,name,email,phone,score', id: 1 })")
  })

  it.each(['external', 'state.current'])('keeps fields available for an external for-of target: %s', target => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); for (${target} of users) consume(${target}.name)`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'id,name,email,phone,score' })")
  })

  it('leaves ordinary loop values separate from SDK values', () => {
    const result = analyzeAndTransform(`
for (const value of localRows) consume(value.name)
const user = await client.getUser({ id: 1 })
consume(user.name)
`, 'test.ts', schema)
    expect(result).toContain("getUser({ $select: 'name', id: 1 })")
  })

  it.each([
    'const packed = { first: users[0]! }; consume(packed)',
    'const packed = { first: users[0]! }; const alias = packed; consume(alias)',
    'const packed = { first: users[0]! }; packed.first = users[1]!; consume(packed.first.name)',
    'const packed = { first: users[0]! }; delete packed.first; consume(packed.first?.name)',
    'const packed = { first: users[0]! }; consume(packed[key].name)',
    'const packed = { first: users[0]!, ...other }; consume(packed.first.name)',
    'const packed = [users[0]!, ...other]; consume(packed[0].name)',
    'const packed = { __proto__: users[0]! }; consume(packed.name)',
    'const packed = { get first() { return users[0]! } }; consume(packed.first.name)',
    'let packed = { first: users[0]! }; packed = replacement; consume(packed.first.name)',
    'const packed = { first: users[0]! }; let alias = packed; alias = replacement; consume(alias.first.name)',
    'const packed = [users[0]!]; packed.push(users[1]!); consume(packed[0].name)',
    'const packed = { first: users[0]! }; const { [key]: first } = packed; consume(first.name)',
    'const packed = { [key]: users[0]! }; consume(packed)',
  ])('retains the existing safe projection at an opaque container boundary: %s', usage => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); ${usage}`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'id,name,email,phone,score' })")
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

  it.each([
    ['filter-map', 'users.filter(user => user.score > 0).map(user => user.name)', 'score,name'],
    ['filter-filter-map', 'users.filter(user => user.score > 0).filter(user => user.email).map(user => user.name)', 'score,email,name'],
    ['filter-alias', '(() => { const active = users.filter(user => user.score > 0); return active.map(user => user.name) })()', 'score,name'],
    ['filter-index', 'users.filter(user => user.score > 0)[0]?.name', 'score,name'],
    ['find-member', 'users.find(user => user.score > 0)?.name', 'score,name'],
    ['filter-find-member', 'users.filter(user => user.score > 0).find(user => user.email)?.name', 'score,email,name'],
    ['find-alias', '(() => { const found = users.find(user => user.score > 0); return found?.email })()', 'score,email'],
    ['optional-find', 'users?.find?.(user => user.score > 0)?.name', 'score,name'],
    ['find-callback-array', 'users.find((user, index, all) => all[0].email && user.score > 0)?.name', 'email,score,name'],
    ['computed-find', "users['find'](user => user.score > 0)?.name", 'score,name'],
    ['parenthesized-filter', '(users.filter)(user => user.score > 0).map(user => user.name)', 'score,name'],
    ['asserted-filter', 'users.filter!(user => user.score > 0).map(user => user.name)', 'score,name'],
    ['find-destructure', '(() => { const { name } = users.find(user => user.score > 0)!; return name })()', 'score,name'],
  ])('tracks chained collection usage: %s', (_name, expression, selection) => {
    const code = `const users = await client.allUsers(); const result = ${expression}; consume(result)`
    const result = analyzeAndTransform(code, 'test.ts', schema)
    expect(result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',').sort()).toEqual(selection?.split(',').sort())
  })

  it.each([
    ['optional receiver', 'users?.filter(user => user.score > 0).map(user => user.name)'],
    ['optional method', 'users.filter?.(user => user.score > 0).map(user => user.name)'],
    ['computed method', "users['filter'](user => user.score > 0).map(({ name }) => name)"],
    ['callback array', 'users.filter((user, index, all) => all[index].email && user.score > 0).map(user => user.name)'],
  ])('preserves collection provenance for %s', (_name, expression) => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); consume(${expression})`, 'test.ts', schema)
    const fields = result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',')
    expect(fields).toEqual(expect.arrayContaining(['score', 'name']))
    if (_name === 'callback array') expect(fields).toContain('email')
  })

  it('does not treat mapped scalar elements as the original model', () => {
    const result = analyzeAndTransform(`
const users = await client.allUsers()
consume(users.map(user => user.name).filter(name => name.length > 0).map(name => name.toUpperCase()))
`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'name' })")
  })

  it('does not treat a found scalar as the original model', () => {
    const result = analyzeAndTransform(`
const users = await client.allUsers()
consume(users.map(user => user.name).find(name => name.length > 0))
`, 'test.ts', schema)
    expect(result).toContain("allUsers({ $select: 'name' })")
  })

  it('keeps a safe projection when an unbound filtered result escapes', () => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); consume(users.filter(user => user.score > 0))`, 'test.ts', schema)
    const fields = result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',')
    expect(fields?.sort()).toEqual(['email', 'id', 'name', 'phone', 'score'])
  })

  it('keeps a safe projection when a found model escapes', () => {
    const result = analyzeAndTransform(`const users = await client.allUsers(); consume(users.find(user => user.score > 0))`, 'test.ts', schema)
    const fields = result?.match(/allUsers\(\{ \$select: '([^']+)'/u)?.[1]?.split(',')
    expect(fields?.sort()).toEqual(['email', 'id', 'name', 'phone', 'score'])
  })

  it('executes the chained view with only the selected response fields', async () => {
    const source = `async function view() {
 const users = await client.allUsers()
 return users.filter(user => user.score > 0).map(user => user.name)
}
return view()`
    // The analyzer accepts modules; expose the function after transformation.
    const transformed = analyzeAndTransform(source.replace('return view()', ''), 'test.ts', schema)
    const rows = [{ name: 'active', score: 1 }, { name: 'inactive', score: 0 }]
    const client = { async allUsers(params: { $select: string }) {
      return rows.map(row => Object.fromEntries(Object.entries(row).filter(([name]) => params.$select.split(',').includes(name))))
    } }
    const view = new Function('client', `${transformed}; return view()`) as (apiClient: typeof client) => Promise<unknown>
    expect(await view(client)).toEqual(['active'])
  })

  it('executes the find view with only the selected response fields', async () => {
    const source = `async function view() {
 const users = await client.allUsers()
 return users.find(user => user.score > 0)?.name
}`
    const transformed = analyzeAndTransform(source, 'test.ts', schema)
    const rows = [{ name: 'active', score: 1 }, { name: 'inactive', score: 0 }]
    const client = { async allUsers(params: { $select: string }) {
      return rows.map(row => Object.fromEntries(Object.entries(row).filter(([name]) => params.$select.split(',').includes(name))))
    } }
    const view = new Function('client', `${transformed}; return view()`) as (apiClient: typeof client) => Promise<unknown>
    expect(await view(client)).toBe('active')
  })

  it('tracks found nested relations and paginated items', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
const page = await client.listUsers()
consume(post.comments.find(comment => comment.content)?.user.name)
consume(page.items.find(user => user.score > 0)?.email)
`, 'test.ts', schema)
    const metadata = {
      id: 3,
      fields: { comments: { fieldID: 7, typeName: 'Comment' }, score: { fieldID: 5 }, email: { fieldID: 3 } },
      types: { Comment: { content: { fieldID: 2 }, user: { fieldID: 3, typeName: 'User' } }, User: { name: { fieldID: 2 } } },
    }
    const mask = (selection: string): Uint8Array => {
      const encoder = new Encoder()
      prepareSelection(selection).write(encoder, metadata)
      return encoder.bytes()
    }
    const post = result?.match(/getPost\(\{ \$select: '([^']+)'/u)?.[1]
    const page = result?.match(/listUsers\(\{ \$select: '([^']+)'/u)?.[1]
    expect(post).toBeDefined()
    expect(page).toBeDefined()
    expect(mask(post!)).toEqual(mask('comments{content,user{name}}'))
    expect(mask(page!)).toEqual(mask('score,email'))
  })

  it('tracks filtered nested relations and paginated items', () => {
    const result = analyzeAndTransform(`
const post = await client.getPost({ id: 1 })
const page = await client.listUsers()
consume(post.comments.filter(comment => comment.content).map(comment => comment.user.name))
consume(page.items.filter(user => user.score > 0).map(user => user.email))
`, 'test.ts', schema)
    expect(result).toContain("$select: 'comments{content,user{name}}'")
    expect(result).toContain("$select: 'score,email'")
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

it('hoists separate immutable selection plans in the production transform', () => {
  const code = `'use client';
const $luxoSelection = 1;
async function run(params) {
  const a = await client.getUser({ id: 1 });
  console.log(a.name);
  const b = await client.getUser(params);
  console.log(b.email);
}`
  const output = analyzeAndTransform(code, 'consumer.ts', schema, true)!
  expect(output.startsWith("'use client';")).toBe(true)
  expect(output).toContain('$luxoSelection_Prepare("name")')
  expect(output).toContain('$luxoSelection_Prepare("email")')
  expect(output).toContain('getUser({ $select: $luxoSelection_0, id: 1 })')
  expect(output).toContain('getUser({ $select: $luxoSelection_1, ...(params) })')
  expect(output.indexOf('const $luxoSelection_0')).toBeLessThan(output.indexOf('async function run'))
})
