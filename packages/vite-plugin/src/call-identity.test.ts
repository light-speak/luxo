import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'
import type { Plugin } from 'vite'
import { createServer } from 'vite'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join, resolve, sep } from 'node:path'
import { luxo } from './index'
import { CallIdentity } from './call-identity'
import { analyzeVerifiedCalls } from './analyzer'

const repository = resolve(import.meta.dirname, '../../..')
const temporary = join(repository, '.tmp')
const fixtures: { directory: string; plugin: Plugin }[] = []
const schema: LuxoSchema = {
  models: { User: { name: 'User', fields: [
    { id: 1, name: 'id', type: 'Int' }, { id: 2, name: 'name', type: 'String' },
    { id: 3, name: 'score', type: 'Float' },
  ] } },
  apis: { getUser: { id: 1, name: 'getUser', module: 'users', returnType: 'User',
    params: [{ id: 1, name: 'id', type: 'Int' }],
  }, getClient: { id: 2, name: 'getClient', module: 'users', returnType: 'User' },
  allUsers: { id: 3, name: 'allUsers', module: 'users', returnType: 'User', returnList: true } },
}

afterEach(async () => {
  for (const fixture of fixtures.splice(0)) {
    if (typeof fixture.plugin.closeBundle === 'function') {
      await (fixture.plugin.closeBundle as () => Promise<void>)()
    }
    if (fixture.directory.startsWith(temporary + sep)) rmSync(fixture.directory, { recursive: true })
  }
})

async function prepare(configured: boolean) {
  mkdirSync(temporary, { recursive: true })
  const directory = mkdtempSync(join(temporary, 'vite-call-identity-'))
  mkdirSync(join(directory, 'src'))
  mkdirSync(join(directory, 'node_modules/@luxojs'), { recursive: true })
  symlinkSync(join(repository, 'packages/client'), join(directory, 'node_modules/@luxojs/client'), 'dir')
  writeFileSync(join(directory, 'schema.json'), JSON.stringify(schema))
  if (configured) writeFileSync(join(directory, 'tsconfig.json'), JSON.stringify({
    compilerOptions: { target: 'esnext', module: 'esnext', moduleResolution: 'bundler', strict: true, skipLibCheck: true },
    include: ['src/**/*'],
  }))
  const plugin = luxo({ schema: 'schema.json', outDir: 'src/generated' })
  fixtures.push({ directory, plugin })
  ;(plugin.configResolved as (config: { root: string }) => void)({ root: directory })
  await (plugin.buildStart as () => Promise<void>)()
  return { directory, plugin }
}

async function transform(plugin: Plugin, code: string, file: string) {
  return await (plugin.transform as (code: string, file: string) => Promise<{ code: string } | null>)(code, file)
}

const source = `import { LuxoClient as SDK } from './generated/client'
const local = { async getUser(params: { id: bigint }) { return { id: params.id, name: 'local' } } }
const client = new SDK(null as never)
const factory = () => new SDK(null as never)
async function view() {
 const user = await client.getUser({ id: 1n })
 const ordinary = await local.getUser({ id: 2n })
 const other = await factory().getUser({ id: 3n })
 console.log(user.name, ordinary.name, other.id)
}
void view
`

