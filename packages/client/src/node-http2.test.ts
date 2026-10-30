import { readFileSync } from 'node:fs'
import { createSecureServer } from 'node:http2'
import { createServer } from 'node:net'
import { createServer as createTLSServer } from 'node:tls'
import type { Socket } from 'node:net'
import type { Http2SecureServer, ServerHttp2Stream, ServerHttp2Session, IncomingHttpHeaders } from 'node:http2'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { NodeHTTP2Transport } from './node'

const cert = readFileSync(new URL('../testdata/node-http2-cert.pem', import.meta.url))
const key = readFileSync(new URL('../testdata/node-http2-key.pem', import.meta.url))
let server: Http2SecureServer
let endpoint: string
let clients: NodeHTTP2Transport[]
let sessions: Set<ServerHttp2Session>
let handle: (stream: ServerHttp2Stream, body: Buffer, headers: IncomingHttpHeaders) => void

beforeEach(async () => {
  clients = []
  sessions = new Set()
  handle = stream => { stream.respond({ ':status': 200 }); stream.end(Uint8Array.of(42)) }
  server = createSecureServer({ cert, key, allowHTTP1: false })
  server.on('session', session => { sessions.add(session); session.on('error', () => {}) })
  server.on('stream', (stream: ServerHttp2Stream, headers: IncomingHttpHeaders) => {
    const chunks: Buffer[] = []
    stream.on('error', () => {})
    stream.on('data', (chunk: Buffer) => chunks.push(chunk))
    stream.on('end', () => handle(stream, Buffer.concat(chunks), headers))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('TCP listener required')
  endpoint = `https://127.0.0.1:${address.port}/api`
})

afterEach(async () => {
  vi.useRealTimers()
  for (const client of clients) client.close()
  for (const session of sessions) session.destroy()
  await new Promise<void>(resolve => server.close(() => resolve()))
})

function client(options: ConstructorParameters<typeof NodeHTTP2Transport>[1] = {}) {
  const result = new NodeHTTP2Transport(endpoint, { mode: 'binary', ...options, tls: { ca: cert } })
  result.setSchema({ echo: { id: 1, params: [] } })
  clients.push(result)
  return result
}

it('reuses verified TLS H2 and preserves response bytes', async () => {
  const transport = client()
  for (let index = 0; index < 3; index++) expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  expect(sessions.size).toBe(1)
})

it('cancels one partial response without closing another request or the session', async () => {
  const transport = client({ maxConcurrentRequests: 2 })
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  let held: ServerHttp2Stream | undefined
  let otherStream: ServerHttp2Stream | undefined
  let otherNotify: (() => void) | undefined
  const otherStarted = new Promise<void>(resolve => { otherNotify = resolve })
  let notify: (() => void) | undefined
  const started = new Promise<void>(resolve => { notify = resolve })
  handle = stream => {
    stream.respond({ ':status': 200 })
    if (!held) { held = stream; stream.write(Uint8Array.of(1)); notify?.() }
    else { otherStream = stream; otherNotify?.() }
  }
  const controller = new AbortController()
  const pending = expect(transport.call('echo', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
  await started
  const other = expect(transport.call('echo')).resolves.toEqual(Uint8Array.of(42))
  await otherStarted
  controller.abort()
  await pending
  expect(otherStream?.destroyed).toBe(false)
  otherStream?.end(Uint8Array.of(42))
  await other
  expect(sessions.size).toBe(1)
})

it.each([1, 16])('honors local/peer capacity and releases timeout slots (local=%s)', async maximum => {
  // Keep real socket I/O; advance only the SDK deadline after capacity is occupied.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  server.updateSettings({ maxConcurrentStreams: 1 })
  const transport = client({ maxConcurrentRequests: maximum, timeout: 25 })
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  handle = stream => { stream.respond({ ':status': 200 }); stream.write(Uint8Array.of(1)) }
  const first = expect(transport.call('echo')).rejects.toMatchObject({ error: 'TimeoutError' })
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
  await vi.advanceTimersByTimeAsync(25)
  await first
  handle = stream => { stream.respond({ ':status': 200 }); stream.end(Uint8Array.of(42)) }
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
})

it('rejects oversized responses and remains usable', async () => {
  handle = stream => { stream.respond({ ':status': 200 }); stream.end(Buffer.alloc(65)) }
  const transport = client({ maxResponseBytes: 64 })
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError' })
  handle = stream => { stream.respond({ ':status': 200 }); stream.end(Uint8Array.of(42)) }
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
})

it.each(['declared', 'streamed'])('isolates an oversized %s response and returns its capacity before another response finishes', async kind => {
  const transport = client({ maxResponseBytes: 64, maxConcurrentRequests: 2, timeout: 1000 })
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  const active: ServerHttp2Stream[] = []
  let notifyStarted: (() => void) | undefined
  const started = new Promise<void>(resolve => { notifyStarted = resolve })
  handle = stream => {
    active.push(stream)
    if (active.length === 2) notifyStarted?.()
    if (active.length > 2) {
      stream.respond({ ':status': 200 })
      stream.end(Uint8Array.of(42))
    }
  }
  const oversized = expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError' })
  const sibling = expect(transport.call('echo')).resolves.toEqual(Uint8Array.of(42))
  await started
  active[1].respond({ ':status': 200 })
  if (kind === 'declared') {
    active[0].respond({ ':status': 200, 'content-length': '65' })
  } else {
    active[0].respond({ ':status': 200 })
    active[0].write(Buffer.alloc(64))
    active[0].write(Uint8Array.of(1))
  }
  // No end frame: exceeding the limit must reject before the server completes.
  await oversized
  expect(active[1].destroyed).toBe(false)
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  active[1].end(Uint8Array.of(42))
  await sibling
  expect(active).toHaveLength(3)
  expect(sessions.size).toBe(1)
})

it('closes pending requests and rejects later calls', async () => {
  const transport = client()
  handle = stream => { stream.respond({ ':status': 200 }); stream.write(Uint8Array.of(1)); transport.close() }
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'CancelledError' })
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
  transport.close()
})

it('rejects insecure endpoints and invalid limits before connecting', () => {
  expect(() => new NodeHTTP2Transport('http://localhost/api')).toThrow()
  for (const value of [0, -1, NaN, Infinity, 1.5]) {
    expect(() => new NodeHTTP2Transport(endpoint, { maxConcurrentRequests: value })).toThrow()
    expect(() => new NodeHTTP2Transport(endpoint, { maxResponseBytes: value })).toThrow()
    expect(() => new NodeHTTP2Transport(endpoint, { maxRequestBytes: value })).toThrow()
  }
  expect(sessions.size).toBe(0)
})

it('does not trust an unconfigured certificate', async () => {
  const transport = new NodeHTTP2Transport(endpoint, { mode: 'binary', timeout: 1000 })
  clients.push(transport)
  transport.setSchema({ echo: { id: 1, params: [] } })
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
})

it('rejects oversized requests before opening a connection', async () => {
  const transport = client({ maxRequestBytes: 1 })
  await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
  expect(sessions.size).toBe(0)
})

it('preserves JSON and authenticated refresh through the common transport', async () => {
  let calls = 0
  let refreshes = 0
  handle = (stream, body, headers) => {
    calls++
    expect(JSON.parse(body.toString())).toEqual({ $api: 'echo' })
    expect(headers['content-type']).toBe('application/json')
    if (headers.authorization === 'Bearer old') {
      stream.respond({ ':status': 401 })
      stream.end(JSON.stringify({ error: 'Unauthorized', code: 401, message: 'expired' }))
    } else {
      expect(headers.authorization).toBe('Bearer new')
      stream.respond({ ':status': 200 })
      stream.end(JSON.stringify({ data: 42 }))
    }
  }
  const transport = client({ mode: 'json', token: 'old', onTokenExpired: async () => { refreshes++; return 'new' } })
  expect(await transport.call('echo')).toBe(42)
  expect(calls).toBe(2)
  expect(refreshes).toBe(1)
  expect(sessions.size).toBe(1)
})

it('finishes accepted GOAWAY work and reconnects only for a later explicit call', async () => {
  let calls = 0
  handle = stream => {
    calls++
    stream.respond({ ':status': 200 })
    stream.session?.goaway(0, stream.id)
    stream.end(Uint8Array.of(42))
  }
  const transport = client()
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  expect(calls).toBe(2)
  expect(sessions.size).toBe(2)
})

it('rejects a truncated declared body without replaying the call', async () => {
  let calls = 0
  handle = stream => {
    calls++
    stream.respond({ ':status': 200, 'content-length': '10' })
    stream.end(Uint8Array.of(1))
  }
  await expect(client().call('echo')).rejects.toMatchObject({ error: 'ParseError' })
  expect(calls).toBe(1)
})

it.each([false, true])('destroys stalled handshake after timeout (TLS negotiated=%s)', async negotiated => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const connected = Promise.withResolvers<void>()
  const sockets = new Set<Socket>()
  let closed: (() => void) | undefined
  const disconnected = new Promise<void>(resolve => { closed = resolve })
  const hold = (socket: Socket) => {
    sockets.add(socket)
    socket.on('data', () => {})
    socket.on('close', () => closed?.())
    connected.resolve()
  }
  const stalled = negotiated ? createTLSServer({ cert, key, ALPNProtocols: ['h2'] }, hold) : createServer(hold)
  await new Promise<void>(resolve => stalled.listen(0, '127.0.0.1', resolve))
  const address = stalled.address()
  if (!address || typeof address === 'string') throw new Error('TCP listener required')
  const transport = new NodeHTTP2Transport(`https://127.0.0.1:${address.port}/api`, { mode: 'binary', timeout: 25, tls: { ca: cert } })
  clients.push(transport)
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    const failed = expect(transport.call('echo')).rejects.toMatchObject({ error: 'TimeoutError' })
    // Observe TCP/TLS acceptance before firing the SDK deadline.
    await connected.promise
    await vi.advanceTimersByTimeAsync(25)
    await failed
    await disconnected
  } finally {
    vi.useRealTimers()
    transport.close()
    for (const socket of sockets) socket.destroy()
    await new Promise<void>(resolve => stalled.close(() => resolve()))
  }
})

