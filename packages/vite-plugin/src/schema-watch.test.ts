import fs, { type Stats, type StatWatcher } from 'node:fs'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
import { basename, dirname, join, resolve } from 'node:path'
import { build, createServer, type InlineConfig, type ViteDevServer } from 'vite'
import { expect, it, vi } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'
import { luxo, type LuxoPluginOptions } from './index'

const packageRoot = resolve(import.meta.dirname, '..')
const temporaryRoot = resolve(packageRoot, '../../.tmp')

const originalSchema: LuxoSchema = {
  models: { User: { name: 'User', fields: [
    { id: 1, name: 'id', type: 'Int' },
    { id: 2, name: 'name', type: 'String' },
  ] } },
  apis: {},
}
const updatedSchema: LuxoSchema = { ...originalSchema, apis: {
  me: { id: 1, name: 'me', module: 'users', returnType: 'User' },
} }

function project(options: LuxoPluginOptions = {}) {
  mkdirSync(temporaryRoot, { recursive: true })
  const directory = mkdtempSync(join(temporaryRoot, 'vite-schema-watch-'))
  const root = join(directory, 'app')
  mkdirSync(root)
  symlinkSync(resolve(packageRoot, 'node_modules'), join(root, 'node_modules'), 'junction')
  const schema = join(directory, 'schema.json')
  const writeSchema = (text: string) => writeFileSync(schema, text)
  const save = (document: LuxoSchema) => writeSchema(JSON.stringify(document))
  save(originalSchema)
  writeFileSync(join(root, 'tsconfig.json'), JSON.stringify({ compilerOptions: {
    module: 'esnext', moduleResolution: 'bundler', target: 'esnext', strict: true,
  } }))
  writeFileSync(join(root, 'entry.ts'), `import type { LuxoClient } from './luxo/client'
if (import.meta.hot) import.meta.hot.accept()
export async function view(client: LuxoClient) { return (await client.me()).name }
`)
  writeFileSync(join(root, 'plain.ts'), 'export const value = 42')
  writeFileSync(join(root, 'runtime.ts'), `import { LuxoClient } from './luxo/client'
if (import.meta.hot) import.meta.hot.accept()
export const clientType = LuxoClient
`)
  const config: InlineConfig = {
    root, configFile: false, logLevel: 'silent', cacheDir: join(directory, 'vite-cache'),
    plugins: [luxo({ schema: '../schema.json', outDir: 'luxo', ...options })],
    optimizeDeps: { noDiscovery: true, include: [] },
  }
  const clientFile = join(root, 'luxo/client.ts')
  return {
    config, root, schema, save, clientFile, writeSchema,
    client: () => readFileSync(clientFile, 'utf8'),
    clean: () => rmSync(directory, { recursive: true, force: true }),
  }
}

const polling = { interval: 20, timeout: 6000 }
const watchOptions = {
  usePolling: true, interval: 20, atomic: false,
  awaitWriteFinish: { stabilityThreshold: 80, pollInterval: 20 },
}
const devOptions: InlineConfig['server'] = {
  middlewareMode: true, ws: false, watch: watchOptions,
}

async function waitForSchemaWatch(server: ViteDevServer, schema: string) {
  await vi.waitFor(() => expect(server.watcher.getWatched()[dirname(schema)]).toContain(basename(schema)), polling)
  // A registered path can precede its first stat sample. Wait for a real no-op edit event.
  const bytes = readFileSync(schema)
  let observed = false
  const change = (file: string) => { if (file === schema) observed = true }
  server.watcher.on('change', change)
  try {
    await vi.waitFor(() => {
      if (!observed) writeFileSync(schema, bytes)
      expect(observed).toBe(true)
    }, { ...polling, interval: 200 })
  } finally {
    server.watcher.off('change', change)
  }
}