describe.each([true, false])('SDK call identity with configured project=%s', configured => {
  it.each(['ts', 'mts', 'cts', 'tsx'])('preserves ordinary calls and distinguishes actual SDK calls in .%s', async extension => {
    const { directory, plugin } = await prepare(configured)
    const file = join(directory, `src/view.${extension}`)
    writeFileSync(file, source)
    const result = await transform(plugin, source, file + '?import')
    expect(result?.code).toContain('local.getUser({ id: 2n })')
    expect(result?.code).toContain('client.getUser({ $select:')
    expect(result?.code).toContain('factory().getUser({ $select:')
    expect(readFileSync(file, 'utf8')).toBe(source)
    expect(result?.code.match(/const \$luxoSelection\d+ =/g)).toHaveLength(2)
  })

  it('retains both filter and map fields only on the generated SDK call', async () => {
    const { directory, plugin } = await prepare(configured)
    const file = join(directory, 'src/chain.ts')
    const code = `import { LuxoClient } from './generated/client'
const client = new LuxoClient(null as never)
const local = { async allUsers() { return [{ name: 'local', score: 1 }] } }
async function view() {
 const users = await client.allUsers()
 const ordinary = await local.allUsers()
 return [users.filter(user => user.score > 0).map(user => user.name), ordinary.map(user => user.name)]
}
void view`
    writeFileSync(file, code)
    const result = await transform(plugin, code, file)
    expect(result?.code).toContain('Prepare("score,name")')
    expect(result?.code).toContain('client.allUsers({ $select:')
    expect(result?.code).toContain('local.allUsers()')
    expect(result?.code.match(/const \$luxoSelection\d+ =/g)).toHaveLength(1)
  })

  it('updates the receiver identity from in-memory source without changing the original file', async () => {
    const { directory, plugin } = await prepare(configured)
    const file = join(directory, 'src/view.ts')
    writeFileSync(file, source)
    expect((await transform(plugin, source, file))?.code).toContain('client.getUser({ $select:')
    const changed = '// 中文😀\n' + source.replace('const client = new SDK(null as never)', 'const client = local')
    const result = await transform(plugin, changed, file)
    expect(result?.code).toContain('client.getUser({ id: 1n })')
    expect(result?.code).toContain('local.getUser({ id: 2n })')
    expect(result?.code).toContain('factory().getUser({ $select:')
    expect(readFileSync(file, 'utf8')).toBe(source)
  })

  it('tracks find result fields only on the generated SDK receiver', async () => {
    const { directory, plugin } = await prepare(configured)
    const file = join(directory, 'src/find.ts')
    const code = `import { LuxoClient } from './generated/client'
const client = new LuxoClient(null as never)
const local = { async allUsers() { return [{ score: 1, name: 'local' }] } }
async function view() {
 const users = await client.allUsers()
 const ordinary = await local.allUsers()
 return [users.find(user => user.score > 0)?.name, ordinary.find(user => user.score > 0)?.name]
}
void view`
    writeFileSync(file, code)
    const result = await transform(plugin, code, file)
    expect(result?.code).toContain('Prepare("name,score")')
    expect(result?.code).toContain('client.allUsers({ $select:')
    expect(result?.code).toContain('local.allUsers()')
    expect(result?.code.match(/const \$luxoSelection\d+ =/g)).toHaveLength(1)
    expect(readFileSync(file, 'utf8')).toBe(code)
  })

  it('keeps nested ordinary factories distinct from SDK calls sharing the same source start', async () => {
    const { directory, plugin } = await prepare(configured)
    const file = join(directory, 'src/view.ts')
    const code = "import { LuxoClient } from './generated/client'; const provider = { getClient() { return new LuxoClient(null as never) } }; async function view() { const user = await provider.getClient().getUser({ id: 1n }); console.log(user.name) }"
    writeFileSync(file, code)
    const result = await transform(plugin, code, file)
    expect(result?.code).toContain('provider.getClient().getUser({ $select:')
    expect(result?.code).not.toContain('provider.getClient({')
  })

  it('invalidates imported factory types after the source dependency changes', async () => {
    const { directory, plugin } = await prepare(configured)
    const helper = join(directory, 'src/helper.ts')
    writeFileSync(helper, "import { LuxoClient } from './generated/client'; export const factory = () => new LuxoClient(null as never)")
    const file = join(directory, 'src/view.ts')
    const code = "import { factory } from './helper'; async function run() { const user = await factory().getUser({ id: 1n }); console.log(user.name) }"
    writeFileSync(file, code)
    expect((await transform(plugin, code, file))?.code).toContain('$select:')
    writeFileSync(helper, "export const factory = () => ({ async getUser(params: {id: bigint}) { return {id: params.id, name: 'ordinary'} } })")
    if (typeof plugin.watchChange === 'function') {
      await (plugin.watchChange as (file: string, change: { event: 'update' }) => Promise<void>)(helper, { event: 'update' })
    }
    expect(await transform(plugin, code, file)).toBeNull()
  })
})

it('drains accepted native queries in order before closing and rejects later work', async () => {
  const { directory } = await prepare(true)
  const first = join(directory, 'src/first.ts')
  const second = join(directory, 'src/second.ts')
  const changed = source.replace('const client = new SDK(null as never)', 'const client = local')
  writeFileSync(first, source)
  writeFileSync(second, changed)
  const owner = new CallIdentity(directory, join(directory, 'src/generated/client.ts'))
  try {
    const one = analyzeVerifiedCalls(source, first, schema, calls => owner.identify(first, source, calls))
    const two = analyzeVerifiedCalls(changed, second, schema, calls => owner.identify(second, changed, calls))
    const closing = owner.close()
    expect(await one).toContain('client.getUser({ $select:')
    expect(await two).toContain('client.getUser({ id: 1n })')
    await closing
    await expect(owner.identify(first, source, [])).rejects.toThrow('selection compiler is closed')
  } finally {
    await owner.close()
  }
})

it.each([false, true])('invalidates resolved receiver types through a real Vite watcher update, type-only=%s', async typeOnly => {
  const { directory, plugin } = await prepare(true)
  const helperName = typeOnly ? 'helper.d.ts' : 'helper.ts'
  const helper = join(directory, 'src', helperName)
  const file = join(directory, 'src/view.ts')
  writeFileSync(helper, typeOnly
    ? "import { LuxoClient } from './generated/client'; export type Provider = () => LuxoClient"
    : "import { LuxoClient } from './generated/client'; export const factory = () => new LuxoClient(null as never)")
  const imports = typeOnly ? "import type { Provider } from './helper'; declare const factory: Provider;" : "import { factory } from './helper';"
  writeFileSync(file, imports + "export async function view() { const user = await factory().getUser({ id: 1n }); return user.name }")
  let updated: (() => void) | undefined
  const server = await createServer({
    root: directory, configFile: false, logLevel: 'silent', appType: 'custom',
    server: { host: '127.0.0.1', port: 0, watch: { usePolling: true, interval: 20 } },
    plugins: [plugin, { name: 'observe-luxo-update', handleHotUpdate(context) {
      if (context.file === helper) updated?.()
    } }],
  })
  try {
    await server.listen()
    await vi.waitFor(() => expect(server.watcher.getWatched()[join(directory, 'src')]).toContain(helperName))
    expect((await server.transformRequest('/src/view.ts'))?.code).toContain('$select:')
    if (!typeOnly) await server.transformRequest('/src/helper.ts')
    const watched = new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error('Vite did not observe the helper change')), 3000)
      updated = () => { clearTimeout(timeout); resolve() }
    })
    writeFileSync(helper, typeOnly
      ? "export type Provider = () => { getUser(params: {id: bigint}): Promise<{id: bigint, name: string}> }"
      : "export const factory = () => ({ async getUser(params: {id: bigint}) { return {id: params.id, name: 'ordinary'} } })")
    await watched
    await vi.waitFor(async () => {
      expect((await server.transformRequest('/src/view.ts'))?.code).not.toContain('$select:')
    })
  } finally {
    await server.close()
  }
})
