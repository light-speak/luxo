import { LuxoError } from './error'
import { Decoder, Encoder, fieldMaskSet } from './codec'

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder()

const BinaryFrame = {
  callRequest: 0x01,
  callSuccess: 0x02,
  callError: 0x03,
  subscribe: 0x04,
  unsubscribe: 0x05,
  stream: 0x06,
  subscribeSuccess: 0x07,
  subscribeError: 0x08,
} as const

const binaryFiltersFieldID = 0x7ffffffe
const binarySortersFieldID = 0x7fffffff
const filterOperatorIDs: Record<string, number> = {
  eq: 1,
  ne: 2,
  gt: 3,
  gte: 4,
  lt: 5,
  lte: 6,
  contains: 7,
  startswith: 8,
  endswith: 9,
  match: 10,
}

export type TransportMode = 'json' | 'binary'

function toArrayBuffer(bytes: Uint8Array): ArrayBuffer {
  return new Uint8Array(bytes).buffer
}

function toUnixSeconds(value: unknown): number {
	if (typeof value !== 'string') {
		throw new LuxoError('ConfigError', 0, 'DateTime parameters require an RFC3339 string')
	}
	const milliseconds = Date.parse(value)
	if (!Number.isFinite(milliseconds)) {
		throw new LuxoError('ConfigError', 0, 'DateTime parameters require an RFC3339 string')
	}
	return Math.floor(milliseconds / 1000)
}

/** Encode a single API param (scalar or list) to binary using the schema metadata. */
export function encodeParam(
  enc: Encoder,
	pm: { fieldID: number; type: string; isList?: boolean; nullable?: boolean },
  v: unknown,
): void {
	enc.writeVarint(pm.fieldID)
	if (pm.nullable) {
		if (v === null) {
			enc.writeBool(false)
			return
		}
		enc.writeBool(true)
	} else if (v === null) {
		throw new LuxoError('ConfigError', 0, `parameter field ${pm.fieldID} is not nullable`)
	}
  if (pm.isList) {
    const arr = v as unknown[]
		enc.writeVarint(arr.length)
    switch (pm.type) {
      case 'Int': case 'Duration':
		for (const value of arr as number[]) enc.writeSvarint(value); break
      case 'DateTime':
		for (const value of arr) enc.writeSvarint(toUnixSeconds(value)); break
      case 'Float':
		for (const value of arr as number[]) enc.writeFixed64(value); break
      case 'String': case 'Enum': case 'Decimal':
		for (const value of arr as string[]) enc.writeString(value); break
      case 'Boolean':
		for (const value of arr as boolean[]) enc.writeBool(value); break
      case 'UUID':
		for (const value of arr as string[]) enc.writeUUID(value); break
	  case 'Bytes':
		for (const value of arr) enc.writeBytes(toUint8Array(value)); break
	  case 'JSON':
		for (const value of arr) enc.writeBytes(encodeJSONParam(value)); break
	  default:
		throw new LuxoError('ConfigError', 0, `unsupported binary list param type: ${pm.type}`)
    }
    return
  }
  switch (pm.type) {
    case 'Int': case 'Duration':
	  enc.writeSvarint(v as number); break
    case 'Float':
	  enc.writeFixed64(v as number); break
    case 'String': case 'Enum': case 'Decimal':
	  enc.writeString(v as string); break
    case 'Boolean':
	  enc.writeBool(v as boolean); break
    case 'UUID':
	  enc.writeUUID(v as string); break
    case 'DateTime': {
	  enc.writeSvarint(toUnixSeconds(v)); break
	}
	case 'Bytes':
	  enc.writeBytes(toUint8Array(v)); break
	case 'JSON':
	  enc.writeBytes(encodeJSONParam(v)); break
	default:
	  throw new LuxoError('ConfigError', 0, `unsupported binary param type: ${pm.type}`)
  }
}

function encodeJSONParam(value: unknown): Uint8Array {
	const encoded = JSON.stringify(value)
	if (encoded === undefined) {
		throw new LuxoError('ConfigError', 0, 'JSON parameter is not serializable')
	}
	return textEncoder.encode(encoded)
}

function toUint8Array(value: unknown): Uint8Array {
  if (value instanceof Uint8Array) return value
  if (value instanceof ArrayBuffer) return new Uint8Array(value)
  throw new LuxoError('ConfigError', 0, 'Bytes parameters require Uint8Array or ArrayBuffer')
}

