import type { Transport, TransportMode, APISchema } from './transport'
import { LuxoError } from './error'
import { Encoder } from './codec'

declare const wx: {
  request(options: {
    url: string
    method: string
    header: Record<string, string>
    data: string | ArrayBuffer
    responseType?: 'text' | 'arraybuffer'
    success: (res: { statusCode: number; data: unknown }) => void
    fail: (err: { errMsg: string }) => void
  }): void
}

/** WeChat Mini Program transport (wx.request) — supports JSON and binary modes */
export class WxTransport implements Transport {
  private headers: Record<string, string> = {}
  private mode: TransportMode
  private schema: Record<string, APISchema>

  constructor(
    private endpoint: string,
    options?: { token?: string; mode?: TransportMode; schema?: Record<string, APISchema> },
  ) {
    this.mode = options?.mode ?? 'json'
    this.schema = options?.schema ?? {}
    if (options?.token) {
      this.headers['Authorization'] = `Bearer ${options.token}`
    }
  }

  call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T> {
    if (this.mode === 'binary') {
      return this.callBinary<T>(api, params)
    }
    return this.callJSON<T>(api, params)
  }

  private callJSON<T>(api: string, params?: Record<string, unknown>): Promise<T> {
    const body: Record<string, unknown> = { $api: api }
    if (params) Object.assign(body, params)

    return new Promise((resolve, reject) => {
      wx.request({
        url: this.endpoint,
        method: 'POST',
        header: { 'Content-Type': 'application/json', ...this.headers },
        data: JSON.stringify(body),
        success(res) {
          let json: Record<string, unknown>
          if (typeof res.data === 'string') {
            try { json = JSON.parse(res.data) } catch {
              reject(new LuxoError('ParseError', res.statusCode, 'invalid JSON response'))
              return
            }
          } else if (res.data && typeof res.data === 'object') {
            json = res.data as Record<string, unknown>
          } else {
            reject(new LuxoError('ParseError', res.statusCode, 'unexpected response type'))
            return
          }

          if (json.error) {
            reject(new LuxoError(String(json.error), Number(json.code ?? 0), String(json.message ?? ''), json.traceId != null ? String(json.traceId) : undefined))
            return
          }
          resolve(json.data as T)
        },
        fail(err) {
          reject(new LuxoError('NetworkError', 0, err.errMsg))
        },
      })
    })
  }

  private callBinary<T>(api: string, params?: Record<string, unknown>): Promise<T> {
    const apiMeta = this.schema[api]
    if (!apiMeta) {
      return Promise.reject(new LuxoError('ConfigError', 0, `no schema for API "${api}" — binary mode requires schema`))
    }

    const enc = new Encoder()
    enc.writeVarint(apiMeta.id)
    enc.writeVarint(0) // field mask = 0

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

    return new Promise((resolve, reject) => {
      wx.request({
        url: this.endpoint,
        method: 'POST',
        header: { 'Content-Type': 'application/x-luxo', 'X-Luxo-Mode': 'binary', ...this.headers },
        data: enc.bytes().buffer,
        responseType: 'arraybuffer',
        success(res) {
          if (res.statusCode !== 200) {
            reject(new LuxoError('Error', res.statusCode, `HTTP ${res.statusCode}`))
            return
          }
          resolve(new Uint8Array(res.data as ArrayBuffer) as unknown as T)
        },
        fail(err) {
          reject(new LuxoError('NetworkError', 0, err.errMsg))
        },
      })
    })
  }

  setToken(token: string | null): void {
    if (token) {
      this.headers['Authorization'] = `Bearer ${token}`
      return
    }
    delete this.headers['Authorization']
  }

  setMode(mode: TransportMode): void {
    this.mode = mode
  }
}
