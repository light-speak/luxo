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
          const json = res.data as Record<string, unknown>
          if (json.error) {
            reject(new LuxoError(
              json.error as string,
              json.code as number,
              json.message as string,
              json.traceId as string,
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
