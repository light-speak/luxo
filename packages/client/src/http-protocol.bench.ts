import { deepStrictEqual } from 'node:assert'
import type { FetchTransport, TransportOptions } from './transport'
import { measure, measureAsync } from './measure.bench'

type TransportConstructor = typeof FetchTransport
const endpoint = 'http://localhost/luvia'
const utf8 = new TextEncoder()

// Snapshot comparison for response presence only. Binary uses a valid Int payload.
export async function measureHTTPResponsePresence(before: TransportConstructor, after: TransportConstructor): Promise<void> {
  const originalFetch = globalThis.fetch
  try {
    for (const name of ['small', 'null', 'nested16', 'unicode4K', 'binary'] as const) {
      const mode = name === 'binary' ? 'binary' : 'json'
      const body = name === 'binary' ? new Uint8Array([14])
        : utf8.encode(name === 'null' ? '{"data":null}' : fixture(name))
      globalThis.fetch = async () => new Response(body)
      const previous = new before(endpoint, { mode }), current = new after(endpoint, { mode })
      previous.setSchema({ echo: { id: 1 } })
      current.setSchema({ echo: { id: 1 } })
      deepStrictEqual(await previous.call('echo'), await current.call('echo'))
      await measureAsync(`response presence HTTP ${name}`, () => previous.call('echo'), () => current.call('echo'))
    }
  } finally { globalThis.fetch = originalFetch }
}

// Compile both snapshots with the same toolchain; do not time a Vite loader.
export async function measureHTTP(before: TransportConstructor, after: TransportConstructor): Promise<void> {
  const configurations: Record<string, TransportOptions | undefined> = {
    default: undefined,
    token: { token: 'test-token' },
    custom: { token: 'test-token', headers: { 'X-Region': 'test', 'X-Client': 'bench' } },
  }
  for (const [name, options] of Object.entries(configurations)) {
    measure(`init ${name}: 32 transports`, constructors(before, options), constructors(after, options))
    const oldTransport = new before(endpoint, options), newTransport = new after(endpoint, options)
    measure(`token update ${name}`, () => oldTransport.setToken('next'), () => newTransport.setToken('next'))
  }
  await measureResponseText()
  const originalFetch = globalThis.fetch
  try {
    for (const mode of ['json', 'binary'] as const) {
      for (const name of ['small', 'nested16', 'unicode4K'] as const) {
        const bytes = utf8.encode(fixture(name))
        globalThis.fetch = async () => new Response(bytes)
        const options = { mode, token: 'test-token', headers: { 'X-Region': 'test' } }
        const oldTransport = new before(endpoint, options), newTransport = new after(endpoint, options)
        oldTransport.setSchema({ echo: { id: 1 } })
        newTransport.setSchema({ echo: { id: 1 } })
        deepStrictEqual(await oldTransport.call('echo'), await newTransport.call('echo'))
        await measureAsync(`${mode} call: ${name}`, () => oldTransport.call('echo'), () => newTransport.call('echo'))
      }
    }
  } finally {
    globalThis.fetch = originalFetch
  }
}

function constructors(Transport: TransportConstructor, options?: TransportOptions): () => number {
  const keep = new Array<FetchTransport>(32)
  return () => {
    for (let index = 0; index < keep.length; index++) keep[index] = new Transport(endpoint, options)
    return keep.length
  }
}

async function measureResponseText(): Promise<void> {
  const strict = new TextDecoder('utf-8', { fatal: true })
  for (const name of ['small', 'nested16', 'unicode4K'] as const) {
    const bytes = utf8.encode(fixture(name))
    const previous = () => new Response(bytes).text()
    const current = async () => strict.decode(await new Response(bytes).arrayBuffer())
    deepStrictEqual(await previous(), await current())
    await measureAsync(`response text: ${name}`, previous, current)
  }
}

function fixture(name: 'small' | 'nested16' | 'unicode4K'): string {
  if (name === 'small') return '{"data":true}'
  if (name === 'unicode4K') return JSON.stringify({ data: '发布检查'.repeat(350) })
  return '{"data":[' + Array.from({ length: 16 }, () =>
    '{"id":9223372036854775807,"name":"发布检查","nested":{"count":-9223372036854775808}}').join(',') + ']}'
}
