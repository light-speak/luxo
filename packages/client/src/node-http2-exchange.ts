import { constants } from 'node:http2'
import type { ClientHttp2Stream, IncomingHttpHeaders, IncomingHttpStatusHeader } from 'node:http2'
import type { HTTPResponse } from './transport'
import { HTTP2Body } from './node-http2-body'

export class HTTP2Exchange implements HTTPResponse {
  readonly response: Promise<HTTPResponse>
  private responseStatus = 0
  private resolveResponse!: (value: HTTPResponse) => void
  private rejectResponse!: (error: Error) => void
  private collector?: HTTP2Body
  private data?: ArrayBuffer
  private error?: Error
  private pendingBody?: Promise<ArrayBuffer>
  private resolveBody?: (value: ArrayBuffer) => void
  private rejectBody?: (error: Error) => void
  private done = false
  private settled = false
  private readonly abort = () => this.fail(new DOMException('request cancelled', 'AbortError'))

  constructor(private readonly stream: ClientHttp2Stream, private readonly signal: AbortSignal | null | undefined,
    private readonly limit: number, private readonly release: () => void, private readonly retire: () => void) {
    this.response = new Promise((resolve, reject) => { this.resolveResponse = resolve; this.rejectResponse = reject })
    stream.on('response', headers => this.receiveHeaders(headers))
    stream.on('data', (chunk: Uint8Array) => this.append(chunk))
    stream.on('end', () => this.finish())
    stream.on('error', error => this.fail(error))
    stream.on('aborted', () => this.fail(new Error('HTTP/2 response aborted')))
    stream.on('close', () => this.closed())
    signal?.addEventListener('abort', this.abort, { once: true })
  }

  get status(): number { return this.responseStatus }
  get closing(): boolean { return this.done }

  start(body: string | Uint8Array | ArrayBuffer): void {
    if (this.signal?.aborted) { this.abort(); return }
    try { this.stream.end(body instanceof ArrayBuffer ? new Uint8Array(body) : body) }
    catch (error) { this.fail(error instanceof Error ? error : new Error(String(error))) }
  }

  arrayBuffer(): Promise<ArrayBuffer> {
    if (this.settled && this.error) return Promise.reject(this.error)
    if (this.settled && this.data) return Promise.resolve(this.data)
    this.pendingBody ??= new Promise((resolve, reject) => { this.resolveBody = resolve; this.rejectBody = reject })
    return this.pendingBody
  }

  fail(error: Error): void {
    if (this.done) return
    this.error = error
    this.collector?.clear()
    this.complete(constants.NGHTTP2_CANCEL)
  }

  private receiveHeaders(headers: IncomingHttpHeaders & IncomingHttpStatusHeader): void {
    if (this.done) return
    const status = headers[':status']
    const length = headers['content-length']
    if (!Number.isInteger(status) || status === undefined || status < 200 || status > 599) {
      this.fail(new Error('invalid HTTP/2 response status')); return
    }
    // Report body errors after resolving the headers, preserving ParseError semantics.
    this.responseStatus = status
    this.resolveResponse(this)
    if (length !== undefined && (typeof length !== 'string' || !/^\d+$/.test(length) ||
        !Number.isSafeInteger(Number(length)) || Number(length) > this.limit)) {
      this.fail(new Error('invalid or oversized HTTP/2 Content-Length')); return
    }
    this.collector = new HTTP2Body(this.limit, length === undefined ? undefined : Number(length))
  }

  private append(chunk: Uint8Array): void {
    if (this.done) return
    try {
      if (!this.collector) throw new Error('HTTP/2 body received before headers')
      this.collector.append(chunk)
    } catch (error) { this.fail(error instanceof Error ? error : new Error(String(error))) }
  }

  private finish(): void {
    if (this.done) return
    try {
      if (!this.collector) throw new Error('missing HTTP/2 response headers')
      this.data = this.collector.finish()
      this.complete(constants.NGHTTP2_NO_ERROR)
    } catch (error) { this.fail(error instanceof Error ? error : new Error(String(error))) }
  }

  private complete(code: number): void {
    this.done = true
    this.signal?.removeEventListener('abort', this.abort)
    this.stream.close(code)
    this.retire()
  }

  private closed(): void {
    if (this.settled) return
    if (!this.done) this.fail(new Error('HTTP/2 response closed before completion'))
    this.settled = true
    this.release()
    if (this.error) {
      this.rejectResponse(this.error)
      this.rejectBody?.(this.error)
    } else if (this.data) this.resolveBody?.(this.data)
  }
}
