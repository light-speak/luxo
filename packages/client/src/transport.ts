import { LuxoError } from './error'
import { Encoder } from './codec'

export type TransportMode = 'json' | 'binary'

/** Encode a single API param (scalar or list) to binary using the schema metadata. */
function encodeParam(
  enc: Encoder,
  pm: { fieldID: number; type: string; isList?: boolean },
  v: unknown,
): void {
  if (pm.isList) {
    const arr = v as unknown[]
    switch (pm.type) {
      case 'Int': case 'Duration': case 'DateTime':
        enc.writeFieldIntArray(pm.fieldID, arr as number[]); break
      case 'Float':
        enc.writeFieldFloatArray(pm.fieldID, arr as number[]); break
      case 'String': case 'Enum': case 'Decimal':
        enc.writeFieldStringArray(pm.fieldID, arr as string[]); break
      case 'Boolean':
        enc.writeFieldBoolArray(pm.fieldID, arr as boolean[]); break
      case 'UUID':
        enc.writeFieldUUIDArray(pm.fieldID, arr as string[]); break
    }
    return
  }
  switch (pm.type) {
    case 'Int': case 'Duration':
      enc.writeFieldInt(pm.fieldID, v as number); break
    case 'Float':
      enc.writeFieldFloat(pm.fieldID, v as number); break
    case 'String': case 'Enum': case 'Decimal':
      enc.writeFieldString(pm.fieldID, v as string); break
    case 'Boolean':
      enc.writeFieldBool(pm.fieldID, v as boolean); break
    case 'UUID':
      // UUID is a fixed 16-byte value on the wire (not a length-prefixed string).
      enc.writeFieldUUID(pm.fieldID, v as string); break
    case 'DateTime':
      // DateTime sent as ISO string, server parses it
      enc.writeFieldString(pm.fieldID, v as string); break
  }
}

/** API schema metadata for binary encoding */
export interface APISchema {
  id: number
  params?: Array<{ fieldID: number; name: string; type: string; isList?: boolean }>
}

/** Transport interface — implemented by HTTP, WebSocket, etc. */
export interface Transport {
  call(api: string, params?: Record<string, unknown>): Promise<unknown>
  setSchema(schema: Record<string, APISchema>): void
  setMode(mode: TransportMode): void
  setToken(token: string): void
  close?(): void
}

/** Shared transport options */
export interface TransportOptions {
  token?: string
  headers?: Record<string, string>
  mode?: TransportMode
  /** Request timeout in milliseconds (default: 30000) */
  timeout?: number
  /** Called when token expires (401). Return new token to auto-retry. */
  onTokenExpired?: () => Promise<string | null>
}

// --- Fetch Transport (HTTP/2) ---

/** Fetch-based transport for Web / Node.js 18+.
 *  Uses HTTP/2 when available — single connection, multiplexed requests. */
export class FetchTransport implements Transport {
  private headers: Record<string, string> = {}
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private timeout: number
  private onTokenExpired?: () => Promise<string | null>

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    this.timeout = options?.timeout ?? 30000
    this.onTokenExpired = options?.onTokenExpired
    if (options?.headers) this.headers = { ...options.headers }
    if (options?.token) this.headers['Authorization'] = `Bearer ${options.token}`
  }

  setSchema(schema: Record<string, APISchema>): void { this.schema = schema }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string): void { this.headers['Authorization'] = `Bearer ${token}` }

  /** Enable/disable request logging */
  debug = false

  async call(api: string, params?: Record<string, unknown>): Promise<unknown> {
    const start = performance.now()
    try {
      const result = await (this.mode === 'binary' ? this.binaryCall(api, params) : this.jsonCall(api, params))
      const ms = (performance.now() - start).toFixed(1)
      if (this.debug) {
        const mode = this.mode === 'binary' ? '🔵' : '🟢'
        const size = result instanceof Uint8Array ? `${result.length}B` : 'json'
        console.log(`${mode} ${api} ${ms}ms → ${size}`, params ?? '')
      }
      return result
    } catch (e) {
      const ms = (performance.now() - start).toFixed(1)
      if (this.debug) {
        console.error(`🔴 ${api} ${ms}ms ✗`, e instanceof LuxoError ? e.message : e, params ?? '')
      }
      throw e
    }
  }

  private async jsonCall(api: string, params?: Record<string, unknown>): Promise<unknown> {
    const body: Record<string, unknown> = { $api: api }
    if (params) Object.assign(body, params)

    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), this.timeout)

    let resp: Response
    try {
      resp = await fetch(this.endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...this.headers },
        body: JSON.stringify(body),
        signal: controller.signal,
      })
    } catch (e) {
      clearTimeout(timer)
      if (e instanceof DOMException && e.name === 'AbortError') {
        throw new LuxoError('TimeoutError', 0, `request timed out after ${this.timeout}ms`)
      }
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
    }
    clearTimeout(timer)

    // 401 auto-refresh: call onTokenExpired, get new token, retry once
    if (resp.status === 401 && this.onTokenExpired) {
      const newToken = await this.onTokenExpired()
      if (newToken) {
        this.setToken(newToken)
        return this.jsonCall(api, params) // retry with new token
      }
    }

    let json: Record<string, unknown>
    try { json = await resp.json() } catch {
      throw new LuxoError('ParseError', resp.status, `invalid JSON (HTTP ${resp.status})`)
    }

    if (json.error) {
      throw new LuxoError(json.error as string, (json.code as number) ?? resp.status, (json.message as string) ?? '', json.traceId as string | undefined)
    }
    return json.data
  }

  private async binaryCall(api: string, params?: Record<string, unknown>): Promise<unknown> {
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}" — call setSchema() or use LuxoClient.create()`)

    const enc = new Encoder()
    enc.writeVarint(meta.id)
    enc.writeVarint(0) // field mask = 0 (SELECT *)
    if (params && meta.params) {
      for (const pm of meta.params) {
        const v = params[pm.name]
        if (v === undefined || v === null) continue
        encodeParam(enc, pm, v)
      }
    }
    enc.writeEnd()

    if (this.debug) {
      const b = enc.bytes()
      const previewLen = 64
      const hex = Array.from(b.subarray(0, previewLen)).map(x => x.toString(16).padStart(2, '0')).join(' ')
      const suffix = b.length > previewLen ? ' ...' : ''
      console.log(`[luxo] ${api} binary ${b.length}B: ${hex}${suffix}`, params)
    }

    let resp: Response
    try {
      resp = await fetch(this.endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-luxo', 'X-Luxo-Mode': 'binary', ...this.headers },
        body: enc.bytes(),
      })
    } catch (e) {
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
    }

    if (!resp.ok) {
      try {
        const json = await resp.json()
        throw new LuxoError(json.error ?? 'Error', json.code ?? resp.status, json.message ?? '', json.traceId)
      } catch (e) { if (e instanceof LuxoError) throw e; throw new LuxoError('Error', resp.status, `HTTP ${resp.status}`) }
    }

    return new Uint8Array(await resp.arrayBuffer())
  }
}