/** API schema metadata for binary encoding */
export interface APISchema {
  id: number
	params?: Array<{ fieldID: number; name: string; type: string; isList?: boolean; nullable?: boolean }>
  fields?: Record<string, SelectionFieldSchema>
  types?: Record<string, Record<string, SelectionFieldSchema>>
}

export interface SelectionFieldSchema {
  fieldID: number
  typeName?: string
}

interface SelectedField {
  name: string
  children?: SelectedField[]
}

class SelectionParser {
  private offset = 0

  constructor(private readonly input: string) {}

  parse(): SelectedField[] {
    const fields = this.parseList(false, 0)
    this.skipSpaces()
    if (this.offset !== this.input.length) this.fail(`unexpected '${this.input[this.offset]}'`)
    return fields
  }

  private parseList(nested: boolean, depth: number): SelectedField[] {
    if (depth >= 32) this.fail('selection depth exceeds 32')
    const fields: SelectedField[] = []
    const names = new Set<string>()
    while (true) {
      this.skipSpaces()
      if (this.offset >= this.input.length || (nested && this.input[this.offset] === '}')) break
      const name = this.readIdentifier()
      if (!name) this.fail('expected field name')
      if (names.has(name)) this.fail(`duplicate field '${name}'`)
      names.add(name)
      this.skipSpaces()
      let children: SelectedField[] | undefined
      if (this.input[this.offset] === '{') {
        this.offset++
        children = this.parseList(true, depth + 1)
        if (children.length === 0) this.fail(`empty selection for '${name}'`)
        this.skipSpaces()
        if (this.input[this.offset] !== '}') this.fail(`missing '}' for '${name}'`)
        this.offset++
      }
      fields.push(children ? { name, children } : { name })
      this.skipSpaces()
      if (this.input[this.offset] !== ',') break
      this.offset++
    }
    return fields
  }

  private readIdentifier(): string {
    const start = this.offset
    const first = this.input.charCodeAt(this.offset)
    if (!isIdentifierStart(first)) return ''
    this.offset++
    while (isIdentifierPart(this.input.charCodeAt(this.offset))) this.offset++
    return this.input.slice(start, this.offset)
  }

  private skipSpaces(): void {
    while (/\s/.test(this.input[this.offset] ?? '')) this.offset++
  }

  private fail(message: string): never {
    throw new LuxoError('ConfigError', 0, `${message} at position ${this.offset}`)
  }
}

function isIdentifierStart(code: number): boolean {
  return code === 95 || (code >= 65 && code <= 90) || (code >= 97 && code <= 122)
}

function isIdentifierPart(code: number): boolean {
  return isIdentifierStart(code) || (code >= 48 && code <= 57)
}

function encodeSelectionNode(
  selected: SelectedField[],
  fields: Record<string, SelectionFieldSchema>,
  types: Record<string, Record<string, SelectionFieldSchema>>,
): Uint8Array {
  let mask: Uint8Array<ArrayBufferLike> = new Uint8Array(0)
  const children: Array<{ fieldID: number; data: Uint8Array }> = []
  for (const field of selected) {
    const meta = fields[field.name]
    if (!meta) throw new LuxoError('ConfigError', 0, `unknown selected field: ${field.name}`)
    mask = fieldMaskSet(mask, meta.fieldID)
    if (!field.children) continue
    const nestedFields = meta.typeName ? types[meta.typeName] : undefined
    if (!nestedFields) throw new LuxoError('ConfigError', 0, `field ${field.name} does not support nested selection`)
    children.push({ fieldID: meta.fieldID, data: encodeSelectionNode(field.children, nestedFields, types) })
  }
  const node = new Encoder()
  node.writeVarint(mask.length)
  node.writeRawBytes(mask)
  children.sort((a, b) => a.fieldID - b.fieldID)
  for (const child of children) {
    node.writeVarint(child.fieldID)
    node.writeVarint(child.data.length)
    node.writeRawBytes(child.data)
  }
  return node.bytes()
}

function encodeFilters(enc: Encoder, value: unknown): void {
  if (!Array.isArray(value) || value.length > 1000) {
    throw new LuxoError('ConfigError', 0, '$filters must be an array with at most 1000 entries')
  }
  enc.writeVarint(binaryFiltersFieldID)
  enc.writeVarint(value.length)
  for (const [index, item] of value.entries()) {
    if (!isRecord(item) || typeof item.field !== 'string' || item.field === '' ||
        typeof item.op !== 'string' || !filterOperatorIDs[item.op] || !isFilterValue(item.value)) {
      throw new LuxoError('ConfigError', 0, `invalid $filters entry at index ${index}`)
    }
    enc.writeString(item.field)
    enc.writeVarint(filterOperatorIDs[item.op])
    enc.writeString(String(item.value))
  }
}

