import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { HmrContext, ViteDevServer } from 'vite'

const mocks = vi.hoisted(() => ({
  generateTypes: vi.fn(), readSchema: vi.fn(),
  identify: vi.fn<(file: string, code: string, calls: readonly { memberStart: number }[]) => Promise<ReadonlySet<number>>>(),
  close: vi.fn<() => Promise<void>>(), invalidate: vi.fn<(file: string, deleted: boolean) => Promise<void>>(),
}))

vi.mock('./codegen', () => ({ generateTypes: mocks.generateTypes }))
vi.mock('fs', () => ({ readFileSync: mocks.readSchema, existsSync: () => true }))
vi.mock('./call-identity', async () => {
  const { BuildOwner } = await vi.importActual<typeof import('./call-identity')>('./call-identity')
  return { BuildOwner, CallIdentity: class {
    identify = mocks.identify
    close = mocks.close
    invalidate = mocks.invalidate
  } }
})

import { generateSDK, luxo as createPlugin, type LuxoPluginOptions } from './index'
import { resolve } from 'node:path'
import { createServer } from 'node:http'
import { once } from 'node:events'
import { SourceMap, type SourceMapPayload } from 'node:module'
import { analyzeVerifiedCalls } from './analyzer'

function luxo(options: LuxoPluginOptions) {
  const plugin = createPlugin(options)
  const context = { addWatchFile: vi.fn() }
  return {
    ...plugin,
    buildStart: (plugin.buildStart as () => Promise<void>).bind(context),
    transform: (plugin.transform as (code: string, id: string) => Promise<unknown>).bind(context),
  }
}

beforeEach(() => {
  mocks.generateTypes.mockReset()
  mocks.readSchema.mockReset()
  mocks.identify.mockReset().mockImplementation(async (_file, _code, calls) => new Set(calls.map(call => call.memberStart)))
  mocks.close.mockReset().mockResolvedValue(undefined)
  mocks.invalidate.mockReset().mockResolvedValue(undefined)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('does not forward the introspection key through redirects', async () => {
  let receivedKey: string | string[] | undefined
  const target = createServer((request, response) => {
    receivedKey = request.headers['x-introspection-key']
    response.end(JSON.stringify({ models: {}, apis: {} }))
  })
  const source = createServer((_request, response) => {
    const address = target.address()
    if (!address || typeof address === 'string') throw new Error('missing target port')
    response.writeHead(302, { Location: `http://127.0.0.1:${address.port}/capture` })
    response.end()
  })
  try {
    target.listen(0, '127.0.0.1')
    await once(target, 'listening')
    source.listen(0, '127.0.0.1')
    await once(source, 'listening')
    const address = source.address()
    if (!address || typeof address === 'string') throw new Error('missing source port')
    let result: unknown
    try {
      await generateSDK({
        endpoint: `http://127.0.0.1:${address.port}/luvia`, introspectionKey: 'test-secret',
      })
    } catch (error) {
      result = error
    }
    expect(receivedKey).toBeUndefined()
    expect(result).toBeInstanceOf(AggregateError)
    expect(mocks.generateTypes).not.toHaveBeenCalled()
  } finally {
    source.closeAllConnections()
    target.closeAllConnections()
    await Promise.all([source, target].map(server => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve())
    })))
  }
})