it('reloads an external schema and retransforms a previously unknown SDK call on a running Vite server', async () => {
  const fixture = project()
  const server = await createServer({ ...fixture.config, server: devOptions })
  try {
    const app = () => server.transformRequest('/entry.ts')
    const hot = vi.spyOn(server.environments.client.hot, 'send')
    const reload = vi.spyOn(server.ws, 'send')
    const errors = vi.spyOn(server.config.logger, 'error')
    expect((await app())?.code).not.toContain('$select')
    await server.transformRequest('/runtime.ts')
    const client = () => server.transformRequest('/luxo/client.ts')
    expect((await client())?.code).not.toContain('  me(')
    const plain = await server.transformRequest('/plain.ts')
    expect(fixture.client()).not.toContain('  me<')
    await waitForSchemaWatch(server, fixture.schema)
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(fixture.client()).toContain('  me<'), polling)
    expect((await app())?.code).toContain('$select')
    expect((await client())?.code).toContain('  me(')
    await vi.waitFor(() => expect(hot).toHaveBeenCalledWith(expect.objectContaining({
      type: 'update', updates: expect.arrayContaining([expect.objectContaining({ path: '/entry.ts' })]),
    })), polling)
    expect(await server.transformRequest('/plain.ts')).toBe(plain)
    const before = statSync(fixture.clientFile).mtimeMs
    const changes = vi.fn()
    server.watcher.on('change', file => { if (file === fixture.schema) changes() })
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(changes).toHaveBeenCalledOnce(), polling)
    fixture.writeSchema('{')
    await vi.waitFor(() => expect(errors).toHaveBeenCalledWith(expect.stringContaining('SyntaxError')), polling)
    expect(statSync(fixture.clientFile).mtimeMs).toBe(before)
    expect((await app())?.code).toContain('$select')
    fixture.save({ ...updatedSchema, apis: { me: { ...updatedSchema.apis.me!, id: 0 } } })
    await vi.waitFor(() => expect(errors).toHaveBeenCalledWith(expect.stringContaining('ID must be an integer')), polling)
    expect(statSync(fixture.clientFile).mtimeMs).toBe(before)
    expect((await app())?.code).toContain('$select')
    expect(reload).not.toHaveBeenCalledWith(expect.objectContaining({ type: 'full-reload' }))
    rmSync(fixture.schema)
    await vi.waitFor(() => expect(errors).toHaveBeenCalledWith(expect.stringContaining('ENOENT')), polling)
    fixture.save(originalSchema)
    await vi.waitFor(() => expect(fixture.client()).not.toContain('  me<'), polling)
    expect((await app())?.code).not.toContain('$select')
    const payloads: unknown[] = reload.mock.calls.map(([payload]) => payload)
    const reloads = payloads.filter(payload =>
      payload !== null && typeof payload === 'object' && 'type' in payload && payload.type === 'full-reload')
    expect(reloads).toEqual([{ type: 'full-reload', path: '*' }])
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(fixture.client()).toContain('  me<'), polling)
    expect((await app())?.code).toContain('$select')
  } finally {
    await server.close()
    fixture.clean()
  }
}, 22000)

it.each(['endpoint', 'fallback'] as const)('keeps %s source priority when the local schema is edited', async source => {
  const fixture = project({ endpoint: 'https://schema.example.invalid/' })
  const fetch = vi.fn().mockImplementation(async () =>
    source === 'endpoint' ? Response.json(updatedSchema) : Response.json({}, { status: 503 }))
  vi.stubGlobal('fetch', fetch)
  const server = await createServer({ ...fixture.config, server: { ...devOptions, hmr: false } })
  try {
    const before = fixture.client()
    expect(before.includes('  me<')).toBe(source === 'endpoint')
    const events = vi.fn()
    server.watcher.on('change', events)
    await waitForSchemaWatch(server, fixture.schema)
    fixture.save({ ...updatedSchema, apis: { who: { ...updatedSchema.apis.me!, name: 'who' } } })
    await vi.waitFor(() => expect(events.mock.calls.some(([file]) => file === fixture.schema)).toBe(true), polling)
    if (source === 'fallback') {
      await vi.waitFor(() => expect(fixture.client()).toContain('  who<'), polling)
    } else {
      expect(fixture.client()).toBe(before)
    }
    expect(fetch).toHaveBeenCalledTimes(1)
  } finally {
    vi.unstubAllGlobals()
    await server.close()
    fixture.clean()
  }
}, 15000)

it('keeps earlier outputs when a later destination is blocked and recovers on the next schema change', async () => {
  const fixture = project()
  const server = await createServer({ ...fixture.config, server: { ...devOptions, hmr: false } })
  try {
    const errors = vi.spyOn(server.config.logger, 'error')
    const previousClient = fixture.client()
    const lastOutput = join(fixture.root, 'luxo/queries.ts')
    rmSync(lastOutput)
    mkdirSync(lastOutput)
    await waitForSchemaWatch(server, fixture.schema)
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(errors).toHaveBeenCalledWith(expect.stringContaining('queries.ts')), polling)
    expect(statSync(lastOutput).isDirectory()).toBe(true)
    expect(fixture.client()).toBe(previousClient)
    rmSync(lastOutput, { recursive: true })
    fixture.writeSchema(JSON.stringify(updatedSchema, undefined, 2))
    await vi.waitFor(() => expect(statSync(lastOutput).isFile()).toBe(true), polling)
    expect(fixture.client()).toContain('  me<')
    expect((await server.transformRequest('/entry.ts'))?.code).toContain('$select')
  } finally {
    await server.close()
    fixture.clean()
  }
}, 15000)

function hasSchemaReadError(errors: readonly unknown[], matches: (cause: Error) => boolean): boolean {
  return errors.some(error => error instanceof AggregateError && error.errors.some((cause: unknown) =>
    cause instanceof Error && cause.cause instanceof Error && matches(cause.cause)))
}

