import type { Transport } from './transport'
import { LuxoError } from './error'

declare const wx: {
  request(options: {
    url: string
    method: string
    header: Record<string, string>
    data: string
    success: (res: { statusCode: number; data: unknown }) => void
    fail: (err: { errMsg: string }) => void
  }): void
}

/** WeChat Mini Program transport (wx.request) */
export class WxTransport implements Transport {
  private headers: Record<string, string> = {}

  constructor(
    private endpoint: string,
    options?: { token?: string },
  ) {
    if (options?.token) {
      this.headers['Authorization'] = `Bearer ${options.token}`
    }
  }

  call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T> {
    const body: Record<string, unknown> = { $api: api }
    if (params) {
      Object.assign(body, params)
    }

    return new Promise((resolve, reject) => {
      wx.request({
        url: this.endpoint,
        method: 'POST',
        header: { 'Content-Type': 'application/json', ...this.headers },
        data: JSON.stringify(body),
        success(res) {
          // wx.request may return string or object depending on dataType
          let json: Record<string, unknown>
          if (typeof res.data === 'string') {
            try {
              json = JSON.parse(res.data)
            } catch {
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
            reject(new LuxoError(
              String(json.error),
              Number(json.code ?? 0),
              String(json.message ?? ''),
              json.traceId != null ? String(json.traceId) : undefined,
            ))
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

  setToken(token: string): void {
    this.headers['Authorization'] = `Bearer ${token}`
  }
}