describe('standalone SDK generation', () => {
  it('resolves schema and output relative to an explicit project root', async () => {
    const schema = { models: {}, apis: {} }
    mockSchemaFile(Buffer.from(JSON.stringify(schema)))
    await generateSDK({ schema: 'schema.json', outDir: 'sdk/src' }, '/workspace/cluster')
    expect(mocks.readSchema).toHaveBeenCalledWith('/workspace/cluster/schema.json')
    expect(mocks.generateTypes).toHaveBeenCalledWith(schema, '/workspace/cluster/sdk/src')
  })

  it('uses the working directory and the same default output as Vite', async () => {
    const schema = { models: {}, apis: {} }
    mockSchemaFile(Buffer.from(JSON.stringify(schema)))
    await generateSDK({ schema: 'schema.json' })
    expect(mocks.generateTypes).toHaveBeenCalledWith(schema, resolve('src/luxo'))
  })

  it('propagates schema and generator failures instead of silently succeeding', async () => {
    await expect(generateSDK()).rejects.toBeInstanceOf(AggregateError)
    expect(mocks.generateTypes).not.toHaveBeenCalled()
    mockSchemaFile(Buffer.from('{'))
    await expect(generateSDK({ schema: 'schema.json' })).rejects.toBeInstanceOf(AggregateError)
    expect(mocks.generateTypes).not.toHaveBeenCalled()
    mockSchemaFile(Buffer.from('{"models":{},"apis":{}}'))
    const failure = new Error('output not writable')
    mocks.generateTypes.mockRejectedValue(failure)
    await expect(generateSDK({ schema: 'schema.json' })).rejects.toBe(failure)
  })
})

describe('schema introspection', () => {
  it.each(['missing', 'network', 'http', 'json', 'file'])('stops the build when the schema is unavailable: %s', async failure => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.stubGlobal('fetch', vi.fn(async () => {
      if (failure === 'network') throw new Error('offline')
      return { ok: failure !== 'http', status: 401, json: async () => { throw new SyntaxError('invalid JSON') } }
    }))
    mocks.readSchema.mockImplementation(() => { throw new Error('missing file') })
    const options = failure === 'missing' ? {} : failure === 'file' ? { schema: 'schema.json' } : { endpoint: 'https://api.example.com/luvia' }
    const plugin = luxo(options)
    await expect((plugin.buildStart as () => Promise<void>)()).rejects.toThrow(/\[luxo\].*[Ss]chema/)
    expect(mocks.generateTypes).not.toHaveBeenCalled()
  })

  it('uses only an explicitly configured local fallback, relative to the Vite root', async () => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 503 }))
    const schema = { models: {}, apis: {} }
    mockSchemaFile(Buffer.from(JSON.stringify(schema)))
    const plugin = luxo({ endpoint: 'https://api.example.com/luvia', schema: 'schema.json' })
    ;(plugin.configResolved as (config: { root: string }) => void)({ root: '/workspace/app' })
    await (plugin.buildStart as () => Promise<void>)()
    expect(mocks.readSchema).toHaveBeenCalledWith('/workspace/app/schema.json')
    expect(mocks.generateTypes).toHaveBeenCalledWith(schema, '/workspace/app/src/luxo', { onWrite: expect.any(Function) })
    expect(warning).toHaveBeenCalled()
  })

  it('sends the introspection key only in the canonical header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ models: {}, apis: {} }))
    vi.stubGlobal('fetch', fetchMock)

    const plugin = luxo({
      endpoint: 'https://api.example.com/luvia',
      introspectionKey: 'secret-key',
    })
    await (plugin.buildStart as () => Promise<void>)()

    expect(fetchMock).toHaveBeenCalledWith('https://api.example.com/luvia?%24schema=', {
      headers: { 'X-Introspection-Key': 'secret-key' },
      redirect: 'error',
    })
    expect(mocks.generateTypes).toHaveBeenCalledOnce()
  })
})

