import { LuxoError } from './error'
import { Encoder } from './codec'

export type TransportMode = 'json' | 'binary'

/** API schema metadata for binary encoding */
export interface APISchema {
  id: number
  params?: Array<{ fieldID: number; name: string; type: string }>
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
}

// --- Fetch Transport (HTTP/2) ---

/** Fetch-based transport for Web / Node.js 18+.
 *  Uses HTTP/2 when available — single connection, multiplexed requests. */
export class FetchTransport implements Transport {
  private headers: Record<string, string> = {}
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    if (options?.headers) this.headers = { ...options.headers }
    if (options?.token) this.headers['Authorization'] = `Bearer ${options.token}`
  }

  setSchema(schema: Record<string, APISchema>): void { this.schema = schema }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string): void { this.headers['Authorization'] = `Bearer ${token}` }

  async call(api: string, params?: Record<string, unknown>): Promise<unknown> {
    return this.mode === 'binary' ? this.binaryCall(api, params) : this.jsonCall(api, params)
  }

  private async jsonCall(api: string, params?: Record<string, unknown>): Promise<unknown> {
    const body: Record<string, unknown> = { $api: api }
    if (params) Object.assign(body, params)

    let resp: Response
    try {
      resp = await fetch(this.endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...this.headers },
        body: JSON.stringify(body),
      })
    } catch (e) {
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
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
        switch (pm.type) {
          case 'Int': enc.writeFieldInt(pm.fieldID, v as number); break
          case 'Float': enc.writeFieldFloat(pm.fieldID, v as number); break
          case 'String': enc.writeFieldString(pm.fieldID, v as string); break
          case 'Boolean': enc.writeFieldBool(pm.fieldID, v as boolean); break
        }
      }
    }
    enc.writeEnd()

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
        // Reject all pending requests
        for (const [, p] of this.pending) {
          p.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
        }
        this.pending.clear()
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
            switch (pm.type) {
              case 'Int': enc.writeFieldInt(pm.fieldID, v as number); break
              case 'Float': enc.writeFieldFloat(pm.fieldID, v as number); break
              case 'String': enc.writeFieldString(pm.fieldID, v as string); break
              case 'Boolean': enc.writeFieldBool(pm.fieldID, v as boolean); break
            }
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
    this.ws?.close()
    this.ws = null
  }
}
