import { decodeJSONEnvelope, encodeLuxoJSON } from './json'
import { jsonUint64, readWireUint64, UINT64_MAX } from './uint64'
import type { UInt64 } from './uint64'
import { decodeBinaryError, jsonResponseError } from './error-codec'
import { JSONParamValidator } from './json-params'
import { LuxoError } from './error'
import { Encoder, fieldMaskSet } from './codec'
import { HTTPHeaders } from './http-headers'

const textEncoder = new TextEncoder()
// Preserve Response.text()'s document BOM handling, but reject invalid UTF-8.
const httpTextDecoder = new TextDecoder('utf-8', { fatal: true })
export { decodeBinaryError, jsonResponseError } from './error-codec'

const BinaryFrame = {
  callRequest: 0x01,
  callSuccess: 0x02,
  callError: 0x03,
  subscribe: 0x10,
  unsubscribe: 0x11,
  stream: 0x12,
  subscribeSuccess: 0x13,
  subscribeError: 0x14,
  streamComplete: 0x15,
  streamError: 0x16,
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

function writeDateTimeParam(enc: Encoder, value: unknown): void {
	if (typeof value !== 'string') {
		throw new LuxoError('ConfigError', 0, 'DateTime parameters require an RFC3339 string')
	}
	enc.writeDateTime(value)
}

/** Encode a single API param (scalar or list) to binary using the schema metadata. */
export function encodeParam(
  enc: Encoder,
	pm: ParamEncodingSchema,
  v: unknown,
	types: Record<string, Record<string, SelectionFieldSchema>> = {},
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
		if (!Array.isArray(v)) throw new LuxoError('ConfigError', 0, `parameter field ${pm.fieldID} must be an array`)
		enc.writeVarint(arr.length)
    switch (pm.type) {
      case 'Int': case 'Duration':
		for (const value of arr as bigint[]) enc.writeSvarint(value); break
      case 'DateTime':
		for (const value of arr) writeDateTimeParam(enc, value); break
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
	  case 'Model':
		for (const value of arr) writeStructuredParam(enc, pm, value, types); break
	  default:
		throw new LuxoError('ConfigError', 0, `unsupported binary list param type: ${pm.type}`)
    }
    return
  }
  switch (pm.type) {
    case 'Int': case 'Duration':
	  enc.writeSvarint(v as bigint); break
    case 'Float':
	  enc.writeFixed64(v as number); break
    case 'String': case 'Enum': case 'Decimal':
	  enc.writeString(v as string); break
    case 'Boolean':
	  enc.writeBool(v as boolean); break
    case 'UUID':
	  enc.writeUUID(v as string); break
    case 'DateTime': {
	  writeDateTimeParam(enc, v); break
	}
	case 'Bytes':
	  enc.writeBytes(toUint8Array(v)); break
	case 'JSON':
	  enc.writeBytes(encodeJSONParam(v)); break
	case 'Model':
	  writeStructuredParam(enc, pm, v, types); break
	default:
	  throw new LuxoError('ConfigError', 0, `unsupported binary param type: ${pm.type}`)
  }
}

type ParamEncodingSchema = Omit<ParamSchema, 'name'> & { name?: string }

function writeStructuredParam(
	enc: Encoder,
	meta: Pick<ParamSchema, 'typeName'>,
	value: unknown,
	types: Record<string, Record<string, SelectionFieldSchema>>,
): void {
	const typeName = meta.typeName
	const fields = typeName ? types[typeName] : undefined
	if (!typeName || !fields) {
		throw new LuxoError('ConfigError', 0, `missing schema for structured type ${typeName ?? '<unknown>'}`)
	}
	enc.writeDelimited(message => encodeStructuredMessage(message, value, fields, types, typeName))
}