describe('source transformation scope', () => {
  it.each(['mts', 'cts'])('transforms .%s source modules', async extension => {
    const schema = {
      models: { User: { name: 'User', fields: [{ id: 1, name: 'name', type: 'String' }] } },
      apis: { me: { id: 1, name: 'me', module: 'user', returnType: 'User' } },
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(schema)))
    const plugin = luxo({ endpoint: 'https://api.example.com/luvia' })
    await (plugin.buildStart as () => Promise<void>)()
    const transform = plugin.transform as (code: string, id: string) => Promise<{ code: string } | null>
    expect((await transform('async function view() { const user = await client.me(); console.log(user.name) }', `/workspace/view.${extension}?import`))?.code)
      .toContain("client.me({ $select: $luxoSelection0 })")
  })

  it('does not mistake a parent directory named luxo for the generated output', async () => {
    const schema = {
      models: {
        User: { name: 'User', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        me: { id: 1, name: 'me', module: 'user', returnType: 'User' },
      },
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json(schema)))
    const plugin = luxo({ endpoint: 'https://api.example.com/luvia', outDir: 'src/luxo' })
    ;(plugin.configResolved as (config: { root: string }) => void)({ root: '/workspace/web' })
    await (plugin.buildStart as () => Promise<void>)()
    expect(mocks.generateTypes).toHaveBeenCalledWith(schema, '/workspace/web/src/luxo')
    const transform = plugin.transform as (code: string, id: string) => Promise<{ code: string } | null>

    const result = await transform(
      'const member = await client.me(); consume(member)',
      '/workspace/golang/luxo/luxo-studio/web/src/hooks/use-auth.ts',
    )
    expect(result?.code).toContain("client.me({ $select: $luxoSelection0 })")
    expect(await transform('export const generated = true', '/workspace/web/src/luxo/client.ts')).toBeNull()
  })
})

const schemaWithText = {
  models: { User: { name: 'User', description: '中文😀\uFEFF\uFFFD', fields: [{ id: 1, name: 'id', type: 'Int' }] } },
  apis: { me: { id: 1, name: 'me', module: 'user', returnType: 'User' } },
}

function mockSchemaFile(bytes: Buffer): void {
  mocks.readSchema.mockImplementation((_path: string, encoding?: string) => encoding ? bytes.toString('utf8') : bytes)
}

function schemaInput(source: string, bytes: Buffer) {
  if (source === 'file') {
    mockSchemaFile(bytes)
    return luxo({ schema: 'schema.json' })
  }
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(Uint8Array.from(bytes), {
    headers: { 'Content-Type': 'application/json; charset=iso-8859-1' },
  })))
  return luxo({ endpoint: 'https://api.example.com/luvia' })
}

describe.each(['file', 'network'])('strict %s schema documents', source => {
  it.each([false, true])('preserves Unicode and one optional document BOM: %s', async bom => {
    const bytes = Buffer.from((bom ? '\uFEFF' : '') + JSON.stringify(schemaWithText))
    const plugin = schemaInput(source, bytes)
    await (plugin.buildStart as () => Promise<void>)()
    const options = source === 'file' ? [{ onWrite: expect.any(Function) }] : []
    expect(mocks.generateTypes).toHaveBeenCalledWith(schemaWithText, expect.any(String), ...options)
  })

  it.each([
    [0x80], [0xc0, 0xaf], [0xe2, 0x82], [0xed, 0xa0, 0x80], [0xf4, 0x90, 0x80, 0x80],
  ])('rejects invalid UTF-8 before generation: %j', async (...invalid) => {
    const text = JSON.stringify(schemaWithText).split('中文😀\uFEFF\uFFFD')
    const bytes = Buffer.concat([Buffer.from(text[0]!), Buffer.from(invalid), Buffer.from(text[1]!)])
    const plugin = schemaInput(source, bytes)
    await expect((plugin.buildStart as () => Promise<void>)()).rejects.toBeInstanceOf(AggregateError)
    expect(mocks.generateTypes).not.toHaveBeenCalled()
  })

  it.each(['null', '[]', 'true', '1', '"schema"', '{}', '{"models":[],"apis":{}}', '{"models":{},"apis":[]}', '{', '\uFEFF\uFEFF{"models":{},"apis":{}}'])
    ('rejects an invalid document/envelope: %s', async text => {
      const plugin = schemaInput(source, Buffer.from(text))
      await expect((plugin.buildStart as () => Promise<void>)()).rejects.toBeInstanceOf(AggregateError)
      expect(mocks.generateTypes).not.toHaveBeenCalled()
    })
})

