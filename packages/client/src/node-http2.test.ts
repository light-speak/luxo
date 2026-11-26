import { readFileSync } from 'node:fs'
import { getEventListeners } from 'node:events'
import { createSecureServer } from 'node:http2'
import { createServer } from 'node:net'
import { createServer as createHTTPServer } from 'node:http'
import { createServer as createTLSServer } from 'node:tls'
import type { Socket } from 'node:net'
import type { Http2SecureServer, ServerHttp2Stream, ServerHttp2Session, IncomingHttpHeaders } from 'node:http2'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { NodeHTTP2Transport } from './node'
import { Encoder } from './codec'

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

it('keeps native H2 registration after non-string parameter names are rejected', async () => {
  const transport = client()
  transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
  let received: number[] | undefined
  handle = (stream, body) => {
    received = Array.from(body)
    stream.respond({ ':status': 200 })
    stream.end(Uint8Array.of(42))
  }
  for (const name of [undefined, null, false, true, 0, 7, [], {}]) {
    const invalid = { echo: { id: 2, params: [{ fieldID: 2, name: 'other', type: 'Int' }] } }
    Object.assign(invalid.echo.params[0], { name })
    expect(() => transport.setSchema(invalid)).toThrow(/name.*string/)
  }
  await expect(transport.call('echo', { value: 7n })).resolves.toEqual(Uint8Array.of(42))
  expect(received).toEqual([1, 0, 1, 14, 0])
})

it('reuses verified TLS H2 and preserves response bytes', async () => {
  const transport = client()
  for (let index = 0; index < 3; index++) expect(await transport.call('echo')).toEqual(Uint8Array.of(42))
  expect(sessions.size).toBe(1)
})

it('keeps native H2 registration after non-string type metadata is rejected', async () => {
  const transport = client()
  transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
  let received: number[] | undefined
  handle = (stream, body) => {
    received = Array.from(body)
    stream.respond({ ':status': 200 })
    stream.end(Uint8Array.of(42))
  }
  for (const target of ['param', 'field', 'type']) {
    for (const member of ['type', 'typeName']) {
      for (const value of [false, true, 0, 7, [], {}]) {
        const field = { fieldID: 2, type: 'Int' }
        const invalid = { echo: { id: 2, params: [{ fieldID: 2, name: 'other', type: 'Int' }],
          fields: { id: { ...field } }, types: { Input: { id: { ...field } } } } }
        const metadata = target === 'param' ? invalid.echo.params[0]
          : target === 'field' ? invalid.echo.fields.id : invalid.echo.types.Input.id
        Object.assign(metadata, { [member]: value })
        expect(() => transport.setSchema(invalid)).toThrow(new RegExp(`${member}.*string`))
      }
    }
  }
  await expect(transport.call('echo', { value: 7n })).resolves.toEqual(Uint8Array.of(42))
  expect(received).toEqual([1, 0, 1, 14, 0])
})

