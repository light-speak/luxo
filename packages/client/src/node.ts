import { Buffer } from 'node:buffer'
import { connect } from 'node:http2'
import type { ClientHttp2Session } from 'node:http2'
import { isIP } from 'node:net'
import { FetchTransport } from './transport'
import type { HTTPResponse, TransportOptions } from './transport'
import { HTTP2Exchange } from './node-http2-exchange'

export interface NodeHTTP2TLSOptions {
  ca?: string | Uint8Array
  cert?: string | Uint8Array
  key?: string | Uint8Array
  passphrase?: string
}

export interface NodeHTTP2Options extends TransportOptions {
  tls?: NodeHTTP2TLSOptions
  /** Per transport, with no unbounded waiting queue. Default: 16. */
  maxConcurrentRequests?: number
  /** Encoded body limits. Defaults: 2 MiB in each direction. */
  maxRequestBytes?: number
  maxResponseBytes?: number
}

/** Native Node HTTP/2 I/O; protocol, auth, cancellation and refresh remain shared. */
export class NodeHTTP2Transport extends FetchTransport {
  private readonly origin: string
  private readonly path: string
  private readonly servername: string
  private readonly maximum: number
  private readonly requestLimit: number
  private readonly responseLimit: number
  private readonly tls: { ca?: string | Buffer, cert?: string | Buffer, key?: string | Buffer, passphrase?: string }
  private readonly pending = new Set<HTTP2Exchange>()
  private session?: ClientHttp2Session
  private ready = false
  private peerMaximum = 0
  private draining = false
  private closed = false

  constructor(private readonly address: string, options: NodeHTTP2Options = {}) {
    super(address, options)
    const url = new URL(address)
    if (url.protocol !== 'https:' || url.username || url.password) throw new Error('Node HTTP/2 requires an HTTPS endpoint without URL credentials')
    this.origin = url.origin
    this.path = url.pathname + url.search
    const hostname = url.hostname.replace(/^\[|\]$/g, '')
    this.servername = isIP(hostname) ? '' : hostname
    this.maximum = positiveLimit(options.maxConcurrentRequests ?? 16)
    this.requestLimit = positiveLimit(options.maxRequestBytes ?? 2 * 1024 * 1024)
    this.responseLimit = positiveLimit(options.maxResponseBytes ?? 2 * 1024 * 1024)
    this.tls = { ca: material(options.tls?.ca), cert: material(options.tls?.cert),
      key: material(options.tls?.key), passphrase: options.tls?.passphrase }
  }

  close(): void {
    if (this.closed) return
    this.closed = true
    for (const exchange of this.pending) exchange.fail(new DOMException('Node HTTP/2 transport closed', 'AbortError'))
    this.session?.destroy()
    this.session = undefined
  }

  protected override executeRequest(endpoint: string, init: RequestInit): Promise<HTTPResponse> {
    try {
      if (this.closed) throw new Error('Node HTTP/2 transport closed')
      if (endpoint !== this.address) throw new Error('Node HTTP/2 endpoint mismatch')
      if (this.pending.size >= this.maximum) throw new Error('Node HTTP/2 request capacity exceeded')
      const body = this.requestBody(init)
      if (!init.headers || init.headers instanceof Headers || Array.isArray(init.headers)) throw new Error('normalized request headers required')
      const session = this.connection()
      if (this.pending.size >= this.peerMaximum) throw new Error('HTTP/2 peer request capacity exceeded')
      const stream = session.request({ ...init.headers, ':method': 'POST', ':path': this.path })
      const exchange = new HTTP2Exchange(stream, init.signal, this.responseLimit, () => this.release(exchange), () => this.retire())
      this.pending.add(exchange)
      exchange.start(body)
      return exchange.response
    } catch (error) { return Promise.reject(error) }
  }

  private requestBody(init: RequestInit): string | Uint8Array | ArrayBuffer {
    const body = init.body
    if (init.method !== 'POST' || !(typeof body === 'string' || body instanceof Uint8Array || body instanceof ArrayBuffer)) {
      throw new Error('unsupported Node HTTP/2 request')
    }
    const bytes = typeof body === 'string' ? Buffer.byteLength(body) : body.byteLength
    if (bytes > this.requestLimit) throw new Error('Node HTTP/2 request body exceeds limit')
    return body
  }

  private connection(): ClientHttp2Session {
    if (this.draining && this.pending.size) throw new Error('Node HTTP/2 connection is draining')
    if (this.session && !this.session.closed && !this.session.destroyed && !this.draining) return this.session
    if (this.pending.size) throw new Error('previous HTTP/2 connection is terminating')
    const session = connect(this.origin, { ...this.tls, rejectUnauthorized: true,
      servername: this.servername, minVersion: 'TLSv1.2',
      maxSessionMemory: 10, settings: { enablePush: false } })
    this.session = session
    this.draining = false
    this.peerMaximum = this.maximum
    this.ready = false
    session.on('connect', () => {
      if (session.alpnProtocol !== 'h2') session.destroy(new Error('TLS peer did not negotiate HTTP/2'))
    })
    session.on('remoteSettings', settings => {
      if (this.session !== session) return
      this.ready = true
      this.peerMaximum = Math.min(this.maximum, settings.maxConcurrentStreams ?? this.maximum)
    })
    session.on('error', () => {}) // Outstanding streams receive their own terminal error.
    session.on('goaway', () => {
      if (this.session !== session) return
      this.draining = true
      if (!this.pending.size) session.close()
    })
    return session
  }

  private retire(): void {
    if (!this.session || (this.ready && !this.session.connecting)) return
    // Pending streams cannot emit close until an unfinished TLS session is destroyed.
    for (const exchange of this.pending) if (!exchange.closing) return
    this.session.destroy()
  }

  private release(exchange: HTTP2Exchange): void {
    this.pending.delete(exchange)
    if (!this.pending.size && this.session && (this.session.connecting || !this.ready)) {
      this.session.destroy()
      this.session = undefined
    } else if (this.draining && !this.pending.size) this.session?.close()
  }
}

function positiveLimit(value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0 || value > 0x7fffffff) throw new Error('invalid Node HTTP/2 limit')
  return value
}

function material(value: string | Uint8Array | undefined): string | Buffer | undefined {
  return value instanceof Uint8Array ? Buffer.from(value) : value
}