function encodeStructuredMessage(
	enc: Encoder,
	value: unknown,
	fields: Record<string, SelectionFieldSchema>,
	types: Record<string, Record<string, SelectionFieldSchema>>,
	typeName: string,
): void {
	if (!isRecord(value)) throw new LuxoError('ConfigError', 0, `${typeName} must be an object`)
	enc.writeVarint(structuredArenaLength(value, fields))
	for (const [name, field] of Object.entries(fields)) {
		const fieldValue = value[name]
		if (fieldValue === undefined) continue
		writeStructuredField(enc, name, field, fieldValue, types)
	}
	enc.writeEnd()
}

function structuredArenaLength(value: Record<string, unknown>, fields: Record<string, SelectionFieldSchema>): number {
	let length = 0
	for (const [name, field] of Object.entries(fields)) {
		const fieldValue = value[name]
		if (fieldValue == null || field.isList || (field.type !== 'String' && field.type !== 'Enum')) continue
		if (typeof fieldValue !== 'string') {
			throw new LuxoError('ConfigError', 0, `${name} must be a string`)
		}
		length += utf8Length(fieldValue)
	}
	return length
}

function utf8Length(value: string): number {
	let length = 0
	for (let i = 0; i < value.length; i++) {
		const code = value.charCodeAt(i)
		if (code < 0x80) length++
		else if (code < 0x800) length += 2
		else if (code >= 0xd800 && code <= 0xdbff && i + 1 < value.length &&
			value.charCodeAt(i + 1) >= 0xdc00 && value.charCodeAt(i + 1) <= 0xdfff) {
			length += 4
			i++
		} else length += 3
	}
	return length
}

function writeStructuredField(
	enc: Encoder,
	name: string,
	field: SelectionFieldSchema,
	value: unknown,
	types: Record<string, Record<string, SelectionFieldSchema>>,
): void {
	if (!field.type) throw new LuxoError('ConfigError', 0, `missing wire type for field ${name}`)
	enc.writeVarint(field.fieldID)
	if (field.nullable) {
		if (value === null) {
			enc.writeBool(false)
			return
		}
		enc.writeBool(true)
	} else if (value === null) {
		throw new LuxoError('ConfigError', 0, `${name} is not nullable`)
	}
	if (field.isList) {
		if (!Array.isArray(value)) throw new LuxoError('ConfigError', 0, `${name} must be an array`)
		enc.writeVarint(value.length)
		for (const item of value) writeStructuredValue(enc, field, item, types)
		return
	}
	writeStructuredValue(enc, field, value, types)
}

function writeStructuredValue(
	enc: Encoder,
	field: SelectionFieldSchema,
	value: unknown,
	types: Record<string, Record<string, SelectionFieldSchema>>,
): void {
	switch (field.type) {
		case 'Int': case 'Duration': enc.writeSvarint(value as bigint); return
		case 'Float': enc.writeFixed64(value as number); return
		case 'String': case 'Enum': case 'Decimal': enc.writeString(value as string); return
		case 'Boolean': enc.writeBool(value as boolean); return
		case 'UUID': enc.writeUUID(value as string); return
		case 'DateTime': writeDateTimeParam(enc, value); return
		case 'Bytes': enc.writeBytes(toUint8Array(value)); return
		case 'JSON': enc.writeBytes(encodeJSONParam(value)); return
		case 'Model': writeNestedStructuredValue(enc, field, value, types); return
		default: throw new LuxoError('ConfigError', 0, `unsupported structured field type: ${field.type}`)
	}
}

function writeNestedStructuredValue(
	enc: Encoder,
	field: SelectionFieldSchema,
	value: unknown,
	types: Record<string, Record<string, SelectionFieldSchema>>,
): void {
	const typeName = field.typeName
	const nestedFields = typeName ? types[typeName] : undefined
	if (!typeName || !nestedFields) {
		throw new LuxoError('ConfigError', 0, `missing schema for structured type ${typeName ?? '<unknown>'}`)
	}
	encodeStructuredMessage(enc, value, nestedFields, types, typeName)
}

function encodeJSONParam(value: unknown): Uint8Array {
	const encoded = encodeLuxoJSON(value)
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
	params?: ParamSchema[]
  fields?: Record<string, SelectionFieldSchema>
  types?: Record<string, Record<string, SelectionFieldSchema>>
}

