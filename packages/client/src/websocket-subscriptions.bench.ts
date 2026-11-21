import { deepStrictEqual } from 'node:assert'
import type { APISchema, TransportMode, TransportOptions, WsTransport } from './transport'
import { decodeJSONEnvelope } from './json'
import { Encoder } from './codec'
import { readWireUint64 } from './uint64'
import type { UInt64 } from './uint64'
import { measure, measureAsync } from './measure.bench'

interface DispatchProbe {
  subscriptions: Map<UInt64, { mode: TransportMode; onData(data: unknown): void }>
  handleJSONResponse(data: string): void
  handleBinaryResponse(data: Uint8Array): void
}

// Use the built runtime, not Vitest's module loader, for the production side.
// This measures parsing and dispatch, not network I/O or generated decoding.
export function measureSubscriptionDispatch(Transport: typeof WsTransport): void {
  for (const size of [1, 32, 128]) {
    for (const mode of ['json', 'binary'] as const) {
      for (const id of [1, 0xffffffffffffffffn]) {
        measureDispatchCase(Transport, size, mode, id)
      }
    }
  }
}

function measureDispatchCase(Transport: typeof WsTransport, size: number, mode: TransportMode, id: UInt64): void {
  const schema: Record<string, APISchema> = {}
  for (let index = 0; index < size; index++) schema[`watch${index}`] = { id: index + 1 }
  const api = `watch${size - 1}`
  let beforeValue: unknown, afterValue: unknown
  const before = new FormerStreamDispatch(schema, api, value => { beforeValue = value })
  const runtime = new Transport('ws://localhost/benchmark', { mode })
  const after = runtime as unknown as DispatchProbe
  runtime.setSchema(schema)
  after.subscriptions.set(id, { mode, onData: value => { afterValue = value } })
  const oldJSON = `{"$stream":"${api}","data":42}`
  const newJSON = `{"$stream":${id},"data":42}`
  const oldBinary = Uint8Array.from(size < 128 ? [6, size, 84] : [6, 128, 1, 84])
  const encoder = new Encoder()
  encoder.writeVarint(0x12)
  encoder.writeUint64(id)
  encoder.writeVarint(84)
  const newBinary = encoder.bytes()
  const previous = () => {
    if (mode === 'json') before.json(oldJSON)
    else before.binary(oldBinary)
    return beforeValue
  }
  const current = () => {
    if (mode === 'json') after.handleJSONResponse(newJSON)
    else after.handleBinaryResponse(newBinary)
    return afterValue
  }
  deepStrictEqual(previous(), mode === 'json' ? 42 : new Uint8Array([84]))
  deepStrictEqual(current(), previous())
  measure(`WS ${mode} dispatch / ${size} APIs / ${id}`, previous, current)
  runtime.close()
}

// Frozen pre-migration API-name lookup. It cannot distinguish two instances of
// one API and is not a correct alternative to the new subscription contract.
class FormerStreamDispatch {
  private readonly subscriptions: Map<string, { onData(data: unknown): void }>

  constructor(private schema: Record<string, APISchema>, api: string, onData: (data: unknown) => void) {
    this.subscriptions = new Map([[api, { onData }]])
  }

  json(data: string): void {
    try {
      const json = decodeJSONEnvelope(data)
      if (typeof json.$sub === 'string') return
      if (typeof json.$stream === 'string') this.subscriptions.get(json.$stream)?.onData(json.data)
    } catch { /* Preserve the old malformed-frame boundary. */ }
  }

  binary(data: Uint8Array): void {
    const header = readWireUint64(data, 1)
    if (!header || data[0] !== 6) return
    const api = Object.entries(this.schema).find(([, meta]) => meta.id === header.value)?.[0]
    if (api) this.subscriptions.get(api)?.onData(data.subarray(header.offset))
  }
}

// Actual built transports, with one shared fake socket implementation on both sides.
type LegacyStreamTransport = Omit<WsTransport, 'subscribe'> & {
  subscribe(api: string, params: Record<string, unknown>, onData: (value: unknown) => void): Promise<() => void>
}
type LegacyTransportConstructor = new(endpoint: string, options?: TransportOptions) => LegacyStreamTransport

export async function measureWebSocketLifecycle(Before: LegacyTransportConstructor, After: typeof WsTransport): Promise<void> {
  const original = globalThis.WebSocket
  globalThis.WebSocket = LifecycleSocket as unknown as typeof WebSocket
  try {
    for (const mode of ['json', 'binary'] as const) await compareLifecycle(Before, After, mode)
  } finally { globalThis.WebSocket = original }
}

