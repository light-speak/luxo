import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ generateTypes: vi.fn(), readSchema: vi.fn() }))

vi.mock('./codegen', () => ({ generateTypes: mocks.generateTypes }))
vi.mock('fs', () => ({ readFileSync: mocks.readSchema }))

import { generateSDK, luxo } from './index'
import { resolve } from 'node:path'

beforeEach(() => {
  mocks.generateTypes.mockReset()
  mocks.readSchema.mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
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
    expect(mocks.generateTypes).toHaveBeenCalledWith(schema, '/workspace/app/src/luxo')
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
    const transform = plugin.transform as (code: string, id: string) => { code: string } | null
    expect(transform('async function view() { const user = await client.me(); console.log(user.name) }', `/workspace/view.${extension}?import`)?.code)
      .toContain("client.me({ $select: 'name' })")
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
    const transform = plugin.transform as (code: string, id: string) => { code: string } | null

    const result = transform(
      'const member = await client.me(); consume(member)',
      '/workspace/golang/luxo/luxo-studio/web/src/hooks/use-auth.ts',
    )
    expect(result?.code).toContain("client.me({ $select: 'id' })")
    expect(transform('export const generated = true', '/workspace/web/src/luxo/client.ts')).toBeNull()
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
    expect(mocks.generateTypes).toHaveBeenCalledWith(schemaWithText, expect.any(String))
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
  expect(mocks.generateTypes).toHaveBeenCalledWith(schemaWithText, expect.any(String))
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
  expect(options).toEqual({ headers: { 'X-Introspection-Key': 'secret' } })
})

it.each(['load', 'generate'])('does not reuse a stale schema after a failed rebuild: %s', async failure => {
  const fetchMock = vi.fn().mockResolvedValueOnce(Response.json(schemaWithText))
  vi.stubGlobal('fetch', fetchMock)
  const plugin = luxo({ endpoint: 'https://api.example.com/luvia' })
  const build = plugin.buildStart as () => Promise<void>
  const transform = plugin.transform as (code: string, id: string) => { code: string } | null
  const source = 'async function view() { const user = await client.me(); console.log(user.id) }'
  await build()
  expect(transform(source, '/workspace/view.ts')?.code).toContain("$select: 'id'")
  fetchMock.mockResolvedValueOnce(failure === 'load' ? new Response('{') : Response.json(schemaWithText))
  if (failure === 'generate') mocks.generateTypes.mockRejectedValueOnce(new Error('generation failed'))
  await expect(build()).rejects.toThrow()
  expect(transform(source, '/workspace/view.ts')).toBeNull()
})