it('retains the source path and original file decoding error', async () => {
  const plugin = schemaInput('file', Buffer.from([0xff]))
  const error: unknown = await (plugin.buildStart as () => Promise<void>)().catch((reason: unknown) => reason)
  expect(error).toBeInstanceOf(AggregateError)
  if (!(error instanceof AggregateError)) throw new Error('expected aggregate failure')
  const failure: unknown = error.errors[0]
  expect(failure).toBeInstanceOf(Error)
  if (!(failure instanceof Error)) throw new Error('expected file failure')
  expect(failure.message).toContain('schema.json')
  expect(failure.cause).toBeInstanceOf(TypeError)
  expect(mocks.generateTypes).not.toHaveBeenCalled()
})

it('retains response read errors without starting generation', async () => {
  const failure = new Error('schema response disconnected')
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(controller) { controller.error(failure) } }))))
  const plugin = luxo({ endpoint: 'https://api.example.com/luvia' })
  const error: unknown = await (plugin.buildStart as () => Promise<void>)().catch((reason: unknown) => reason)
  expect(error).toBeInstanceOf(AggregateError)
  if (!(error instanceof AggregateError)) throw new Error('expected aggregate failure')
  expect(error.errors).toEqual([failure])
  expect(mocks.generateTypes).not.toHaveBeenCalled()
})

it('warns before using an explicit file fallback after corrupt network bytes', async () => {
  const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
  schemaInput('network', Buffer.from([0xff]))
  mockSchemaFile(Buffer.from(JSON.stringify(schemaWithText)))
  const plugin = luxo({ endpoint: 'https://api.example.com/luvia', schema: 'schema.json' })
  await (plugin.buildStart as () => Promise<void>)()
  expect(warning).toHaveBeenCalledOnce()
  expect(mocks.generateTypes).toHaveBeenCalledWith(schemaWithText, expect.any(String), { onWrite: expect.any(Function) })
})

it('preserves endpoint query parameters and sets schema discovery exactly once', async () => {
  const fetchMock = vi.fn().mockResolvedValue(Response.json(schemaWithText))
  vi.stubGlobal('fetch', fetchMock)
  const plugin = luxo({ endpoint: 'https://api.example.com/luvia?tenant=a%20b&%24schema=old#ignored', introspectionKey: 'secret' })
  await (plugin.buildStart as () => Promise<void>)()
  const [address, options] = fetchMock.mock.calls[0]!
  const url = new URL(address)
  expect(url.searchParams.get('tenant')).toBe('a b')
  expect(url.searchParams.getAll('$schema')).toEqual([''])
  expect(url.hash).toBe('')
  expect(options).toEqual({ headers: { 'X-Introspection-Key': 'secret' }, redirect: 'error' })
})

it.each(['load', 'generate'])('does not reuse a stale schema after a failed rebuild: %s', async failure => {
  const fetchMock = vi.fn().mockResolvedValueOnce(Response.json(schemaWithText))
  vi.stubGlobal('fetch', fetchMock)
  const plugin = luxo({ endpoint: 'https://api.example.com/luvia' })
  const build = plugin.buildStart as () => Promise<void>
  const transform = plugin.transform as (code: string, id: string) => Promise<{ code: string } | null>
  const source = 'async function view() { const user = await client.me(); console.log(user.id) }'
  await build()
  expect((await transform(source, '/workspace/view.ts'))?.code).toContain("$select: $luxoSelection0")
  fetchMock.mockResolvedValueOnce(failure === 'load' ? new Response('{') : Response.json(schemaWithText))
  if (failure === 'generate') mocks.generateTypes.mockRejectedValueOnce(new Error('generation failed'))
  await expect(build()).rejects.toThrow()
  expect(await transform(source, '/workspace/view.ts')).toBeNull()
})

