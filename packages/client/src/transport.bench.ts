import { bench, describe } from 'vitest'
import { FetchTransport } from './transport'

const response = {
  status: 200,
  json: async () => ({ data: true }),
}
globalThis.fetch = async () => response as Response

describe('FetchTransport hot path', () => {
  const transport = new FetchTransport('http://localhost/luvia')

  bench('observer disabled JSON call', async () => {
    await transport.call('health')
  })
})