it('rejects empty Binary bodies and releases TLS H2 capacity without losing the session', async () => {
  let requests = 0
  handle = stream => {
    stream.respond({ ':status': 200 })
    stream.end(++requests % 2 === 1 ? undefined : Uint8Array.of(0))
  }
  const transport = client({ maxConcurrentRequests: 1 })
  const codec = { apiID: 1, initialCapacity: () => 8, writeParameters() {} }
  for (const generated of [false, true]) {
    const call = () => generated ? transport.callGenerated('echo', codec) : transport.call('echo')
    await expect(call()).rejects.toMatchObject({ error: 'ParseError', code: 200 })
    await expect(call()).resolves.toEqual(Uint8Array.of(0))
  }
  expect(requests).toBe(4)
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
  // Keep real socket I/O; advance the SDK timer and monotonic clock together.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'performance'] })
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

it.each([['json', false], ['json', true], ['binary', false], ['binary', true]] as const)('closing H2 terminates %s refresh with late rejection=%s', async (mode, rejectLate) => {
  const entered = Promise.withResolvers<void>()
  const refresh = Promise.withResolvers<string | null>()
  const encoder = new Encoder()
  encoder.writeFieldInt(1, 401n)
  encoder.writeFieldString(2, 'Unauthorized')
  encoder.writeFieldString(3, 'expired')
  encoder.writeEnd()
  handle = stream => {
    stream.respond({ ':status': 401 })
    stream.end(mode === 'json' ? '{"error":"Unauthorized","code":401,"message":"expired"}' : encoder.bytes())
  }
  const transport = client({ mode, onTokenExpired: () => { entered.resolve(); return refresh.promise } })
  const setToken = vi.spyOn(transport, 'setToken')
  let settled = false
  let failure: unknown
  const observed = transport.call('echo').then(() => { settled = true }, error => { settled = true; failure = error })
  await entered.promise
  transport.close()
  await new Promise<void>(resolve => setImmediate(resolve))
  try {
    expect(settled).toBe(true)
    expect(failure).toMatchObject({ error: 'CancelledError' })
  } finally {
    if (rejectLate) refresh.reject(new Error('late refresh failure'))
    else refresh.resolve('late-token')
    await observed
  }
  await new Promise<void>(resolve => setImmediate(resolve))
  expect(setToken).not.toHaveBeenCalled()
})

it.each(['json', 'binary'] as const)('isolates cancelled %s refresh and removes both abort listeners', async mode => {
  class ObservableTransport extends NodeHTTP2Transport {
    get refreshSignal() { return this.refreshAbortSignal! }
  }
  const entered = Promise.withResolvers<void>()
  const renewal = Promise.withResolvers<string | null>()
  const refresh = vi.fn(() => { entered.resolve(); return renewal.promise })
  const transport = new ObservableTransport(endpoint, {
    mode, token: 'old', tls: { ca: cert },
    onTokenExpired: refresh,
  })
  transport.setSchema({ echo: { id: 1, params: [] } })
  clients.push(transport)
  const encoder = new Encoder()
  encoder.writeFieldInt(1, 401n)
  encoder.writeFieldString(2, 'Unauthorized')
  encoder.writeFieldString(3, 'expired')
  encoder.writeEnd()
  handle = (stream, _body, headers) => {
    const authorized = headers.authorization === 'Bearer new'
    stream.respond({ ':status': authorized ? 200 : 401 })
    stream.end(authorized ? (mode === 'json' ? '{"data":42}' : Uint8Array.of(42))
      : (mode === 'json' ? '{"error":"Unauthorized","code":401,"message":"expired"}' : encoder.bytes()))
  }
  const controller = new AbortController()
  const first = expect(transport.call('echo', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
  await entered.promise
  const second = transport.call('echo')
  await vi.waitFor(() => expect(getEventListeners(transport.refreshSignal, 'abort')).toHaveLength(2))
  controller.abort()
  await first
  expect(getEventListeners(controller.signal, 'abort')).toHaveLength(0)
  expect(getEventListeners(transport.refreshSignal, 'abort')).toHaveLength(1)
  renewal.resolve('new')
  expect(await second).toEqual(mode === 'json' ? 42 : Uint8Array.of(42))
  expect(refresh).toHaveBeenCalledOnce()
  expect(getEventListeners(transport.refreshSignal, 'abort')).toHaveLength(0)
  expect(sessions.size).toBe(1)
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
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'performance'] })
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

it.each(['json', 'binary'] as const)('preserves %s retry bytes over verified TLS H2', async mode => {
  const bodies: Buffer[] = []
  const tokens: (string | string[] | undefined)[] = []
  handle = (stream, body, headers) => {
    bodies.push(body)
    tokens.push(headers.authorization)
    if (bodies.length === 1) {
      stream.respond({ ':status': 401 })
      if (mode === 'json') stream.end('{"error":"Unauthorized","code":401,"message":"expired"}')
      else {
        const encoder = new Encoder()
        encoder.writeFieldInt(1, 401n)
        encoder.writeFieldString(2, 'Unauthorized')
        encoder.writeFieldString(3, 'expired')
        encoder.writeEnd()
        stream.end(encoder.bytes())
      }
    } else {
      stream.respond({ ':status': 200 })
      stream.end(mode === 'json' ? '{"data":true}' : Uint8Array.of(42))
    }
  }
  const params = { count: 42n }
  const transport = client({ mode, token: 'original', onTokenExpired: async () => {
    params.count = 99n
    transport.setSchema({ echo: { id: 2, params: [{ fieldID: 3, name: 'count', type: 'Int' }] } })
    return 'renewed'
  } })
  transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'count', type: 'Int' }] } })
  await transport.call('echo', params)
  expect(bodies).toHaveLength(2)
  expect(bodies[1]).toEqual(bodies[0])
  expect(tokens).toEqual(['Bearer original', 'Bearer renewed'])
  await transport.call('echo', params)
  expect(bodies[2]).not.toEqual(bodies[0])
  expect(sessions.size).toBe(1)
})

it.each(['json', 'binary'] as const)('never follows redirects or replays %s requests on verified H2', async mode => {
  let targetRequests = 0, sourceRequests = 0
  const target = createHTTPServer((request, reply) => {
    targetRequests++
    request.resume()
    reply.end('{"data":true}')
  })
  await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
  const address = target.address()
  if (!address || typeof address === 'string') throw new Error('missing target port')
  const refresh = vi.fn(async () => 'unused')
  const transport = client({ mode, headers: { 'X-Project-Key': 'test-only-key' }, onTokenExpired: refresh })
  try {
    for (const status of [301, 302, 303, 307, 308]) {
      for (const location of ['/capture', `http://127.0.0.1:${address.port}/capture`]) {
        handle = (stream, _body, headers) => {
          sourceRequests++
          if (headers[':path'] !== '/api') targetRequests++
          stream.respond({ ':status': status, location })
          stream.end()
        }
        const previous = sourceRequests
        await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: status })
        expect(sourceRequests).toBe(previous + 1)
        expect(targetRequests).toBe(0)
      }
    }
    expect(refresh).not.toHaveBeenCalled()
    handle = stream => { stream.respond({ ':status': 200 }); stream.end(mode === 'json' ? '{"data":true}' : Uint8Array.of(42)) }
    expect(await transport.call('echo')).toEqual(mode === 'json' ? true : Uint8Array.of(42))
    expect(sessions.size).toBe(1)
  } finally {
    target.closeAllConnections()
    await new Promise<void>(resolve => target.close(() => resolve()))
  }
})
