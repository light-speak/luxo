import { expect, it, vi } from 'vitest'
import { FetchTransport } from './transport'

it('shares binary validation and request cancellation with a specialized HTTP executor', async () => {
  const send = vi.fn(async (_endpoint: string, init: RequestInit) => {
    expect(init.signal).toBeInstanceOf(AbortSignal)
    expect(init.method).toBe('POST')
    return { status: 200, arrayBuffer: async () => Uint8Array.of(42).buffer }
  })
  class SpecializedTransport extends FetchTransport {
    protected override executeRequest(endpoint: string, init: RequestInit) { return send(endpoint, init) }
  }
  const transport = new SpecializedTransport('https://localhost/api', { mode: 'binary' })
  transport.setSchema({ echo: { id: 1, params: [] } })
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  const controller = new AbortController()
  controller.abort()
  await expect(transport.call('echo', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
  expect(send).toHaveBeenCalledTimes(1)
})
