const jsonHeaders = Object.freeze({ 'content-type': 'application/json', 'x-luxo-mode': 'json' })
const binaryHeaders = Object.freeze({ 'content-type': 'application/x-luxo', 'x-luxo-mode': 'binary' })
type Snapshot = Readonly<Record<string, string>>

/** Normalize once; every request receives an immutable snapshot for its own mode. */
export class HTTPHeaders {
  json: Snapshot
  binary: Snapshot

  constructor(custom?: Record<string, string>, token?: string) {
    if (!custom && !token) {
      this.json = jsonHeaders
      this.binary = binaryHeaders
      return
    }
    const headers: Record<string, string> = custom ? Object.fromEntries(new Headers(custom)) : {}
    if (token) headers.authorization = `Bearer ${token}`
    this.json = Object.freeze({ ...headers, ...jsonHeaders })
    this.binary = Object.freeze({ ...headers, ...binaryHeaders })
  }

  setToken(token: string | null): void {
    this.json = withToken(this.json, token)
    this.binary = withToken(this.binary, token)
  }
}

function withToken(headers: Snapshot, token: string | null): Snapshot {
  const next = { ...headers }
  if (token) next.authorization = `Bearer ${token}`
  else delete next.authorization
  return Object.freeze(next)
}