export interface ParamSchema {
	fieldID: number
	name: string
	type: string
	typeName?: string
	isList?: boolean
	nullable?: boolean
}

export interface SelectionFieldSchema {
  fieldID: number
	type?: string
  typeName?: string
	isList?: boolean
	nullable?: boolean
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

function isFilterValue(value: unknown): value is string | number | bigint | boolean {
  return typeof value === 'string' || typeof value === 'boolean' || typeof value === 'bigint' ||
    (typeof value === 'number' && Number.isFinite(value))
}

export function writeFieldMask(enc: Encoder, meta: APISchema, params?: Record<string, unknown>): void {
  const select = params?.$select
	if (!meta.fields || Object.keys(meta.fields).length === 0) {
    enc.writeVarint(0)
    return
  }
	if (typeof select !== 'string' || select.trim() === '') {
		throw new LuxoError('ConfigError', 0, '$select is required for structured responses')
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
      encodeParam(enc, param, value, meta.types)
    }
  }
  if (params && '$filters' in params) encodeFilters(enc, params.$filters)
  if (params && '$sorters' in params) encodeSorters(enc, params.$sorters)
  enc.writeEnd()
  return enc.bytes()
}

/** Transport interface — implemented by HTTP, WebSocket, etc. */
export interface Transport {
  call(api: string, params?: Record<string, unknown>, options?: CallOptions): Promise<unknown>
  subscribe<T>(api: string, params: Record<string, unknown>, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void>
  setSchema(schema: Record<string, APISchema>): void
  setMode(mode: TransportMode): void
  setToken(token: string | null): void
  close?(): void
}

export interface CallOptions {
  signal?: AbortSignal
}

/** Terminal notifications after acknowledgement; setup failures reject subscribe(). */
export interface StreamObserver {
  onError?: (error: LuxoError) => void
  onComplete?: () => void
}

/** Shared transport options */
export interface TransportOptions {
  token?: string
  headers?: Record<string, string>
  mode?: TransportMode
  /** Request timeout in milliseconds (default: 30000) */
  timeout?: number
  /** Called only for valid HTTP/code 401 errors; a new token retries once in the original mode. */
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
  private readonly jsonParams = new JSONParamValidator()
  private readonly headers: HTTPHeaders
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
    this.headers = new HTTPHeaders(options?.headers, options?.token)
  }

  setSchema(schema: Record<string, APISchema>): void {
    this.jsonParams.setSchema(schema)
    this.schema = schema
  }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void {
    this.headers.setToken(token)
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

  async subscribe<T>(_api: string, _params: Record<string, unknown>, _decode: (data: unknown) => T, _onData: (data: T) => void): Promise<() => void> {
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
    const prepared = this.jsonParams.prepare(api, params)
    const body = { ...prepared, $api: api }

    const scope = await this.openRequest({
      method: 'POST',
      headers: this.headers.json,
      body: encodeLuxoJSON(body),
    }, options?.signal)
    const resp = scope.response

    try {
      let json: Record<string, unknown>
      try { json = decodeJSONEnvelope(httpTextDecoder.decode(await resp.arrayBuffer())) } catch (error) {
        const aborted = scope.abortError(error)
        if (aborted) throw aborted
        throw new LuxoError('ParseError', resp.status, `invalid JSON (HTTP ${resp.status})`)
      }

      if (resp.status === 200 && !('error' in json)) return json.data
      const error = jsonResponseError(json, resp.status)
      const refresh = this.refreshAfterUnauthorized(error, scope, allowRefresh)
      if (refresh) {
        const token = await refresh
        if (token) {
          this.setToken(token)
          return this.jsonCall(api, params, options, false)
        }
      }
      throw error
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
      headers: this.headers.binary,
      body: this.encodeBinaryRequest(api, params),
    }, options?.signal)
    try {
      const data = await this.readBinaryResponse(scope)
      if (scope.response.status === 200) return data
      const error = decodeBinaryError(data, scope.response.status)
      const refresh = this.refreshAfterUnauthorized(error, scope, allowRefresh)
      if (refresh) {
        const token = await refresh
        if (token) {
          this.setToken(token)
          return this.binaryCall(api, params, options, false)
        }
      }
      throw error
    } finally {
      scope.close()
    }
  }