async function waitForSchemaPolling(file: string, registered: () => StatWatcher | undefined) {
  const mode = statSync(file).mode & 0o777
  let subscription: StatWatcher | undefined
  let sampled = false
  const observe = (current: Stats, previous: Stats) => {
    if (current.mode !== previous.mode && current.mtimeMs === previous.mtimeMs && current.size === previous.size) sampled = true
  }
  try {
    await vi.waitFor(() => {
      const latest = registered()
      expect(latest).toBeDefined()
      if (subscription !== latest) {
        subscription?.off('change', observe)
        subscription = latest
        subscription?.on('change', observe)
        sampled = false
      }
      // A permission-only change witnesses a real poll while leaving the schema revision intact.
      if (!sampled) fs.chmodSync(file, statSync(file).mode & 0o200 ? mode & ~0o222 : mode | 0o200)
      expect(sampled).toBe(true)
    }, { ...polling, interval: 100 })
  } finally {
    subscription?.off('change', observe)
    fs.chmodSync(file, mode)
  }
}

it('rebuilds generated files and cached app transforms for a schema-only change in Vite build watch', async () => {
  const fixture = project()
  const registration = vi.spyOn(fs, 'watchFile')
  let watchBuild: Awaited<ReturnType<typeof build>> | undefined
  try {
    const watcher = watchBuild = await build({ ...fixture.config, build: {
      watch: { chokidar: watchOptions }, minify: false, lib: { entry: 'entry.ts', formats: ['es'], fileName: 'app' },
    } })
    if (!('on' in watcher)) throw new Error('expected Vite build watcher')
    let completed = 0
    let ended = 0
    let previousEnd = 0
    const errors: unknown[] = []
    watcher.on('event', event => {
      if (event.code === 'BUNDLE_END') completed++
      if (event.code === 'ERROR') errors.push(event.error)
      if (event.code === 'END') ended++
    })
    const nextSave = async () => {
      await vi.waitFor(() => expect(ended).toBeGreaterThan(previousEnd), polling)
      previousEnd = ended
      // Rollup reports END before its asynchronous file watch can establish the next baseline.
      await waitForSchemaPolling(fixture.schema, () => {
        const { calls, results } = registration.mock
        for (let index = calls.length - 1; index >= 0; index--) {
          if (calls[index]?.[0] !== fixture.schema) continue
          const result = results[index]
          return result?.type === 'return' ? result.value : undefined
        }
      })
    }
    const bundle = () => readFileSync(join(fixture.root, 'dist/app.mjs'), 'utf8')
    await vi.waitFor(() => expect(completed).toBeGreaterThan(0), polling)
    expect(bundle()).not.toContain('$select')
    const before = completed
    await nextSave()
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(completed).toBeGreaterThan(before), polling)
    expect(errors).toEqual([])
    expect(fixture.client()).toContain('  me<')
    expect(bundle()).toContain('$select')
    const rendered = completed
    await nextSave()
    rmSync(fixture.clientFile)
    expect(existsSync(fixture.clientFile)).toBe(false)
    fixture.save(updatedSchema)
    await vi.waitFor(() => expect(completed).toBeGreaterThan(rendered), polling)
    expect(fixture.client()).toContain('  me<')
    expect(bundle()).toContain('$select')
    await nextSave()
    fixture.writeSchema('{')
    await vi.waitFor(() => expect(hasSchemaReadError(errors, cause => cause instanceof SyntaxError)).toBe(true), polling)
    expect(fixture.client()).toContain('  me<')
    expect(bundle()).toContain('$select')
    await nextSave()
    const failed = completed
    fixture.save(originalSchema)
    await vi.waitFor(() => expect(completed).toBeGreaterThan(failed), polling)
    expect(fixture.client()).not.toContain('  me<')
    expect(bundle()).not.toContain('$select')
    const beforeReplace = completed
    await nextSave()
    const replacement = `${fixture.schema}.next`
    writeFileSync(replacement, JSON.stringify(updatedSchema))
    renameSync(replacement, fixture.schema)
    await vi.waitFor(() => expect(completed).toBeGreaterThan(beforeReplace), polling)
    expect(fixture.client()).toContain('  me<')
    expect(bundle()).toContain('$select')
    const previousClient = fixture.client()
    const previousBundle = bundle()
    await nextSave()
    const beforeDelete = ended
    rmSync(fixture.schema)
    await vi.waitFor(() => expect(hasSchemaReadError(errors, cause => cause.message.includes('ENOENT'))).toBe(true), polling)
    await vi.waitFor(() => expect(ended).toBeGreaterThan(beforeDelete), polling)
    expect(fixture.client()).toBe(previousClient)
    expect(bundle()).toBe(previousBundle)
  } finally {
    try {
      if (watchBuild && 'on' in watchBuild) await watchBuild.close()
    } finally {
      registration.mockRestore()
      fixture.clean()
    }
  }
}, 30000)