it('rejects TLS peers without H2 instead of keeping an unusable session', async () => {
  const sockets = new Set<Socket>()
  const other = createTLSServer({ cert, key }, socket => {
    sockets.add(socket)
    socket.on('data', () => {})
  })
  await new Promise<void>(resolve => other.listen(0, '127.0.0.1', resolve))
  const address = other.address()
  if (!address || typeof address === 'string') throw new Error('TCP listener required')
  const transport = new NodeHTTP2Transport(`https://127.0.0.1:${address.port}/api`, {
    mode: 'binary', timeout: 250, tls: { ca: cert },
  })
  clients.push(transport)
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
  } finally {
    transport.close()
    for (const socket of sockets) socket.destroy()
    await new Promise<void>(resolve => other.close(() => resolve()))
  }
})

it('keeps concurrent responses independent when the peer finishes out of order', async () => {
  const transport = client({ mode: 'json' })
  transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'key', type: 'String' }] } })
  handle = (stream, body) => {
    const request: unknown = JSON.parse(body.toString())
    if (!request || typeof request !== 'object' || !('key' in request) || typeof request.key !== 'string') {
      throw new Error('expected a string key')
    }
    const key = request.key
    setTimeout(() => { stream.respond({ ':status': 200 }); stream.end(JSON.stringify({ data: key })) }, 8 - Number(key))
  }
  const keys = Array.from({ length: 8 }, (_, index) => String(index))
  expect(await Promise.all(keys.map(key => transport.call('echo', { key })))).toEqual(keys)
  expect(sessions.size).toBe(1)
})
