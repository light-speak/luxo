import { LuxoError } from './error'
import { Encoder, Decoder } from './codec'

export type TransportMode = 'json' | 'binary'

/** Transport interface — implement for different runtimes */
export interface Transport {
  call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T>
}

/** Schema metadata needed for binary encoding */
export interface APISchema {
  id: number
  params?: Array<{ fieldID: number; name: string; type: string }>
}

export interface TransportOptions {
  token?: string
  headers?: Record<string, string>
  /** Transport mode: 'json' for debugging, 'binary' for production. Default: 'json' */
  mode?: TransportMode
  /** API schema map (name → metadata) — required for binary mode */
  schema?: Record<string, APISchema>
}

/** Fetch-based transport for Web / React Native / Node.js 18+ */
export class FetchTransport implements Transport {
  private headers: Record<string, string> = {}
  private mode: TransportMode
  private schema: Record<string, APISchema>

  constructor(
    private endpoint: string,
    options?: TransportOptions,
  ) {
    this.mode = options?.mode ?? 'json'
    this.schema = options?.schema ?? {}
    if (options?.headers) {
      this.headers = { ...options.headers }
    }
    if (options?.token) {
      this.headers['Authorization'] = `Bearer ${options.token}`
    }
  }

  async call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T> {
    if (this.mode === 'binary') {
      return this.callBinary<T>(api, params)
    }
    return this.callJSON<T>(api, params)
  }

  private async callJSON<T>(api: string, params?: Record<string, unknown>): Promise<T> {
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
    try {
      json = await resp.json()
    } catch {
      throw new LuxoError('ParseError', resp.status, `invalid JSON response (HTTP ${resp.status})`)
    }

    if (json.error) {
      throw new LuxoError(
        json.error as string,
        (json.code as number) ?? resp.status,
        (json.message as string) ?? '',
        json.traceId as string | undefined,
      )
    }
    return json.data as T
  }

  private async callBinary<T>(api: string, params?: Record<string, unknown>): Promise<T> {
    const apiMeta = this.schema[api]
    if (!apiMeta) {
      throw new LuxoError('ConfigError', 0, `no schema for API "${api}" — binary mode requires schema`)
    }

    // Encode binary request: [API ID varint][FieldMask len varint][mask][Params][0x00]
    const enc = new Encoder()
    enc.writeVarint(apiMeta.id)
    enc.writeVarint(0) // field mask length = 0 (SELECT *)

    // Encode params
    if (params && apiMeta.params) {
      for (const pm of apiMeta.params) {
        const v = params[pm.name]
        if (v === undefined) continue
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
        headers: {
          'Content-Type': 'application/x-luxo',
          'X-Luxo-Mode': 'binary',
          ...this.headers,
        },
        body: enc.bytes(),
      })
    } catch (e) {
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
    }

    if (!resp.ok) {
      // Error responses are always JSON
      try {
        const json = await resp.json()
        throw new LuxoError(json.error ?? 'Error', json.code ?? resp.status, json.message ?? '', json.traceId)
      } catch (e) {
        if (e instanceof LuxoError) throw e
        throw new LuxoError('Error', resp.status, `HTTP ${resp.status}`)
      }
    }

    const buf = new Uint8Array(await resp.arrayBuffer())
    // Return raw binary data — generated client decodes with ReadLuxo
    return buf as unknown as T
  }

  /** Update authorization token */
  setToken(token: string): void {
    this.headers['Authorization'] = `Bearer ${token}`
  }

  /** Switch transport mode at runtime */
  setMode(mode: TransportMode): void {
    this.mode = mode
  }
}