function encodeSorters(enc: Encoder, value: unknown): void {
  if (!Array.isArray(value) || value.length > 100) {
    throw new LuxoError('ConfigError', 0, '$sorters must be an array with at most 100 entries')
  }
  enc.writeVarint(binarySortersFieldID)
  enc.writeVarint(value.length)
  for (const [index, item] of value.entries()) {
    if (!isRecord(item) || typeof item.field !== 'string' || item.field === '' ||
        (item.order !== 'asc' && item.order !== 'desc')) {
      throw new LuxoError('ConfigError', 0, `invalid $sorters entry at index ${index}`)
    }
    enc.writeString(item.field)
    enc.writeBool(item.order === 'desc')
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function isFilterValue(value: unknown): value is string | number | boolean {
  return typeof value === 'string' || typeof value === 'boolean' ||
    (typeof value === 'number' && Number.isFinite(value))
}

export function writeFieldMask(enc: Encoder, meta: APISchema, params?: Record<string, unknown>): void {
  const select = params?.$select
  if (typeof select !== 'string' || select.trim() === '' || !meta.fields) {
    enc.writeVarint(0)
    return
  }
  const selected = new SelectionParser(select).parse()
  const mask = encodeSelectionNode(selected, meta.fields, meta.types ?? {})
  enc.writeVarint(mask.length)
  enc.writeRawBytes(mask)
}

export function encodeBinaryBody(meta: APISchema, params?: Record<string, unknown>): Uint8Array {
  const enc = new Encoder()
  enc.writeVarint(meta.id)
  writeFieldMask(enc, meta, params)
  if (params && meta.params) {
    for (const param of meta.params) {
      const value = params[param.name]
		if (value === undefined) continue
      encodeParam(enc, param, value)
    }
  }
  if (params && '$filters' in params) encodeFilters(enc, params.$filters)
  if (params && '$sorters' in params) encodeSorters(enc, params.$sorters)
  enc.writeEnd()
  return enc.bytes()
}

export function decodeBinaryError(data: Uint8Array, statusCode: number): LuxoError {
  const dec = new Decoder(data)
  let code = statusCode
  let name = 'Error'
  let message = `HTTP ${statusCode}`
  let traceId: string | undefined
  let errorData: unknown
  let cause: string | undefined
  let seen = 0
  let ended = false
  while (dec.remaining > 0) {
    if (!dec.nextField()) {
      ended = dec.error === null
      break
    }
    switch (dec.fieldID) {
      case 1: code = dec.readInt(); seen |= 1; break
      case 2: name = dec.readString(); seen |= 2; break
      case 3: message = dec.readString(); seen |= 4; break
      case 4: traceId = dec.readString(); break
      case 5: {
        const raw = textDecoder.decode(dec.readBytes())
        try { errorData = JSON.parse(raw) } catch {
          return binaryParseError(statusCode, 'invalid JSON data')
        }
        break
      }
      case 6: cause = dec.readString(); break
      default: return binaryParseError(statusCode, `unknown binary error field ${dec.fieldID}`)
    }
  }
  if (dec.error !== null) return binaryParseError(statusCode, dec.error)
  if (!ended) return binaryParseError(statusCode, 'missing end marker')
  if (dec.remaining !== 0) return binaryParseError(statusCode, 'trailing bytes')
  if (seen !== 7) return binaryParseError(statusCode, 'missing required fields')
  return new LuxoError(name, code, message, traceId, errorData, cause)
}

function binaryParseError(statusCode: number, message: string): LuxoError {
  return new LuxoError('ParseError', statusCode, `invalid binary error response: ${message}`)
}

/** Transport interface — implemented by HTTP, WebSocket, etc. */
export interface Transport {
  call(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown>
  subscribe(api: string, params: Record<string, unknown>, onData: (data: unknown) => void): Promise<() => void>
  setSchema(schema: Record<string, APISchema>): void
  setMode(mode: TransportMode): void
  setToken(token: string | null): void
  close?(): void
}

export interface CallOptions {
  signal?: AbortSignal
}

/** Shared transport options */
export interface TransportOptions {
  token?: string
  headers?: Record<string, string>
  mode?: TransportMode
  /** Request timeout in milliseconds (default: 30000) */
  timeout?: number
  /** Called when token expires (401). Return new token to auto-retry. */
  onTokenExpired?: () => Promise<string | null>
  /** Optional safe request observer. Parameter values and wire payloads are never exposed. */
  observer?: TransportObserver
}

export interface TransportEvent {
  api: string
  mode: TransportMode
  status: 'success' | 'error'
  durationMs: number
  responseBytes?: number
  statusCode?: number
  errorCode?: string
  traceId?: string
}

export type TransportObserver = (event: TransportEvent) => void

interface ResponseScope {
  response: Response
  close: () => void
  abortError: (error: unknown) => LuxoError | undefined
}

function notifyObserver(observer: TransportObserver, event: TransportEvent): void {
  try {
    observer(event)
  } catch {
    // Observability must never affect request behavior.
  }
}

// --- Fetch Transport (HTTP/2) ---

/** Fetch-based transport for Web / Node.js 18+.
 *  Uses HTTP/2 when available — single connection, multiplexed requests. */
export class FetchTransport implements Transport {
  private headers: Record<string, string> = {}
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private timeout: number
  private onTokenExpired?: () => Promise<string | null>
  private readonly observer?: TransportObserver

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    this.timeout = options?.timeout ?? 30000
    this.onTokenExpired = options?.onTokenExpired
    this.observer = options?.observer
    if (options?.headers) this.headers = { ...options.headers }
    if (options?.token) this.headers['Authorization'] = `Bearer ${options.token}`
  }

  setSchema(schema: Record<string, APISchema>): void { this.schema = schema }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void {
    if (token) {
      this.headers['Authorization'] = `Bearer ${token}`
      return
    }
    delete this.headers['Authorization']
  }

  call(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatch(api, params, options)
    return this.callObserved(api, params, options)
  }

  private dispatch(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    if (options?.signal?.aborted) {
      return Promise.reject(new LuxoError('CancelledError', 0, 'request cancelled'))
    }
    return this.mode === 'binary'
      ? this.binaryCall(api, params, options)
      : this.jsonCall(api, params, options)
  }

  private async callObserved(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    const start = performance.now()
    const mode = this.mode
    try {
      const result = await this.dispatch(api, params, options)
      notifyObserver(this.observer!, {
        api,
        mode,
        status: 'success',
        durationMs: performance.now() - start,
        responseBytes: result instanceof Uint8Array ? result.length : undefined,
      })
      return result
    } catch (e) {
      const error = e instanceof LuxoError ? e : undefined
      notifyObserver(this.observer!, {
        api,
        mode,
        status: 'error',
        durationMs: performance.now() - start,
        statusCode: error?.code,
        errorCode: error?.error,
        traceId: error?.traceId,
      })
      throw e
    }
  }

  async subscribe(_api: string, _params: Record<string, unknown>, _onData: (data: unknown) => void): Promise<() => void> {
    throw new LuxoError('ConfigError', 0, 'subscriptions require a WebSocket endpoint')
  }

  private async openRequest(init: RequestInit, signal?: AbortSignal): Promise<ResponseScope> {
    if (signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const controller = new AbortController()
    let timedOut = false
    let closed = false
    const cancel = () => controller.abort()
    signal?.addEventListener('abort', cancel, { once: true })
    const timer = setTimeout(() => {
      timedOut = true
      controller.abort()
    }, this.timeout)
    const close = () => {
      if (closed) return
      closed = true
      clearTimeout(timer)
      signal?.removeEventListener('abort', cancel)
    }
    const abortError = (error: unknown) => {
      if (!(error instanceof DOMException) || error.name !== 'AbortError') return undefined
      if (timedOut) return new LuxoError('TimeoutError', 0, `request timed out after ${this.timeout}ms`)
      if (signal?.aborted) return new LuxoError('CancelledError', 0, 'request cancelled')
      return new LuxoError('CancelledError', 0, 'request cancelled')
    }
    try {
      const response = await fetch(this.endpoint, { ...init, signal: controller.signal })
      return { response, close, abortError }
    } catch (e) {
      const aborted = abortError(e)
      close()
      if (aborted) throw aborted
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
    }
  }

  private async jsonCall(
    api: string,
    params?: Record<string, unknown>,
    options?: CallOptions,
    allowRefresh = true,
  ): Promise<unknown> {
    const body: Record<string, unknown> = { $api: api }
    if (params) Object.assign(body, params)

    const scope = await this.openRequest({
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...this.headers },
      body: JSON.stringify(body),
    }, options?.signal)
    const resp = scope.response

    try {
      // 401 auto-refresh: call onTokenExpired, get new token, retry once
      if (resp.status === 401 && allowRefresh && this.onTokenExpired) {
        scope.close()
        const newToken = await this.onTokenExpired()
        if (newToken) {
          this.setToken(newToken)
          return this.jsonCall(api, params, options, false)
        }
      }

      let json: Record<string, unknown>
      try { json = await resp.json() } catch (error) {
        const aborted = scope.abortError(error)
        if (aborted) throw aborted
        throw new LuxoError('ParseError', resp.status, `invalid JSON (HTTP ${resp.status})`)
      }

      if (json.error) {
		throw new LuxoError(
		  json.error as string,
		  (json.code as number) ?? resp.status,
		  (json.message as string) ?? '',
		  json.traceId as string | undefined,
		  json.data,
		  typeof json.cause === 'string' ? json.cause : undefined,
		)
      }
      return json.data
    } finally {
      scope.close()
    }
  }

  private async binaryCall(
    api: string,
    params?: Record<string, unknown>,
    options?: CallOptions,
    allowRefresh = true,
  ): Promise<unknown> {
    const scope = await this.openRequest({
      method: 'POST',
      headers: { 'Content-Type': 'application/x-luxo', 'X-Luxo-Mode': 'binary', ...this.headers },
      body: this.encodeBinaryRequest(api, params),
    }, options?.signal)
    const resp = scope.response

    try {
      if (resp.status === 401 && allowRefresh && this.onTokenExpired) {
        scope.close()
        const newToken = await this.onTokenExpired()
        if (newToken) {
          this.setToken(newToken)
          return this.binaryCall(api, params, options, false)
        }
      }

      return await this.readBinaryResponse(scope)
    } finally {
      scope.close()
    }
  }

  private encodeBinaryRequest(api: string, params?: Record<string, unknown>): ArrayBuffer {
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}" — call setSchema() or use LuxoClient.create()`)
	return toArrayBuffer(encodeBinaryBody(meta, params))
  }

  private async readBinaryResponse(scope: ResponseScope): Promise<Uint8Array> {
    const response = scope.response
    try {
	  const data = new Uint8Array(await response.arrayBuffer())
	  if (response.ok) return data
	  throw decodeBinaryError(data, response.status)
    } catch (error) {
      if (error instanceof LuxoError) throw error
      const aborted = scope.abortError(error)
      if (aborted) throw aborted
      if (!response.ok) throw new LuxoError('Error', response.status, `HTTP ${response.status}`)
      throw error
    }
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
  private pending = new Map<number, {
    resolve: (v: unknown) => void
    reject: (e: Error) => void
    cleanup: () => void
  }>()
  private seq = 0
  private connectPromise: Promise<void> | null = null
  private token: string | undefined
  private closed = false
  private reconnectAttempts = 0
  private maxReconnectAttempts = 10
  private reconnectDelay = 1000 // ms, doubles each attempt (exponential backoff)
  private readonly observer?: TransportObserver
  private readonly timeout: number
  private subscriptions = new Map<string, {
    params: Record<string, unknown>
    onData: (data: unknown) => void
    acknowledgement?: {
      resolve: (unsubscribe: () => void) => void
      reject: (error: Error) => void
      cleanup: () => void
    }
  }>()

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    this.token = options?.token
    this.observer = options?.observer
    this.timeout = options?.timeout ?? 30000
  }

  setSchema(schema: Record<string, APISchema>): void { this.schema = schema }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void { this.token = token ?? undefined }

  private connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) return Promise.resolve()
    if (this.connectPromise) return this.connectPromise

    this.connectPromise = new Promise((resolve, reject) => {
      const separator = this.endpoint.includes('?') ? '&' : '?'
      const url = this.token ? `${this.endpoint}${separator}token=${encodeURIComponent(this.token)}` : this.endpoint
      const ws = new WebSocket(url)
      ws.binaryType = 'arraybuffer'

      ws.onopen = () => {
        this.ws = ws
        this.connectPromise = null
        this.reconnectAttempts = 0
        for (const [api, subscription] of this.subscriptions) {
          this.sendSubscription(api, subscription.params)
        }
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
        this.connectPromise = null
        // Reject all pending requests
        for (const [, p] of this.pending) {
          p.cleanup()
          p.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
        }
        this.pending.clear()
        for (const [api, subscription] of this.subscriptions) {
          if (!subscription.acknowledgement) continue
          subscription.acknowledgement.cleanup()
          subscription.acknowledgement.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
          this.subscriptions.delete(api)
        }
        // Auto-reconnect with exponential backoff
        if (!this.closed && this.reconnectAttempts < this.maxReconnectAttempts) {
          const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts)
          this.reconnectAttempts++
          setTimeout(() => { this.connect().catch(() => {}) }, Math.min(delay, 30000))
        }
      }
    })
    return this.connectPromise
  }

  call(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatchCall(api, params, options)
    return this.callObserved(api, params, options)
  }

  private async callObserved(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    const start = performance.now()
    const mode = this.mode
    try {
      const result = await this.dispatchCall(api, params, options)
      notifyObserver(this.observer!, {
        api,
        mode,
        status: 'success',
        durationMs: performance.now() - start,
        responseBytes: result instanceof Uint8Array ? result.length : undefined,
      })
      return result
    } catch (e) {
      const error = e instanceof LuxoError ? e : undefined
      notifyObserver(this.observer!, {
        api,
        mode,
        status: 'error',
        durationMs: performance.now() - start,
        statusCode: error?.code,
        errorCode: error?.error,
        traceId: error?.traceId,
      })
      throw e
    }
  }

  private async dispatchCall(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown> {
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    await this.connect()
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const id = ++this.seq
    return this.createPendingCall(id, api, params, options?.signal)
  }

  private createPendingCall(
    id: number,
    api: string,
    params: Record<string, unknown> | undefined,
    signal: AbortSignal | undefined,
  ): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        if (!this.pending.delete(id)) return
        cleanup()
        reject(new LuxoError('TimeoutError', 0, `request timed out after ${this.timeout}ms`))
      }, this.timeout)
      const cancel = () => {
        if (!this.pending.delete(id)) return
        cleanup()
        reject(new LuxoError('CancelledError', 0, 'request cancelled'))
      }
      const cleanup = () => {
        clearTimeout(timeout)
        signal?.removeEventListener('abort', cancel)
      }
      signal?.addEventListener('abort', cancel, { once: true })
      this.pending.set(id, { resolve, reject, cleanup })
      if (signal?.aborted) { cancel(); return }
      try {
        this.sendCall(id, api, params)
      } catch (error) {
        this.pending.delete(id)
        cleanup()
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  private sendCall(id: number, api: string, params?: Record<string, unknown>): void {
    if (this.mode !== 'binary') {
      this.ws!.send(JSON.stringify({ $id: id, $api: api, ...params }))
      return
    }
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}"`)
    const enc = new Encoder()
	enc.writeRawBytes(new Uint8Array([BinaryFrame.callRequest]))
    enc.writeVarint(id)
	enc.writeRawBytes(encodeBinaryBody(meta, params))
    this.ws!.send(toArrayBuffer(enc.bytes()))
  }

  async subscribe(api: string, params: Record<string, unknown>, onData: (data: unknown) => void): Promise<() => void> {
    if (this.subscriptions.has(api)) {
      throw new LuxoError('ConfigError', 0, `already subscribed to "${api}" on this transport`)
    }
    await this.connect()
    return new Promise<() => void>((resolve, reject) => {
      let acknowledgement: {
        resolve: (unsubscribe: () => void) => void
        reject: (error: Error) => void
        cleanup: () => void
      }
      const timeout = setTimeout(() => {
        const subscription = this.subscriptions.get(api)
        if (subscription?.acknowledgement !== acknowledgement) return
        this.subscriptions.delete(api)
        acknowledgement.cleanup()
        reject(new LuxoError('TimeoutError', 0, `subscription timed out after ${this.timeout}ms`))
      }, this.timeout)
      acknowledgement = { resolve, reject, cleanup: () => clearTimeout(timeout) }
      this.subscriptions.set(api, {
        params,
        onData,
        acknowledgement,
      })
      try {
        this.sendSubscription(api, params)
      } catch (error) {
        this.subscriptions.delete(api)
        acknowledgement.cleanup()
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  private unsubscribe(api: string): void {
    if (!this.subscriptions.delete(api)) return
    if (this.ws?.readyState !== WebSocket.OPEN) return
    if (this.mode === 'binary') {
      const meta = this.schema[api]
      if (!meta) return
      const enc = new Encoder()
      enc.writeRawBytes(new Uint8Array([BinaryFrame.unsubscribe]))
      enc.writeVarint(meta.id)
      this.ws.send(toArrayBuffer(enc.bytes()))
      return
    }
    this.ws.send(JSON.stringify({ $unsub: api }))
  }

  private sendSubscription(api: string, params: Record<string, unknown>): void {
    if (this.mode === 'binary') {
      const meta = this.schema[api]
      if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}"`)
      const enc = new Encoder()
	  enc.writeRawBytes(new Uint8Array([BinaryFrame.subscribe]))
	  enc.writeRawBytes(encodeBinaryBody(meta, params))
      this.ws!.send(toArrayBuffer(enc.bytes()))
      return
    }
    this.ws!.send(JSON.stringify({ $sub: api, ...params }))
  }

  private handleJSONResponse(data: string) {
    try {
      const json = JSON.parse(data)
      if (typeof json.$sub === 'string') {
        this.handleSubscriptionAcknowledgement(json.$sub, json.error
          ? new LuxoError(json.error, json.code ?? 0, json.message ?? '', json.traceId, json.data, json.cause)
          : undefined)
        return
      }
      if (typeof json.$stream === 'string') {
        this.subscriptions.get(json.$stream)?.onData(json.data)
        return
      }
      const id = json.$id as number
      const p = this.pending.get(id)
      if (!p) return
      this.pending.delete(id)
      p.cleanup()

      if (json.error) {
		p.reject(new LuxoError(json.error, json.code ?? 0, json.message ?? '', json.traceId, json.data, json.cause))
      } else {
        p.resolve(json.data)
      }
    } catch { /* malformed message, ignore */ }
  }

  private handleBinaryResponse(data: Uint8Array) {
	if (data[0] === BinaryFrame.subscribeSuccess || data[0] === BinaryFrame.subscribeError) {
	  const { value: apiID, offset } = readVarint(data, 1)
      const api = Object.entries(this.schema).find(([, meta]) => meta.id === apiID)?.[0]
      if (!api) return
      this.handleSubscriptionAcknowledgement(
        api,
        data[0] === BinaryFrame.subscribeError ? decodeBinaryError(data.subarray(offset), 0) : undefined,
      )
      return
    }
	if (data[0] === BinaryFrame.stream) {
	  const { value: apiID, offset } = readVarint(data, 1)
      const api = Object.entries(this.schema).find(([, meta]) => meta.id === apiID)?.[0]
      if (api) this.subscriptions.get(api)?.onData(data.subarray(offset))
      return
    }
	if (data[0] !== BinaryFrame.callSuccess && data[0] !== BinaryFrame.callError) return
	const { value: id, offset: off } = readVarint(data, 1)

    const p = this.pending.get(id)
    if (!p) return
    this.pending.delete(id)
    p.cleanup()
	if (data[0] === BinaryFrame.callError) {
	  p.reject(decodeBinaryError(data.subarray(off), 0))
	  return
	}
	p.resolve(data.subarray(off))
  }

  private handleSubscriptionAcknowledgement(api: string, error?: LuxoError): void {
    const subscription = this.subscriptions.get(api)
    const acknowledgement = subscription?.acknowledgement
    if (!subscription) return
    if (error) {
      this.subscriptions.delete(api)
      acknowledgement?.cleanup()
      acknowledgement?.reject(error)
      return
    }
    if (!acknowledgement) return
    subscription.acknowledgement = undefined
    acknowledgement.cleanup()
    acknowledgement.resolve(() => this.unsubscribe(api))
  }

  close(): void {
    this.closed = true
    for (const [, subscription] of this.subscriptions) {
      subscription.acknowledgement?.cleanup()
      subscription.acknowledgement?.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
    }
    this.subscriptions.clear()
    this.ws?.close()
    this.ws = null
  }
}

function readVarint(data: Uint8Array, start: number): { value: number; offset: number } {
  let value = 0
  let shift = 0
  let offset = start
  while (offset < data.length) {
    const byte = data[offset++]
    value += (byte & 0x7F) * (2 ** shift)
    if (byte < 0x80) break
    shift += 7
  }
  return { value, offset }
}