describe('semantic build ownership', () => {
  const code = 'async function view() { const user = await client.me(); console.log(user.id) }'

  async function preparePlugin() {
    mockSchemaFile(Buffer.from(JSON.stringify(schemaWithText)))
    const plugin = luxo({ schema: 'schema.json' })
    await (plugin.buildStart as () => Promise<void>)()
    return plugin
  }

  it.each([
    { label: 'LF', newline: '\n', header: '', spread: false },
    { label: 'CRLF', newline: '\r\n', header: '', spread: false },
    { label: 'shebang and directive', newline: '\n', header: '#!/usr/bin/env node\n"use strict";', spread: true },
    { label: 'prefix collision', newline: '\n', header: 'const $luxoSelection0 = 42', spread: true },
  ])('maps injected selection code back to the original source: $label', async ({ newline, header, spread }) => {
    const plugin = await preparePlugin()
    const sourceID = '/workspace/source-map.ts'
    const source = [
      ...(header ? [header] : []),
      `export async function view(${spread ? '...args: []' : ''}) {`,
      `  const first = await client.me(${spread ? '...args' : ''}); const second = await client.me()`,
      '  console.log(first.id, second.name)',
      '  /* 中文 \u{1D11E} */ throw new Error("源码定位")',
      '}',
    ].join(newline)
    const transform = plugin.transform as (code: string, id: string) => Promise<{
      code: string; map: SourceMapPayload | null
    } | null>
    const result = await transform(source, `${sourceID}?import`)
    expect(result?.code).toContain('$select: $luxoSelection')
    const legacy = await analyzeVerifiedCalls(source, sourceID, schemaWithText,
      async calls => new Set(calls.map(call => call.memberStart)))
    expect(result?.code).toBe(legacy)
    if (!result?.map) throw new Error('missing source map after selection injection')
    const lines = result.code.split('\n')
    const generatedLine = lines.findIndex(line => line.includes('throw new Error'))
    const entry = new SourceMap(result.map).findEntry(generatedLine, lines[generatedLine]!.indexOf('throw'))
    const original = source.split(/\r?\n/)
    const originalLine = original.findIndex(line => line.includes('throw new Error'))
    expect(entry).toMatchObject({ originalSource: sourceID, originalLine, originalColumn: original[originalLine]!.indexOf('throw') })
    expect(result.map.sourcesContent).toContain(source)
    await (plugin.closeBundle as () => Promise<void>)()
  })

  it('does not invoke the semantic compiler for plain modules or explicit selections', async () => {
    const plugin = await preparePlugin()
    const transform = plugin.transform as (code: string, id: string) => Promise<unknown>
    expect(await transform('export const value = 1', '/workspace/plain.ts')).toBeNull()
    expect(await transform(code.replace('client.me()', "client.me({ $select: 'id' })"), '/workspace/manual.ts')).toBeNull()
    expect(mocks.identify).not.toHaveBeenCalled()
  })

  it('propagates identity failures instead of emitting a name-based selection', async () => {
    const plugin = await preparePlugin()
    mocks.identify.mockRejectedValueOnce(new Error('native compiler unavailable'))
    const transform = plugin.transform as (code: string, id: string) => Promise<unknown>
    await expect(transform(code, '/workspace/view.ts')).rejects.toThrow('native compiler unavailable')
  })

  it('rejects an analysis result after its build owner is retired', async () => {
    const plugin = await preparePlugin()
    let release!: (calls: ReadonlySet<number>) => void
    mocks.identify.mockImplementationOnce(async () => await new Promise(resolve => { release = resolve }))
    const transform = plugin.transform as (code: string, id: string) => Promise<unknown>
    const pending = transform(code, '/workspace/view.ts')
    const closing = (plugin.closeBundle as () => Promise<void>)()
    release(new Set(mocks.identify.mock.calls[0]![2].map(call => call.memberStart)))
    await expect(pending).rejects.toThrow('Schema changed during selection analysis')
    await closing
  })

  it('serializes overlapping builds before generating or publishing their schemas', async () => {
    let release!: (response: Response) => void
    const first = { models: {}, apis: {} }
    const fetchMock = vi.fn().mockImplementationOnce(async () => await new Promise<Response>(resolve => { release = resolve }))
      .mockResolvedValueOnce(Response.json(schemaWithText))
    vi.stubGlobal('fetch', fetchMock)
    const plugin = luxo({ endpoint: 'https://api.example.com/luvia' })
    const build = plugin.buildStart as () => Promise<void>
    const pending = [build(), build()]
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled())
    release(Response.json(first))
    await Promise.all(pending)
    expect(mocks.generateTypes.mock.calls.map(args => args[0])).toEqual([first, schemaWithText])
    await (plugin.closeBundle as () => Promise<void>)()
  })

  it('reads a changed local schema in build order and generates each revision once', async () => {
    let release!: () => void
    const first = { models: {}, apis: {} }
    mockSchemaFile(Buffer.from(JSON.stringify(first)))
    mocks.generateTypes.mockImplementationOnce(async () => await new Promise<void>(resolve => { release = resolve }))
    const plugin = luxo({ schema: 'schema.json' })
    const pending = plugin.buildStart()
    await vi.waitFor(() => expect(mocks.generateTypes).toHaveBeenCalledOnce())
    const rebuild = plugin.buildStart()
    const duplicate = plugin.buildStart()
    mockSchemaFile(Buffer.from(JSON.stringify(schemaWithText)))
    release()
    await Promise.all([pending, rebuild, duplicate])
    expect(mocks.generateTypes.mock.calls.map(args => args[0])).toEqual([first, schemaWithText])
    await (plugin.closeBundle as () => Promise<void>)()
  })

  it('keeps a failed close owned for retry and retires its schema', async () => {
    const plugin = await preparePlugin()
    mocks.close.mockRejectedValueOnce(new Error('compiler cleanup failed'))
    await expect((plugin.closeBundle as () => Promise<void>)()).rejects.toThrow('compiler cleanup failed')
    const transform = plugin.transform as (code: string, id: string) => Promise<unknown>
    expect(await transform(code, '/workspace/view.ts')).toBeNull()
    await (plugin.closeBundle as () => Promise<void>)()
    expect(mocks.close).toHaveBeenCalledTimes(2)
  })

  it('forwards deletion and closes the compiler when a build fails', async () => {
    const plugin = await preparePlugin()
    await (plugin.watchChange as (file: string, change: { event: 'delete' }) => Promise<void>)('/workspace/deleted.ts', { event: 'delete' })
    expect(mocks.invalidate).toHaveBeenCalledWith('/workspace/deleted.ts', true)
    await (plugin.buildEnd as (error: Error) => Promise<void>)(new Error('build failed'))
    expect(mocks.close).toHaveBeenCalledOnce()
    await (plugin.closeBundle as () => Promise<void>)()
    expect(mocks.close).toHaveBeenCalledOnce()
  })

  it('handles candidates absent from the Vite graph and forgets deleted callers', async () => {
    const plugin = await preparePlugin()
    const getModulesByFile = vi.fn().mockReturnValue(undefined)
    const server = {
      watcher: { add: vi.fn() }, moduleGraph: { getModulesByFile, invalidateModule: vi.fn() },
    } as unknown as ViteDevServer
    ;(plugin.configureServer as (server: ViteDevServer) => void)(server)
    await (plugin.transform as (code: string, file: string) => Promise<unknown>)(code, '/workspace/view.ts')
    const update = plugin.watchChange as (file: string, change: { event: 'delete' | 'update' }) => Promise<void>
    await update('/workspace/helper.d.ts', { event: 'update' })
    expect(getModulesByFile).toHaveBeenCalledWith('/workspace/view.ts')
    const hotUpdate = plugin.handleHotUpdate as (context: HmrContext) => Promise<unknown[]>
    await expect(hotUpdate({
      file: '/workspace/helper.d.ts', modules: [], server, timestamp: 0, read: async () => '',
    })).resolves.toEqual([])
    getModulesByFile.mockClear()
    await update('/workspace/view.ts', { event: 'delete' })
    expect(getModulesByFile).not.toHaveBeenCalled()
    await (plugin.closeBundle as () => Promise<void>)()
  })
})
