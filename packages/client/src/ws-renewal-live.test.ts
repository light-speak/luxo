import { expect, it, vi } from 'vitest'
import { WsTransport } from './transport'

const endpoint = process.env.LUXO_WS_RENEWAL_URL

it.skipIf(!endpoint)('renews expired access against the owned Go service in both modes', async () => {
  for (const mode of ['json', 'binary'] as const) {
    const bootstrap = await fetch(`${endpoint}/token`)
    expect(bootstrap.status).toBe(200)
    const refresh = bootstrap.headers.get('X-Refresh-Token')
    expect(refresh).not.toBeNull()
    const token = await bootstrap.text()
    const renewed = Promise.withResolvers<void>()
    let refreshes = 0
    const transport = new WsTransport(endpoint!.replace('http://', 'ws://') + '/ws', {
      mode, token, timeout: 5000,
      onTokenExpired: async () => {
        refreshes++
        const response = await fetch(`${endpoint}/refresh`, { headers: { Authorization: `Bearer ${refresh}` } })
        expect(response.status).toBe(200)
        const access = await response.text()
        renewed.resolve()
        return access
      },
    })
    transport.setSchema({ identity: { id: 7, params: [] } })
    try {
      const first = await transport.call('identity')
      expect(first).toEqual(mode === 'json' ? 42 : new Uint8Array([84]))
      const deadline = setTimeout(() => renewed.reject(new Error('expiry did not trigger renewal')), 6000)
      try { await renewed.promise } finally { clearTimeout(deadline) }
      expect(await transport.call('identity')).toEqual(first)
      expect(refreshes).toBe(1)
    } finally {
      transport.close()
    }
  }
}, 15000)


it.skipIf(!endpoint || process.env.LUXO_WS_RENEWAL_STREAM !== '1')('ends expired streams without replay and permits explicit subscription through the gateway', async () => {
  const stats = async (): Promise<{ started: number; gateway: number; backend: number }> => {
    const response = await fetch(`${endpoint}/stats`)
    expect(response.status).toBe(200)
    return response.json()
  }
  for (const mode of ['json', 'binary'] as const) {
    await vi.waitFor(async () => { expect(await stats()).toMatchObject({ gateway: 0, backend: 0 }) })
    const before = (await stats()).started
    const bootstrap = await fetch(`${endpoint}/token`)
    const refresh = bootstrap.headers.get('X-Refresh-Token')
    const token = await bootstrap.text()
    const renewed = Promise.withResolvers<void>()
    const ended = Promise.withResolvers<unknown>()
    const first = Promise.withResolvers<unknown>()
    let errors = 0, completions = 0, values = 0
    const transport = new WsTransport(endpoint!.replace('http://', 'ws://') + '/ws', {
      mode, token, timeout: 5000,
      onTokenExpired: async () => {
        const response = await fetch(`${endpoint}/refresh`, { headers: { Authorization: `Bearer ${refresh}` } })
        expect(response.status).toBe(200)
        const access = await response.text()
        renewed.resolve()
        return access
      },
    })
    transport.setSchema({ identity: { id: 7, params: [] }, watch: { id: 8, params: [] } })
    const expected = mode === 'json' ? 42 : new Uint8Array([84])
    try {
      await transport.subscribe('watch', {}, value => value, value => { values++; first.resolve(value) }, {
        onError: error => { errors++; ended.resolve(error) }, onComplete: () => { completions++ },
      })
      expect(await first.promise).toEqual(expected)
      expect(await ended.promise).toMatchObject({ code: 401 })
      await renewed.promise
      expect(await transport.call('identity')).toEqual(expected)
      await vi.waitFor(async () => { expect(await stats()).toEqual({ started: before + 1, gateway: 0, backend: 0 }) })
      const fresh = Promise.withResolvers<unknown>()
      const unsubscribe = await transport.subscribe('watch', {}, value => value, fresh.resolve, { onError: fresh.reject })
      expect(await fresh.promise).toEqual(expected)
      expect((await stats()).started).toBe(before + 2)
      unsubscribe()
      expect(errors).toBe(1)
      expect(completions).toBe(0)
      expect(values).toBe(1)
    } finally { transport.close() }
  }
}, 15000)
