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

    let resp: Response
    try {
      resp = await fetch(this.endpoint, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...this.headers,
        },
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

  /** Update authorization token */
  setToken(token: string): void {
    this.headers['Authorization'] = `Bearer ${token}`
  }
}
