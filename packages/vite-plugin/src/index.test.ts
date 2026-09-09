import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ generateTypes: vi.fn() }))

vi.mock('./codegen', () => ({ generateTypes: mocks.generateTypes }))

import { luxo } from './index'

beforeEach(() => {
  mocks.generateTypes.mockReset()
  vi.unstubAllGlobals()
})

describe('schema introspection', () => {
  it('sends the introspection key only in the canonical header', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ models: {}, apis: {} }),
    })
    vi.stubGlobal('fetch', fetchMock)

    const plugin = luxo({
      endpoint: 'https://api.example.com/luvia',
      introspectionKey: 'secret-key',
    })
    await (plugin.buildStart as () => Promise<void>)()

    expect(fetchMock).toHaveBeenCalledWith('https://api.example.com/luvia?$schema', {
      headers: { 'X-Introspection-Key': 'secret-key' },
    })
    expect(mocks.generateTypes).toHaveBeenCalledOnce()
  })
})

describe('source transformation scope', () => {
  it('does not mistake a parent directory named luxo for the generated output', async () => {
    const schema = {
      models: {
        User: { name: 'User', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        me: { id: 1, name: 'me', module: 'user', returnType: 'User' },
      },
    }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => schema }))
    const plugin = luxo({ endpoint: 'https://api.example.com/luvia', outDir: 'src/luxo' })
    ;(plugin.configResolved as (config: { root: string }) => void)({ root: '/workspace/web' })
    await (plugin.buildStart as () => Promise<void>)()
    const transform = plugin.transform as (code: string, id: string) => { code: string } | null

    const result = transform(
      'const member = await client.me(); consume(member)',
      '/workspace/golang/luxo/luxo-studio/web/src/hooks/use-auth.ts',
    )
    expect(result?.code).toContain("client.me({ $select: 'id' })")
    expect(transform('export const generated = true', '/workspace/web/src/luxo/client.ts')).toBeNull()
  })
})
