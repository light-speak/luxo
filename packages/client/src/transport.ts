import { LuxoError } from './error'

/** Transport interface — implement for different runtimes */
export interface Transport {
  call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T>
}

/** Fetch-based transport for Web / React Native / Node.js 18+ */
export class FetchTransport implements Transport {
  private headers: Record<string, string> = {}

  constructor(
    private endpoint: string,
    options?: { token?: string; headers?: Record<string, string> },
  ) {
    if (options?.headers) {
      this.headers = { ...options.headers }
    }
    if (options?.token) {
      this.headers['Authorization'] = `Bearer ${options.token}`
    }
  }

  async call<T = unknown>(api: string, params?: Record<string, unknown>): Promise<T> {
    const body: Record<string, unknown> = { $api: api }
    if (params) {
      Object.assign(body, params)
    }

    const resp = await fetch(this.endpoint, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...this.headers,
      },
      body: JSON.stringify(body),
    })

    const json = await resp.json()

    if (json.error) {
      throw new LuxoError(json.error, json.code, json.message, json.traceId)
    }

    return json.data as T
  }

  /** Update authorization token */
  setToken(token: string): void {
    this.headers['Authorization'] = `Bearer ${token}`
  }
}
