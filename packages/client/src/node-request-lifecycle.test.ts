import { EventEmitter } from 'node:events'
import { createServer } from 'node:net'
import type { Socket } from 'node:net'
import { afterEach, expect, it, vi } from 'vitest'
import { NodeHTTP2Transport } from './node'

const mocked = vi.hoisted(() => ({ connect: vi.fn() }))
vi.mock('node:http2', async importOriginal => ({
  ...await importOriginal<typeof import('node:http2')>(), connect: mocked.connect,
}))

afterEach(() => vi.clearAllMocks())

it('destroys the real H2 session after forbidden connection headers reject stream creation', async () => {
  const actual = await vi.importActual<typeof import('node:http2')>('node:http2')
  mocked.connect.mockImplementation(actual.connect)
  const sockets = new Set<Socket>()
  const server = createServer(socket => { sockets.add(socket); socket.on('error', () => {}) })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('TCP listener required')
  const transport = new NodeHTTP2Transport(`https://127.0.0.1:${address.port}/api`, {
    mode: 'binary', headers: { connection: 'keep-alive' },
  })
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
    expect(mocked.connect).toHaveBeenCalledOnce()
    const session: unknown = mocked.connect.mock.results[0].value
    expect(session).toMatchObject({ destroyed: true })
  } finally {
    transport.close()
    for (const socket of sockets) socket.destroy()
    await new Promise<void>(resolve => server.close(() => resolve()))
  }
})

it('retires a new connection when opening its first stream throws', async () => {
  const session = Object.assign(new EventEmitter(), {
    connecting: true, closed: false, destroyed: false,
    request: vi.fn(() => { throw new Error('invalid request headers') }),
    destroy: vi.fn(),
  })
  mocked.connect.mockReturnValue(session)
  const transport = new NodeHTTP2Transport('https://example.invalid/api', { mode: 'binary' })
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError', message: 'invalid request headers' })
    expect(session.destroy).toHaveBeenCalledOnce()
  } finally { transport.close() }
})

it('keeps an established connection when opening a later stream throws', async () => {
  const session = Object.assign(new EventEmitter(), {
    connecting: false, closed: false, destroyed: false,
    request: vi.fn(() => {
      session.emit('remoteSettings', { maxConcurrentStreams: 16 })
      throw new Error('invalid request headers')
    }),
    destroy: vi.fn(),
  })
  mocked.connect.mockReturnValue(session)
  const transport = new NodeHTTP2Transport('https://example.invalid/api', { mode: 'binary' })
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
    expect(session.destroy).not.toHaveBeenCalled()
  } finally { transport.close() }
})

it('preserves an active stream when another opening fails during handshake', async () => {
  const stream = Object.assign(new EventEmitter(), {
    end: vi.fn(),
    close: vi.fn(() => queueMicrotask(() => stream.emit('close'))),
  })
  const session = Object.assign(new EventEmitter(), {
    connecting: true, closed: false, destroyed: false,
    request: vi.fn().mockReturnValueOnce(stream).mockImplementation(() => {
      throw new Error('invalid later headers')
    }),
    destroy: vi.fn(),
  })
  mocked.connect.mockReturnValue(session)
  const transport = new NodeHTTP2Transport('https://example.invalid/api', { mode: 'binary' })
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    const first = transport.call('echo')
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
    expect(session.destroy).not.toHaveBeenCalled()
    session.connecting = false
    session.emit('remoteSettings', { maxConcurrentStreams: 16 })
    stream.emit('response', { ':status': 200 })
    stream.emit('data', Buffer.from([42]))
    stream.emit('end')
    expect(await first).toEqual(Uint8Array.of(42))
    expect(session.destroy).not.toHaveBeenCalled()
    expect(mocked.connect).toHaveBeenCalledOnce()
  } finally { transport.close() }
})

it('replaces each failed cold session without leaking request capacity', async () => {
  const created: Array<ReturnType<typeof makeSession>> = []
  function makeSession() {
    const session = Object.assign(new EventEmitter(), {
      connecting: true, closed: false, destroyed: false,
      request: vi.fn(() => { throw new Error('invalid headers') }),
      destroy: vi.fn(() => { session.destroyed = true }),
    })
    return session
  }
  mocked.connect.mockImplementation(() => {
    const session = makeSession()
    created.push(session)
    return session
  })
  const transport = new NodeHTTP2Transport('https://example.invalid/api', {
    mode: 'binary', maxConcurrentRequests: 1,
  })
  transport.setSchema({ echo: { id: 1, params: [] } })
  try {
    for (let index = 0; index < 3; index++) {
      await expect(transport.call('echo')).rejects.toMatchObject({
        error: 'NetworkError', message: 'invalid headers',
      })
      expect(created).toHaveLength(index + 1)
      expect(created[index].destroy).toHaveBeenCalledOnce()
    }
  } finally { transport.close() }
})