async function compareLifecycle(Before: LegacyTransportConstructor, After: typeof WsTransport, mode: TransportMode): Promise<void> {
  const before = await lifecycleFixture(Before, mode)
  const after = await lifecycleFixture(After, mode)
  try {
    deepStrictEqual(await before.transport.call('watch'), await after.transport.call('watch'))
    await measureAsync(`${mode} unary fake roundtrip`,
      () => before.transport.call('watch'), () => after.transport.call('watch'))
    await measureAsync(`${mode} setup ACK cancel`,
      async () => { (await before.transport.subscribe('watch', {}, () => {}))(); return 1 },
      async () => { (await after.transport.subscribe('watch', {}, value => value, () => {}))(); return 1 })
    let beforeValue: unknown, afterValue: unknown
    const cancelBefore = await before.transport.subscribe('watch', {}, value => { beforeValue = value })
    const cancelAfter = await after.transport.subscribe('watch', {}, value => value, value => { afterValue = value })
    const previous = before.socket.dataFrame(mode), current = after.socket.dataFrame(mode)
    measure(`${mode} established data dispatch`,
      () => { before.socket.receive(previous); return beforeValue },
      () => { after.socket.receive(current); return afterValue })
    cancelBefore(); cancelAfter()
  } finally { before.transport.close(); after.transport.close() }
}

// Same decoder contract on both sides, isolating the malformed-envelope cleanup change.
export async function measureWebSocketJSONFailure(Before: typeof WsTransport, After: typeof WsTransport): Promise<void> {
  const original = globalThis.WebSocket
  globalThis.WebSocket = LifecycleSocket as unknown as typeof WebSocket
  try {
    for (const mode of ['json', 'binary'] as const) {
      const before = await lifecycleFixture(Before, mode), after = await lifecycleFixture(After, mode)
      const decode = (value: unknown) => value
      try {
        deepStrictEqual(await before.transport.call('watch'), await after.transport.call('watch'))
        await measureAsync(`${mode} unary fake roundtrip`,
          () => before.transport.call('watch'), () => after.transport.call('watch'))
        await measureAsync(`${mode} setup ACK cancel`,
          async () => { (await before.transport.subscribe('watch', {}, decode, () => {}))(); return 1 },
          async () => { (await after.transport.subscribe('watch', {}, decode, () => {}))(); return 1 })
        let oldValue: unknown, newValue: unknown
        const oldCancel = await before.transport.subscribe('watch', {}, decode, value => { oldValue = value })
        const newCancel = await after.transport.subscribe('watch', {}, decode, value => { newValue = value })
        const oldFrame = before.socket.dataFrame(mode), newFrame = after.socket.dataFrame(mode)
        measure(`${mode} established data dispatch`,
          () => { before.socket.receive(oldFrame); return oldValue },
          () => { after.socket.receive(newFrame); return newValue })
        oldCancel(); newCancel()
      } finally { before.transport.close(); after.transport.close() }
    }
  } finally { globalThis.WebSocket = original }
}

async function lifecycleFixture<T extends Pick<WsTransport, 'call' | 'setSchema' | 'close'>>(
  Transport: new(endpoint: string, options?: TransportOptions) => T, mode: TransportMode,
): Promise<{ transport: T; socket: LifecycleSocket }> {
  const transport = new Transport('ws://localhost/benchmark', { mode })
  transport.setSchema({ watch: { id: 7 } })
  const value = await transport.call('watch')
  deepStrictEqual(value, mode === 'json' ? 7 : new Uint8Array([14]))
  return { transport, socket: LifecycleSocket.latest }
}

class LifecycleSocket {
  static readonly OPEN = 1
  static latest: LifecycleSocket
  readyState = 0
  binaryType = ''
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  private subscriptionID: UInt64 = 0

  constructor() {
    LifecycleSocket.latest = this
    queueMicrotask(() => { this.readyState = LifecycleSocket.OPEN; this.onopen?.() })
  }

  send(data: string | ArrayBuffer): void {
    if (typeof data === 'string') {
      const json = JSON.parse(data) as { $id: number; $sub?: string; $api?: string }
      if (json.$sub) {
        this.subscriptionID = json.$id
        this.receive(`{"$sub":${json.$id},"ok":true}`)
      } else if (json.$api) this.receive(`{"$id":${json.$id},"data":7}`)
      return
    }
    const bytes = new Uint8Array(data), header = readWireUint64(bytes, 1)
    if (!header) throw new Error('invalid benchmark request header')
    if (bytes[0] === 0x10) {
      this.subscriptionID = header.value
      this.receive(this.frame(0x13, header.value))
    } else if (bytes[0] === 1) this.receive(this.frame(2, header.value, [14]))
  }

  dataFrame(mode: TransportMode): string | ArrayBuffer {
    return mode === 'json' ? `{"$stream":${this.subscriptionID},"data":7}` : this.frame(0x12, this.subscriptionID, [14])
  }

  receive(data: string | ArrayBuffer): void { this.onmessage?.({ data }) }
  close(): void { this.readyState = 3; this.onclose?.() }

  private frame(kind: number, id: UInt64, payload: number[] = []): ArrayBuffer {
    const encoder = new Encoder()
    encoder.writeRawBytes(new Uint8Array([kind]))
    encoder.writeUint64(id)
    encoder.writeRawBytes(new Uint8Array(payload))
    return Uint8Array.from(encoder.bytes()).buffer
  }
}
