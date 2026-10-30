import { afterEach, expect, it, vi } from 'vitest'
import { FetchTransport } from './transport'
import { readHTTPBody } from './http-response'

afterEach(() => vi.unstubAllGlobals())

function transport(maxResponseBytes = 64) {
  const value = new FetchTransport('https://localhost/api', { mode: 'binary', maxResponseBytes })
  value.setSchema({ read: { id: 1, params: [] } })
  return value
}

it.each([0, -1, 1.5, NaN, Infinity, 0x80000000])('rejects invalid response limit %s', limit => {
  expect(() => transport(limit)).toThrow()
})

it.each(['declared', 'streamed'])('cancels an oversized %s response before EOF and isolates a sibling', async kind => {
  const cancelled = vi.fn()
  let sibling!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(controller) { if (kind === 'streamed') controller.enqueue(new Uint8Array(65)) },
    cancel: cancelled,
  })
  const pending = new ReadableStream<Uint8Array>({ start(controller) { sibling = controller } })
  const send = vi.fn()
    .mockResolvedValueOnce(new Response(body, { headers: kind === 'declared' ? { 'content-length': '65' } : {} }))
    .mockResolvedValueOnce(new Response(pending))
    .mockResolvedValueOnce(new Response(Uint8Array.of(42)))
  vi.stubGlobal('fetch', send)
  const client = transport()
  const rejected = expect(client.call('read')).rejects.toMatchObject({ error: 'ParseError' })
  const other = client.call('read')
  await rejected
  expect(cancelled).toHaveBeenCalledOnce()
  expect(await client.call('read')).toEqual(Uint8Array.of(42))
  sibling.enqueue(Uint8Array.of(7))
  sibling.close()
  expect(await other).toEqual(Uint8Array.of(7))
})

it('counts received bytes even when Content-Length understates the body', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array(65), { headers: { 'content-length': '1' } })))
  await expect(transport().call('read')).rejects.toMatchObject({ error: 'ParseError' })
})

it('accepts exact-limit chunks and empty bodies', async () => {
  const body = new ReadableStream<Uint8Array>({ start(controller) {
    controller.enqueue(Uint8Array.of(1, 2))
    controller.enqueue(Uint8Array.of(3, 4))
    controller.close()
  } })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(body)).mockResolvedValueOnce(new Response(null)))
  const client = transport(4)
  expect(await client.call('read')).toEqual(Uint8Array.of(1, 2, 3, 4))
  expect(await client.call('read')).toEqual(new Uint8Array())
})

it('bounds JSON responses before parsing and preserves normal JSON results', async () => {
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(new Response('{"data":"' + 'x'.repeat(64) + '"}'))
    .mockResolvedValueOnce(new Response('{"data":42}')))
  const client = new FetchTransport('https://localhost/api', { maxResponseBytes: 64 })
  await expect(client.call('read')).rejects.toMatchObject({ error: 'ParseError' })
  expect(await client.call('read')).toBe(42)
})

it('enforces the default 2 MiB limit', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(new Uint8Array(2 * 1024 * 1024 + 1))))
  const client = new FetchTransport('https://localhost/api', { mode: 'binary' })
  client.setSchema({ read: { id: 1, params: [] } })
  await expect(client.call('read')).rejects.toMatchObject({ error: 'ParseError' })
})

it('keeps a single received chunk without copying its backing buffer', async () => {
  const source = new Uint8Array(64).subarray(4, 8)
  const body = new ReadableStream<Uint8Array>({ start(controller) {
    controller.enqueue(source)
    controller.close()
  } })
  expect(await readHTTPBody(new Response(body), 4)).toBe(source)
  expect(body.locked).toBe(false)
})

it('counts decoded bytes rather than a compressed Content-Length', async () => {
  const response = new Response(Uint8Array.of(1), { headers: { 'content-length': '100', 'content-encoding': 'gzip' } })
  expect(await readHTTPBody(response, 1)).toEqual(Uint8Array.of(1))
})

it('bounds complete bodies supplied by a specialized executor', async () => {
  await expect(readHTTPBody({ status: 200, arrayBuffer: async () => new ArrayBuffer(65) }, 64))
    .rejects.toMatchObject({ error: 'ParseError' })
})

it('propagates body read failures and releases the reader', async () => {
  const failure = new Error('connection lost')
  const body = new ReadableStream<Uint8Array>({ start(controller) { controller.error(failure) } })
  await expect(readHTTPBody(new Response(body), 64)).rejects.toBe(failure)
  expect(body.locked).toBe(false)
})