  private refreshAfterUnauthorized(
    error: LuxoError, scope: ResponseScope, allowRefresh: boolean,
  ): Promise<string | null> | undefined {
    // Only a validated 401 envelope may refresh; body failures never reach here.
    if (!allowRefresh || scope.response.status !== 401 ||
      error.code !== 401 || error.error === 'ParseError' || !this.onTokenExpired) return undefined
    scope.close()
    return this.onTokenExpired()
  }

  private encodeBinaryRequest(api: string, params?: Record<string, unknown>): ArrayBuffer {
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}" — call setSchema() or use LuxoClient.create()`)
	return toArrayBuffer(encodeBinaryBody(meta, params))
  }

  private async readBinaryResponse(scope: ResponseScope): Promise<Uint8Array> {
    const response = scope.response
    try {
      return new Uint8Array(await response.arrayBuffer())
    } catch (error) {
      const aborted = scope.abortError(error)
      if (aborted) throw aborted
      throw new LuxoError('ParseError', response.status, 'invalid binary response body')
    }
  }
}

// --- WebSocket Transport ---

interface SubscriptionAcknowledgement {
  resolve: (unsubscribe: () => void) => void
  reject: (error: Error) => void
  cleanup: () => void
}

interface WSSubscription {
  mode: TransportMode
  onData: (data: unknown) => void
  observer?: StreamObserver
  acknowledgement?: SubscriptionAcknowledgement
}

/** WebSocket transport — persistent connection, multiplexed requests.
 *  Ideal for real-time apps: subscriptions, events, high-frequency calls.
 *  Supports both JSON and binary modes. */
export class WsTransport implements Transport {
  private readonly jsonParams = new JSONParamValidator()
  private ws: WebSocket | null = null
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private pending = new Map<UInt64, {
    resolve: (v: unknown) => void
    reject: (e: Error) => void
    cleanup: () => void
  }>()
  private seq: UInt64 = 0
  private connectPromise: Promise<void> | null = null
  private rejectConnection: ((error: Error) => void) | null = null
  private token: string | undefined
  private closed = false
  private reconnectAttempts = 0
  private maxReconnectAttempts = 10
  private reconnectDelay = 1000 // ms, doubles each attempt (exponential backoff)
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined
  private readonly observer?: TransportObserver
  private readonly timeout: number
  private subscriptions = new Map<UInt64, WSSubscription>()

  constructor(private endpoint: string, options?: TransportOptions) {
    this.mode = options?.mode ?? 'json'
    this.token = options?.token
    this.observer = options?.observer
    this.timeout = options?.timeout ?? 30000
  }

  setSchema(schema: Record<string, APISchema>): void {
    this.jsonParams.setSchema(schema)
    this.schema = schema
  }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void { this.token = token ?? undefined }