// --- WebSocket Transport ---

/** WebSocket transport — persistent connection, multiplexed requests.
 *  Ideal for real-time apps: subscriptions, events, high-frequency calls.
 *  Supports both JSON and binary modes. */
export class WsTransport implements Transport {
  private ws: WebSocket | null = null
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>()
  private seq = 0
  private connectPromise: Promise<void> | null = null
  private token: string | undefined
  private closed = false
  private reconnectAttempts = 0
  private maxReconnectAttempts = 10
  private reconnectDelay = 1000 // ms, doubles each attempt (exponential backoff)

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    this.token = options?.token
  }

  setSchema(schema: Record<string, APISchema>): void { this.schema = schema }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string): void { this.token = token }

  private connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) return Promise.resolve()
    if (this.connectPromise) return this.connectPromise

    this.connectPromise = new Promise((resolve, reject) => {
      const url = this.token ? `${this.endpoint}?token=${this.token}` : this.endpoint
      const ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'

      ws.onopen = () => {
        this.ws = ws
        this.connectPromise = null
        this.reconnectAttempts = 0
        resolve()
      }

      ws.onerror = () => {
        this.connectPromise = null
        reject(new LuxoError('NetworkError', 0, 'WebSocket connection failed'))
      }

      ws.onmessage = (event) => {
        if (this.mode === 'binary' && event.data instanceof ArrayBuffer) {
          this.handleBinaryResponse(new Uint8Array(event.data))
        } else {
          this.handleJSONResponse(typeof event.data === 'string' ? event.data : '')
        }
      }

      ws.onclose = () => {
        this.ws = null
        this.connectPromise = null
        // Reject all pending requests
        for (const [, p] of this.pending) {
          p.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
        }
        this.pending.clear()
        // Auto-reconnect with exponential backoff
        if (!this.closed && this.reconnectAttempts < this.maxReconnectAttempts) {
          const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts)
          this.reconnectAttempts++
          setTimeout(() => this.connect(), Math.min(delay, 30000))
        }
      }
    })
    return this.connectPromise
  }

  async call(api: string, params?: Record<string, unknown>): Promise<unknown> {
    await this.connect()
    const id = ++this.seq

    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject })

      if (this.mode === 'binary') {
        const meta = this.schema[api]
        if (!meta) { reject(new LuxoError('ConfigError', 0, `no schema for "${api}"`)); return }

        const enc = new Encoder()
        enc.writeVarint(id)       // request sequence ID
        enc.writeVarint(meta.id)  // API ID
        enc.writeVarint(0)        // field mask
        if (params && meta.params) {
          for (const pm of meta.params) {
            const v = params[pm.name]
            if (v === undefined || v === null) continue
            encodeParam(enc, pm, v)
          }
        }
        enc.writeEnd()
        this.ws!.send(enc.bytes())
      } else {
        this.ws!.send(JSON.stringify({ $id: id, $api: api, ...params }))
      }
    })
  }

  private handleJSONResponse(data: string) {
    try {
      const json = JSON.parse(data)
      const id = json.$id as number
      const p = this.pending.get(id)
      if (!p) return
      this.pending.delete(id)

      if (json.error) {
        p.reject(new LuxoError(json.error, json.code ?? 0, json.message ?? '', json.traceId))
      } else {
        p.resolve(json.data)
      }
    } catch { /* malformed message, ignore */ }
  }

  private handleBinaryResponse(data: Uint8Array) {
    // Binary WS response format: [seq varint][payload...]
    let off = 0
    let id = 0
    // Read seq varint
    let shift = 0
    while (off < data.length) {
      const b = data[off++]
      id += (b & 0x7F) * (2 ** shift)
      if (b < 0x80) break
      shift += 7
    }

    const p = this.pending.get(id)
    if (!p) return
    this.pending.delete(id)
    p.resolve(data.subarray(off))
  }

  close(): void {
    this.closed = true
    this.ws?.close()
    this.ws = null
  }
}
