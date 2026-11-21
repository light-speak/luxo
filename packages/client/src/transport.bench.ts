import { beforeAll, describe, expect, test } from 'vitest'
import { FetchTransport } from './transport'

const body = new TextEncoder().encode('{"data":true}').buffer
const response = {
  status: 200,
  arrayBuffer: async () => body,
}
globalThis.fetch = async () => response as Response

describe('FetchTransport hot path', () => {
  const transport = new FetchTransport('http://localhost/luvia')

  beforeAll(async () => {
    expect(await transport.call('health')).toBe(true)
  })

  test('observer disabled JSON call', async ({ bench }) => {
    await bench('observer disabled JSON call', async () => {
      await transport.call('health')
    }).run()
  })
})