  private connect(): Promise<void> {
    if (this.closed) return Promise.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
    if (this.ws?.readyState === WebSocket.OPEN) return Promise.resolve()
    if (this.connectPromise) return this.connectPromise
    if (this.ws) return this.replaceClosingSocket(this.ws)

    let ws: WebSocket
    try {
      const separator = this.endpoint.includes('?') ? '&' : '?'
      const url = this.token ? `${this.endpoint}${separator}token=${encodeURIComponent(this.token)}` : this.endpoint
      ws = new WebSocket(url)
    } catch (error) {
      return Promise.reject(error)
    }
    this.cancelReconnect()
    ws.binaryType = 'arraybuffer'
    this.ws = ws
    this.connectPromise = new Promise((resolve, reject) => {
      this.rejectConnection = reject
      ws.onopen = () => {
        if (this.ws !== ws) { ws.close(); return }
        this.connectPromise = null
        this.rejectConnection = null
        this.reconnectAttempts = 0
        resolve()
      }
      ws.onerror = () => {
        if (this.ws !== ws) return
        try { this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket connection failed'), true) }
        finally { ws.close() }
      }
      ws.onmessage = (event) => {
        if (this.ws !== ws) return
        if (event.data instanceof ArrayBuffer) {
          this.handleBinaryResponse(new Uint8Array(event.data))
        } else {
          this.handleJSONResponse(typeof event.data === 'string' ? event.data : '', ws)
        }
      }

      ws.onclose = () => this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket closed'), true)
    })
    return this.connectPromise
  }

  private replaceClosingSocket(ws: WebSocket): Promise<void> {
    try { this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket closed'), true) }
    finally { ws.close() }
    return this.connect()
  }

  private disconnect(ws: WebSocket, error: LuxoError, notify: boolean): void {
    if (this.ws !== ws) return
    this.ws = null
    this.connectPromise = null
    this.rejectConnection?.(error)
    this.rejectConnection = null
    for (const request of this.pending.values()) {
      request.cleanup()
      request.reject(error)
    }
    this.pending.clear()
    // Detach before invoking observers: callbacks may close or start a new connection.
    const retired = this.subscriptions
    this.subscriptions = new Map()
    for (const subscription of retired.values()) {
      subscription.acknowledgement?.cleanup()
      subscription.acknowledgement?.reject(error)
    }
    this.scheduleReconnect()
    try { if (notify) this.notifyDisconnected(retired, error) }
    finally { retired.clear() }
  }

  private notifyDisconnected(retired: Map<UInt64, WSSubscription>, error: LuxoError): void {
    let failures: unknown[] | undefined
    for (const subscription of retired.values()) {
      if (subscription.acknowledgement) continue
      try { subscription.observer?.onError?.(error) }
      catch (failure) { (failures ??= []).push(failure) }
    }
    if (failures?.length === 1) throw failures[0]
    if (failures) throw new AggregateError(failures, 'WebSocket disconnect observers failed')
  }

  private scheduleReconnect(): void {
    this.cancelReconnect()
    if (this.closed || this.reconnectAttempts >= this.maxReconnectAttempts) return
    const delay = Math.min(this.reconnectDelay * 2 ** this.reconnectAttempts++, 30000)
    this.reconnectTimer = setTimeout(async () => {
      this.reconnectTimer = undefined
      try { await this.connect() }
      catch {
        // A constructor failure has no socket event; never duplicate an event-scheduled retry.
        if (!this.ws && this.reconnectTimer === undefined) this.scheduleReconnect()
      }
    }, delay)
  }

  private cancelReconnect(): void {
    if (this.reconnectTimer === undefined) return
    clearTimeout(this.reconnectTimer)
    this.reconnectTimer = undefined
  }

  private requireSocket(): WebSocket {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN) throw new LuxoError('NetworkError', 0, 'WebSocket is not connected')
    return ws
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
    const id = this.nextRequestID()
    return this.createPendingCall(id, api, params, options?.signal)
  }

  private nextRequestID(): UInt64 {
    if (typeof this.seq === 'number' && this.seq < Number.MAX_SAFE_INTEGER) return ++this.seq
    const previous = BigInt(this.seq)
    if (previous >= UINT64_MAX) throw new LuxoError('RequestIDExhausted', 0, 'WebSocket request IDs exhausted')
    return this.seq = previous + 1n
  }

  private createPendingCall(
    id: UInt64,
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

  private sendCall(id: UInt64, api: string, params?: Record<string, unknown>): void {
    const ws = this.requireSocket()
    if (this.mode !== 'binary') {
      const prepared = this.jsonParams.prepare(api, params)
      ws.send(encodeLuxoJSON({ ...prepared, $id: id, $api: api }))
      return
    }
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}"`)
    const enc = new Encoder()
	enc.writeRawBytes(new Uint8Array([BinaryFrame.callRequest]))
    enc.writeUint64(id)
	enc.writeRawBytes(encodeBinaryBody(meta, params))
    ws.send(toArrayBuffer(enc.bytes()))
  }

  async subscribe<T>(api: string, params: Record<string, unknown>, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void> {
    await this.connect()
    const id = this.nextRequestID()
    const subscription: WSSubscription = {
      observer, mode: this.mode,
      onData: data => {
        let value: T
        try { value = decode(data) }
        catch (error) { this.failSubscriptionDecoding(id, subscription, error); return }
        // Application exceptions must not become protocol failures.
        onData(value)
      },
    }
    return new Promise<() => void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        if (!subscription.acknowledgement || this.subscriptions.get(id) !== subscription) return
        this.unsubscribe(id)
        subscription.acknowledgement = undefined
        reject(new LuxoError('TimeoutError', 0, `subscription timed out after ${this.timeout}ms`))
      }, this.timeout)
      subscription.acknowledgement = { resolve, reject, cleanup: () => clearTimeout(timeout) }
      this.subscriptions.set(id, subscription)
      try {
        this.sendSubscription(id, api, params, subscription.mode)
      } catch (error) {
        this.subscriptions.delete(id)
        subscription.acknowledgement.cleanup()
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  private unsubscribe(id: UInt64): void {
    const subscription = this.subscriptions.get(id)
    if (!subscription) return
    this.subscriptions.delete(id)
    subscription.acknowledgement?.cleanup()
    if (this.ws?.readyState !== WebSocket.OPEN) return
    if (subscription.mode === 'binary') {
      const enc = new Encoder()
      enc.writeRawBytes(new Uint8Array([BinaryFrame.unsubscribe]))
      enc.writeUint64(id)
      this.ws.send(toArrayBuffer(enc.bytes()))
      return
    }
    this.ws.send(encodeLuxoJSON({ $unsub: id }))
  }

  private sendSubscription(id: UInt64, api: string, params: Record<string, unknown>, mode: TransportMode): void {
    const ws = this.requireSocket()
    if (mode === 'binary') {
      const meta = this.schema[api]
      if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}"`)
      const enc = new Encoder()
	  enc.writeRawBytes(new Uint8Array([BinaryFrame.subscribe]))
	  enc.writeUint64(id)
	  enc.writeRawBytes(encodeBinaryBody(meta, params))
      ws.send(toArrayBuffer(enc.bytes()))
      return
    }
    const prepared = this.jsonParams.prepare(api, params)
    ws.send(encodeLuxoJSON({ ...prepared, $id: id, $sub: api }))
  }

  private handleJSONResponse(data: string, ws: WebSocket) {
    let json: Record<string, unknown>
    try { json = decodeJSONEnvelope(data) }
    catch {
      try { this.disconnect(ws, new LuxoError('ParseError', 0, 'invalid WebSocket JSON message'), true) }
      finally { ws.close() }
      return
    }
    if ('$end' in json) {
      if ('$sub' in json || '$stream' in json || '$id' in json) return
      this.handleJSONStreamEnd(json)
      return
    }
    if ('$sub' in json) {
      const id = jsonUint64(json.$sub)
      if (id === undefined || this.subscriptions.get(id)?.mode !== 'json') return
      if (!('error' in json) && json.ok !== true) return
      this.handleSubscriptionAcknowledgement(id, 'error' in json
        ? jsonResponseError(json)
        : undefined)
      return
    }
    if ('$stream' in json) {
      const id = jsonUint64(json.$stream)
      if (id === undefined) return
      this.deliverSubscription(id, 'json', json.data)
      return
    }
    const id = jsonUint64(json.$id)
    if (id === undefined) return
    const p = this.pending.get(id)
    if (!p) return
    this.pending.delete(id)
    p.cleanup()
    if ('error' in json) p.reject(jsonResponseError(json))
    else p.resolve(json.data)
  }

  private handleJSONStreamEnd(json: Record<string, unknown>): void {
    const id = jsonUint64(json.$end)
    if (id === undefined) return
    const failure = 'error' in json
    const error = failure && !('ok' in json) ? jsonResponseError(json)
      : !failure && json.ok === true && !('data' in json) ? undefined
      : new LuxoError('ParseError', 0, 'invalid stream completion')
    this.finishSubscription(id, 'json', error)
  }

  private handleBinaryResponse(data: Uint8Array) {
	const header = readWireUint64(data, 1)
	if (!header) return
	if (data[0] === BinaryFrame.subscribeSuccess || data[0] === BinaryFrame.subscribeError) {
	  const { value: id, offset } = header
      if (data[0] === BinaryFrame.subscribeSuccess && offset !== data.length) return
      if (this.subscriptions.get(id)?.mode !== 'binary') return
      this.handleSubscriptionAcknowledgement(
        id,
        data[0] === BinaryFrame.subscribeError ? decodeBinaryError(data.subarray(offset), 0) : undefined,
      )
      return
    }
	if (data[0] === BinaryFrame.stream) {
	  const { value: id, offset } = header
      this.deliverSubscription(id, 'binary', data.subarray(offset))
      return
    }
    if (data[0] === BinaryFrame.streamComplete || data[0] === BinaryFrame.streamError) {
      const { value: id, offset } = header
      const error = data[0] === BinaryFrame.streamError ? decodeBinaryError(data.subarray(offset), 0)
        : offset === data.length ? undefined
        : new LuxoError('ParseError', 0, 'trailing stream completion bytes')
      this.finishSubscription(id, 'binary', error)
      return
    }
	if (data[0] !== BinaryFrame.callSuccess && data[0] !== BinaryFrame.callError) return
	const { value: id, offset: off } = header

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

  private deliverSubscription(id: UInt64, mode: TransportMode, data: unknown): void {
    const subscription = this.subscriptions.get(id)
    if (subscription?.mode === mode && !subscription.acknowledgement) subscription.onData(data)
  }

  private failSubscriptionDecoding(id: UInt64, subscription: WSSubscription, cause: unknown): void {
    if (this.subscriptions.get(id) !== subscription) return
    const error = new LuxoError('DecodeError', 0, cause instanceof Error ? cause.message : 'invalid subscription response')
    // Local retirement is authoritative even if the advisory cancellation cannot be sent.
    try { this.unsubscribe(id) } catch { /* Connection events own transport failure notification. */ }
    subscription.observer?.onError?.(error)
  }

  private finishSubscription(id: UInt64, mode: TransportMode, error?: LuxoError): void {
    const subscription = this.subscriptions.get(id)
    if (!subscription || subscription.mode !== mode) return
    this.subscriptions.delete(id)
    const acknowledgement = subscription.acknowledgement
    if (acknowledgement) {
      acknowledgement.cleanup()
      acknowledgement.reject(new LuxoError('ParseError', 0, 'stream ended before acknowledgement'))
      return
    }
    if (error) subscription.observer?.onError?.(error)
    else subscription.observer?.onComplete?.()
  }

  private handleSubscriptionAcknowledgement(id: UInt64, error?: LuxoError): void {
    const subscription = this.subscriptions.get(id)
    const acknowledgement = subscription?.acknowledgement
    if (!subscription) return
    if (error) {
      this.subscriptions.delete(id)
      acknowledgement?.cleanup()
      acknowledgement?.reject(error)
      if (!acknowledgement) subscription.observer?.onError?.(error)
      return
    }
    if (!acknowledgement) return
    subscription.acknowledgement = undefined
    acknowledgement.cleanup()
    acknowledgement.resolve(() => this.unsubscribe(id))
  }

  close(): void {
    this.closed = true
    this.cancelReconnect()
    const ws = this.ws
    if (!ws) return
    this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket closed'), false)
    ws.close()
  }
}
