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
