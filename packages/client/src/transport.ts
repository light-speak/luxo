import { booleanFromJSON, decodeJSONEnvelope, encodeBoundedLuxoJSON, encodeLuxoJSON } from './json'
import { readWireUint64, UINT64_MAX } from './uint64'
import type { UInt64 } from './uint64'
import { decodeBinaryError, jsonResponseError, missingBinaryResponse } from './error-codec'
import { JSONParamValidator } from './json-params'
import { JSONEnvelopeSource, JSONResourceError, opaqueJSONResource } from './json-resource'
import type { JSONResourcePlan } from './json-resource'
import { snapshotAPISchema } from './schema-runtime'
import { LuxoError } from './error'
import { Encoder } from './codec'
import { writeSelectionMask } from './selection'
import { HTTPHeaders } from './http-headers'
import { scheduleDeadline } from './deadline'
import { readHTTPBody, responseLimit } from './http-response'
import { enumRawValue } from './enum'
import { GeneratedRequest, requestJSON, staticJSONParameters, generatedJSONParameters } from './request'
import type { RequestEncoder, RequestValues } from './request'
import type { RequestParameters } from './request'
import { writeFilters, writeSorters, writeDynamicFilters, writeDynamicSorters } from './query-controls'

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

export type TransportMode = 'json' | 'binary'
type PreparedRequestBody = Uint8Array<ArrayBuffer> | string

function writeDateTimeParam(enc: Encoder, value: unknown): void {
	if (typeof value !== 'string') {
		throw new LuxoError('ConfigError', 0, 'DateTime parameters require an RFC3339 string')
	}
	enc.writeDateTime(value)
}

function writeTextParam(enc: Encoder, value: unknown): void {
	if (typeof value !== 'string') {
		throw new LuxoError('ConfigError', 0, 'text parameters require a string')
	}
	enc.writeString(value)
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
		enc.writeArrayHeader(arr.length)
    switch (pm.type) {
      case 'Int': case 'Duration':
		for (const value of arr as bigint[]) enc.writeSvarint(value); break
      case 'DateTime':
		for (const value of arr) writeDateTimeParam(enc, value); break
      case 'Float':
		for (const value of arr as number[]) enc.writeFixed64(value); break
      case 'Enum':
		for (const value of arr) enc.writeString(enumRawValue(value, pm.typeName)); break
      case 'String': case 'Decimal':
		for (const value of arr) writeTextParam(enc, value); break
      case 'Boolean':
		for (const value of arr) enc.writeBool(booleanFromJSON(value)); break
      case 'UUID':
		for (const value of arr as string[]) enc.writeUUID(value); break
	  case 'Bytes':
		for (const value of arr) enc.writeBytes(toUint8Array(value)); break
	  case 'JSON':
		for (const value of arr) enc.writeJSON(value); break
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
    case 'Enum':
	  enc.writeString(enumRawValue(v, pm.typeName)); break
    case 'String': case 'Decimal':
	  writeTextParam(enc, v); break
    case 'Boolean':
	  enc.writeBool(booleanFromJSON(v)); break
    case 'UUID':
	  enc.writeUUID(v as string); break
    case 'DateTime': {
	  writeDateTimeParam(enc, v); break
	}
	case 'Bytes':
	  enc.writeBytes(toUint8Array(v)); break
	case 'JSON':
	  enc.writeJSON(v); break
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
		let fieldValue = value[name]
		if (fieldValue == null || field.isList || (field.type !== 'String' && field.type !== 'Enum')) continue
		if (field.type === 'Enum') fieldValue = enumRawValue(fieldValue, field.typeName)
		if (typeof fieldValue !== 'string') {
			throw new LuxoError('ConfigError', 0, `${name} must be a string`)
		}
		length += utf8Length(fieldValue)
	}
	return length
}

export function utf8Length(value: string): number {
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
		if (field.type === 'JSON' && !field.isList) {
			enc.writeJSONNull()
			return
		}
		throw new LuxoError('ConfigError', 0, `${name} is not nullable`)
	}
	if (field.isList) {
		if (!Array.isArray(value)) throw new LuxoError('ConfigError', 0, `${name} must be an array`)
		enc.writeArrayHeader(value.length)
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
		case 'Enum': enc.writeString(enumRawValue(value, field.typeName)); return
		case 'String': case 'Decimal': writeTextParam(enc, value); return
		case 'Boolean': enc.writeBool(booleanFromJSON(value)); return
		case 'UUID': enc.writeUUID(value as string); return
		case 'DateTime': writeDateTimeParam(enc, value); return
		case 'Bytes': enc.writeBytes(toUint8Array(value)); return
		case 'JSON': enc.writeJSON(value); return
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

export function encodeJSONParam(value: unknown): Uint8Array {
	const encoded = encodeBoundedLuxoJSON(value)
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
  response?: JSONResponseSchema
}

export interface JSONResponseSchema {
  type: string
  typeName?: string
  isList?: boolean
  paginated?: boolean
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function writeFieldMask(enc: Encoder, meta: APISchema, params?: Record<string, unknown>): void {
  writeSelectionMask(enc, meta, params?.$select)
}


export function encodeStaticRequestBody<P extends RequestValues>(
  meta: APISchema, codec: RequestEncoder<P>, params?: P,
): Uint8Array<ArrayBuffer> {
  if (codec.apiID !== meta.id) throw new LuxoError('ConfigError', 0, 'generated request API ID differs from registered schema')
  const enc = new Encoder(Math.min(65536, codec.initialCapacity(params)))
  enc.writeVarint(codec.apiID)
  writeSelectionMask(enc, meta, params?.$select)
  codec.writeParameters(enc, params)
  if (params?.$filters !== undefined) writeFilters(enc, params.$filters)
  if (params?.$sorters !== undefined) writeSorters(enc, params.$sorters)
  enc.writeEnd()
  return enc.takeBytes()
}

function prepareStaticBody<P extends RequestValues>(
  meta: APISchema | undefined, mode: TransportMode, codec: RequestEncoder<P>, params?: P,
): PreparedRequestBody | undefined {
  if (!meta) throw new LuxoError('ConfigError', 0, 'generated request API has no registered schema')
  if (mode === 'binary') return encodeStaticRequestBody(meta, codec, params)
  if (meta.id !== codec.apiID) throw new LuxoError('ConfigError', 0, 'generated request API ID differs from registered schema')
  return staticJSONParameters(codec, params)
}

function encodeGeneratedBody(meta: APISchema, params: GeneratedRequest): Uint8Array<ArrayBuffer> {
  if (params.apiID !== meta.id) throw new LuxoError('ConfigError', 0, 'generated request API ID differs from registered schema')
  const enc = new Encoder(Math.min(65536, params.initialCapacity))
  enc.writeVarint(params.apiID)
  writeSelectionMask(enc, meta, params.selection)
  params.writeParameters(enc)
  const filters = params.filters
  const sorters = params.sorters
  if (filters !== undefined) writeFilters(enc, filters)
  if (sorters !== undefined) writeSorters(enc, sorters)
  enc.writeEnd()
  return enc.bytes()
}

export function encodeBinaryBody(meta: APISchema, params?: RequestParameters): Uint8Array<ArrayBuffer> {
  if (params instanceof GeneratedRequest) return encodeGeneratedBody(meta, params)
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
  const filters = params?.$filters
  const sorters = params?.$sorters
  if (filters !== undefined) writeDynamicFilters(enc, filters)
  if (sorters !== undefined) writeDynamicSorters(enc, sorters)
  enc.writeEnd()
  return enc.bytes()
}

/** Transport interface — implemented by HTTP, WebSocket, etc. */
export interface Transport {
  call(api: string, params?: RequestParameters, options?: CallOptions): Promise<unknown>
  subscribe<T>(api: string, params: RequestParameters, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void>
  setSchema(schema: Record<string, APISchema>): void
  setMode(mode: TransportMode): void
  setToken(token: string | null): void
  close?(): void
}

/** Candidate static request entry; dynamic callers retain the explicit Transport API. */
export interface GeneratedTransport extends Transport {
  callGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown>
  subscribeGenerated<P extends RequestValues, T>(api: string, codec: RequestEncoder<P>, params: P | undefined, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void>
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
  /** Maximum collected HTTP response bytes (default: 2 MiB). */
  maxResponseBytes?: number
  token?: string
  headers?: Record<string, string>
  mode?: TransportMode
  /** Request timeout in milliseconds (default: 30000) */
  // WebSocket bounds connection setup, response/ACK and renewal separately.
  timeout?: number
  /** HTTP 401 retries once; WS close 4001 refreshes before reconnecting, without replaying old work. */
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

/** Minimal HTTP I/O boundary; decoding and authentication stay in the transport. */
export interface HTTPResponse {
  readonly status: number
  readonly body?: ReadableStream<Uint8Array> | null
  readonly headers?: Pick<Headers, 'get'>
  arrayBuffer(): Promise<ArrayBuffer>
}

interface ResponseScope {
  signal: AbortSignal
  response: HTTPResponse
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

// --- Fetch Transport ---

/** Fetch-based transport; the host runtime selects the HTTP version.
 *  Use the Node-specific NodeHTTP2Transport for explicit HTTP/2 connections. */
export class FetchTransport implements Transport {
  protected refreshAbortSignal?: AbortSignal
  private readonly responseByteLimit: number
  private jsonParams = new JSONParamValidator()
  private readonly headers: HTTPHeaders
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private timeout: number
  private onTokenExpired?: () => Promise<string | null>
  private readonly observer?: TransportObserver

  constructor(private endpoint: string, options?: TransportOptions) {
    this.responseByteLimit = responseLimit(options?.maxResponseBytes)
    this.mode = options?.mode ?? 'json'
    this.timeout = options?.timeout ?? 30000
    this.onTokenExpired = options?.onTokenExpired
    this.observer = options?.observer
    this.headers = new HTTPHeaders(options?.headers, options?.token)
  }

  setSchema(schema: Record<string, APISchema>): void {
    const snapshot = snapshotAPISchema(schema)
    this.jsonParams = new JSONParamValidator(snapshot)
    this.schema = snapshot
  }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void {
    this.headers.setToken(token)
  }

  call(api: string, params?: RequestParameters, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatch(api, params, options)
    return this.callObserved(api, this.mode, performance.now(), this.dispatch(api, params, options))
  }

  callGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatchGenerated(api, codec, params, options)
    return this.callObserved(api, this.mode, performance.now(), this.dispatchGenerated(api, codec, params, options))
  }

  private dispatchGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown> {
    try {
      if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
      const jsonParams = this.jsonParams
      const body = prepareStaticBody(this.schema[api], this.mode, codec, params)
      return body instanceof Uint8Array
        ? this.binaryCall(api, undefined, options, body)
        : this.jsonCall(api, params, options, body, jsonParams)
    } catch (error) {
      return Promise.reject(error)
    }
  }

  private dispatch(api: string, params?: RequestParameters, options?: CallOptions): Promise<unknown> {
    if (options?.signal?.aborted) {
      return Promise.reject(new LuxoError('CancelledError', 0, 'request cancelled'))
    }
    return this.mode === 'binary'
      ? this.binaryCall(api, params, options)
      : this.jsonCall(api, params, options)
  }

  private async callObserved(api: string, mode: TransportMode, start: number, pending: Promise<unknown>): Promise<unknown> {
    try {
      const result = await pending
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

  async subscribe<T>(_api: string, _params: RequestParameters, _decode: (data: unknown) => T, _onData: (data: T) => void): Promise<() => void> {
    throw new LuxoError('ConfigError', 0, 'subscriptions require a WebSocket endpoint')
  }

  async subscribeGenerated<P extends RequestValues, T>(_api: string, _codec: RequestEncoder<P>, _params: P | undefined, _decode: (data: unknown) => T, _onData: (data: T) => void): Promise<() => void> {
    throw new LuxoError('ConfigError', 0, 'subscriptions require a WebSocket endpoint')
  }

  protected executeRequest(endpoint: string, init: RequestInit): Promise<HTTPResponse> {
    return fetch(endpoint, init)
  }

  private async openRequest(init: RequestInit, signal?: AbortSignal, parent?: ResponseScope): Promise<ResponseScope> {
    if (parent) return this.openRetry(init, parent)
    if (signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const controller = new AbortController()
    let timedOut = false
    const cancel = () => controller.abort()
    signal?.addEventListener('abort', cancel, { once: true })
    const cancelTimeout = scheduleDeadline(this.timeout, () => {
      timedOut = true
      controller.abort()
    })
    const close = () => {
      cancelTimeout()
      signal?.removeEventListener('abort', cancel)
    }
    const abortError = (error: unknown) => {
      if (!(error instanceof DOMException) || error.name !== 'AbortError') return undefined
      if (timedOut) return new LuxoError('TimeoutError', 0, `request timed out after ${this.timeout}ms`)
      if (signal?.aborted) return new LuxoError('CancelledError', 0, 'request cancelled')
      return new LuxoError('CancelledError', 0, 'request cancelled')
    }
    try {
      const response = await this.executeRequest(this.endpoint, { ...init, redirect: 'error', signal: controller.signal })
      return { response, close, abortError, signal: controller.signal }
    } catch (e) {
      const aborted = abortError(e)
      close()
      if (aborted) throw aborted
      throw new LuxoError('NetworkError', 0, e instanceof Error ? e.message : String(e))
    }
  }

  private async openRetry(init: RequestInit, parent: ResponseScope): Promise<ResponseScope> {
    try {
      if (parent.signal.aborted) throw new DOMException('request aborted', 'AbortError')
      const response = await this.executeRequest(this.endpoint, { ...init, redirect: 'error', signal: parent.signal })
      return { response, signal: parent.signal, abortError: parent.abortError, close: () => {} }
    } catch (error) {
      throw parent.abortError(error) ?? new LuxoError('NetworkError', 0, error instanceof Error ? error.message : String(error))
    }
  }

  private jsonCall(api: string, params?: RequestParameters, options?: CallOptions, prepared?: string, jsonParams = this.jsonParams): Promise<unknown> {
    try {
      const responsePlan = jsonParams.response(api)
      const fragment = prepared ?? generatedJSONParameters(params)
      const body = fragment === undefined
        ? jsonParams.encode(api, { ...jsonParams.prepare(api, requestJSON(params)), $api: api })
        : jsonParams.validateEncoded(api, '{"$api":' + encodeLuxoJSON(api) + fragment + '}')
      return this.jsonRequest(body, responsePlan, options)
    } catch (error) {
      return Promise.reject(error)
    }
  }

  private async jsonRequest(
    body: string,
    responsePlan: JSONResourcePlan,
    options?: CallOptions,
    allowRefresh = true,
    parent?: ResponseScope,
  ): Promise<unknown> {
    const requestHeaders = this.headers.json
    const scope = await this.openRequest({
      method: 'POST',
      headers: requestHeaders,
      body,
    }, options?.signal, parent)
    const resp = scope.response

    try {
      let json: Record<string, unknown>
      try {
        const text = httpTextDecoder.decode(await readHTTPBody(resp, this.responseByteLimit))
        const source = new JSONEnvelopeSource(text)
        source.validateData(responsePlan, source.error || resp.status !== 200)
        json = decodeJSONEnvelope(text, true)
      } catch (error) {
        const aborted = scope.abortError(error)
        if (aborted) throw aborted
        throw new LuxoError('ParseError', resp.status, error instanceof JSONResourceError ? error.message : `invalid JSON (HTTP ${resp.status})`)
      }

      if (resp.status === 200 && !Object.hasOwn(json, 'error')) {
        if (!Object.hasOwn(json, 'data')) throw new LuxoError('ParseError', resp.status, 'missing JSON response data')
        return json.data
      }
      const error = jsonResponseError(json, resp.status)
      if (this.headers.json !== requestHeaders) throw error
      const refresh = this.refreshAfterUnauthorized(error, scope, allowRefresh, options?.signal)
      if (refresh) {
        const token = await refresh
        if (scope.signal.aborted) throw scope.abortError(new DOMException('request aborted', 'AbortError'))
        if (token) {
          if (options?.signal?.aborted || this.refreshAbortSignal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
          if (this.headers.json !== requestHeaders) throw error
          this.setToken(token)
          return await this.jsonRequest(body, responsePlan, options, false, scope)
        }
      }
      throw error
    } finally {
      scope.close()
    }
  }

  private binaryCall(
    api: string,
    params?: RequestParameters,
    options?: CallOptions,
    preparedBody?: ArrayBuffer | Uint8Array<ArrayBuffer>,
  ): Promise<unknown> {
    try {
      const requestHeaders = this.headers.binary
      const body = preparedBody ?? this.encodeBinaryRequest(api, params)
      return this.binaryRequest(body, options, true, undefined, requestHeaders)
    } catch (error) {
      return Promise.reject(error)
    }
  }

  private async binaryRequest(
    body: ArrayBuffer | Uint8Array<ArrayBuffer>,
    options?: CallOptions,
    allowRefresh = true,
    parent?: ResponseScope,
    requestHeaders = this.headers.binary,
  ): Promise<unknown> {
    const scope = await this.openRequest({
      method: 'POST',
      headers: requestHeaders,
      body,
    }, options?.signal, parent)
    try {
      let data: Uint8Array
      try {
        data = await readHTTPBody(scope.response, this.responseByteLimit)
      } catch (error) {
        const aborted = scope.abortError(error)
        if (aborted) throw aborted
        throw new LuxoError('ParseError', scope.response.status, 'invalid binary response body')
      }
      if (scope.response.status === 200) {
        if (data.length === 0) throw missingBinaryResponse(scope.response.status)
        return data
      }
      const error = decodeBinaryError(data, scope.response.status)
      if (this.headers.binary !== requestHeaders) throw error
      const refresh = this.refreshAfterUnauthorized(error, scope, allowRefresh, options?.signal)
      if (refresh) {
        const token = await refresh
        if (scope.signal.aborted) throw scope.abortError(new DOMException('request aborted', 'AbortError'))
        if (token) {
          if (options?.signal?.aborted || this.refreshAbortSignal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
          if (this.headers.binary !== requestHeaders) throw error
          this.setToken(token)
          return await this.binaryRequest(body, options, false, scope)
        }
      }
      throw error
    } finally {
      scope.close()
    }
  }

  private refreshAfterUnauthorized(
    error: LuxoError, scope: ResponseScope, allowRefresh: boolean, signal?: AbortSignal,
  ): Promise<string | null> | undefined {
    // Only a validated 401 envelope may refresh; body failures never reach here.
    if (!allowRefresh || scope.response.status !== 401 ||
      error.code !== 401 || error.error === 'ParseError' || !this.onTokenExpired) return undefined
    if (scope.signal.aborted) return Promise.reject(scope.abortError(new DOMException('request aborted', 'AbortError')))
    if (signal?.aborted || this.refreshAbortSignal?.aborted) return Promise.reject(new LuxoError('CancelledError', 0, 'request cancelled'))
    const refresh = this.onTokenExpired()
    return this.waitForRefresh(refresh, scope, this.refreshAbortSignal)
  }

  private waitForRefresh(refresh: Promise<string | null>, scope: ResponseScope, lifecycle?: AbortSignal): Promise<string | null> {
    const signal = scope.signal
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        signal?.removeEventListener('abort', cancel)
        lifecycle?.removeEventListener('abort', cancel)
      }
      const cancel = () => {
        cleanup()
        reject(scope.abortError(new DOMException('request aborted', 'AbortError')))
      }
      signal?.addEventListener('abort', cancel, { once: true })
      lifecycle?.addEventListener('abort', cancel, { once: true })
      if (signal?.aborted || lifecycle?.aborted) cancel()
      // Observe late settlement without changing credentials or retrying a cancelled call.
      void refresh.then(token => {
        cleanup()
        resolve(token)
      }, error => {
        cleanup()
        reject(error)
      })
    })
  }

  private encodeBinaryRequest(api: string, params?: RequestParameters): ArrayBuffer {
    const meta = this.schema[api]
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}" — call setSchema() or use LuxoClient.create()`)
    return encodeBinaryBody(meta, params).buffer
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
  responsePlan: JSONResourcePlan
  onData: (data: unknown) => void
  observer?: StreamObserver
  acknowledgement?: SubscriptionAcknowledgement
}

/** WebSocket transport — persistent connection, multiplexed requests.
 *  Ideal for real-time apps: subscriptions, events, high-frequency calls.
 *  Supports both JSON and binary modes. */
export class WsTransport implements Transport {
  private jsonParams = new JSONParamValidator()
  private ws: WebSocket | null = null
  private mode: TransportMode = 'json'
  private schema: Record<string, APISchema> = {}
  private pending = new Map<UInt64, {
    resolve: (v: unknown) => void
    reject: (e: Error) => void
    cleanup: () => void
    responsePlan?: JSONResourcePlan
  }>()
  private seq: UInt64 = 0
  private connectPromise: Promise<void> | null = null
  private rejectConnection: ((error: Error) => void) | null = null
  private token: string | undefined
  private readonly onTokenExpired?: () => Promise<string | null>
  private credentialRevision = 0
  private refreshPromise: Promise<void> | null = null
  private refreshController: AbortController | null = null
  private authError: LuxoError | null = null
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
    this.onTokenExpired = options?.onTokenExpired
    this.observer = options?.observer
    this.timeout = options?.timeout ?? 30000
  }

  setSchema(schema: Record<string, APISchema>): void {
    const snapshot = snapshotAPISchema(schema)
    this.jsonParams = new JSONParamValidator(snapshot)
    this.schema = snapshot
  }
  setMode(mode: TransportMode): void { this.mode = mode }
  setToken(token: string | null): void {
    this.credentialRevision++
    this.token = token ?? undefined
    this.authError = null
    this.refreshController?.abort()
  }

  private connect(signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return Promise.reject(new LuxoError('CancelledError', 0, 'request cancelled'))
    if (this.closed) return Promise.reject(new LuxoError('NetworkError', 0, 'WebSocket closed'))
    if (this.refreshPromise) return this.waitForConnection(this.connectAfterRefresh(this.refreshPromise), signal)
    if (this.authError) return Promise.reject(this.authError)
    if (this.ws?.readyState === WebSocket.OPEN) return Promise.resolve()
    if (this.connectPromise) return this.waitForConnection(this.connectPromise, signal)
    if (this.ws) return this.waitForConnection(this.replaceClosingSocket(this.ws), signal)

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
      const cancelTimeout = scheduleDeadline(this.timeout, () => {
        if (this.ws !== ws || this.rejectConnection === null) return
        try { this.disconnect(ws, new LuxoError('TimeoutError', 0, `connection timed out after ${this.timeout}ms`), true) }
        finally { ws.close() }
      })
      this.rejectConnection = error => { cancelTimeout(); reject(error) }
      ws.onopen = () => {
        if (this.ws !== ws) { ws.close(); return }
        this.connectPromise = null
        this.rejectConnection = null
        this.reconnectAttempts = 0
        cancelTimeout()
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
          this.handleBinaryResponse(new Uint8Array(event.data), ws)
        } else {
          this.handleJSONResponse(typeof event.data === 'string' ? event.data : '', ws)
        }
      }

      ws.onclose = event => this.onSocketClose(ws, event?.code)
    })
    return this.waitForConnection(this.connectPromise, signal)
  }

  private waitForConnection(connection: Promise<void>, signal?: AbortSignal): Promise<void> {
    if (!signal) return connection
    return (async () => {
      let abort: (() => void) | undefined
      try {
        const cancelled = new Promise<never>((_, reject) => {
          abort = () => reject(new LuxoError('CancelledError', 0, 'request cancelled'))
          signal.addEventListener('abort', abort, { once: true })
          if (signal.aborted) abort()
        })
        await Promise.race([connection, cancelled])
      } finally {
        if (abort) signal.removeEventListener('abort', abort)
      }
    })()
  }

  private onSocketClose(ws: WebSocket, code?: number): void {
    if (this.ws !== ws) return
    if (code !== 4001) {
      this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket closed'), true)
      return
    }
    const error = new LuxoError('Unauthorized', 401, 'access token expired')
    this.authError = error
    if (this.onTokenExpired && !this.closed) {
      const controller = new AbortController()
      this.refreshController = controller
      this.refreshPromise = this.renewAccessToken(controller, this.credentialRevision)
    }
    this.disconnect(ws, error, true)
  }

  private async connectAfterRefresh(refresh: Promise<void>): Promise<void> {
    await refresh
    return this.connect()
  }

  private async renewAccessToken(controller: AbortController, revision: number): Promise<void> {
    // Defer user code until disconnect has detached the old connection and work.
    await Promise.resolve()
    let cancelTimeout: (() => void) | undefined
    let abort: (() => void) | undefined
    try {
      if (controller.signal.aborted || this.closed) return
      const cancelled = new Promise<never>((_, reject) => {
        abort = () => reject(new LuxoError('Unauthorized', 401, 'token renewal cancelled'))
        controller.signal.addEventListener('abort', abort, { once: true })
      })
      cancelTimeout = scheduleDeadline(this.timeout, () => controller.abort())
      const renewal = (async () => this.onTokenExpired!())()
      const token = await Promise.race([renewal, cancelled])
      if (controller.signal.aborted || this.closed || revision !== this.credentialRevision) return
      if (!token) throw new LuxoError('Unauthorized', 401, 'token renewal failed')
      this.token = token
      this.credentialRevision++
      this.authError = null
    } catch {
      if (!this.closed && revision === this.credentialRevision) {
        this.authError = new LuxoError('Unauthorized', 401, 'token renewal failed')
      }
    } finally {
      cancelTimeout?.()
      if (abort) controller.signal.removeEventListener('abort', abort)
      if (this.refreshController === controller) {
        this.refreshController = null
        this.refreshPromise = null
        if (!this.closed && !this.authError) this.scheduleReconnect()
      }
    }
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
    const reject = this.rejectConnection
    this.rejectConnection = null
    // Timer cancellation hooks may start a replacement connection.
    reject?.(error)
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
    if (this.closed || this.authError || this.refreshPromise || this.reconnectAttempts >= this.maxReconnectAttempts) return
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

  call(api: string, params?: RequestParameters, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatchCall(api, params, options)
    return this.callObserved(api, this.mode, performance.now(), this.dispatchCall(api, params, options))
  }

  callGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown> {
    if (!this.observer) return this.dispatchGeneratedCall(api, codec, params, options)
    return this.callObserved(api, this.mode, performance.now(), this.dispatchGeneratedCall(api, codec, params, options))
  }

  private async callObserved(api: string, mode: TransportMode, start: number, pending: Promise<unknown>): Promise<unknown> {
    try {
      const result = await pending
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

  private async dispatchCall(api: string, params?: RequestParameters, options?: CallOptions): Promise<unknown> {
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const mode = this.mode, jsonParams = this.jsonParams, meta = this.schema[api]
    await this.connect(options?.signal)
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const id = this.nextRequestID()
    return this.createPendingCall(id, api, params, options?.signal, mode, jsonParams, meta)
  }

  private async dispatchGeneratedCall<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown> {
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const mode = this.mode, jsonParams = this.jsonParams, meta = this.schema[api]
    await this.connect(options?.signal)
    if (options?.signal?.aborted) throw new LuxoError('CancelledError', 0, 'request cancelled')
    const body = prepareStaticBody(meta, mode, codec, params)
    return this.createPendingCall(this.nextRequestID(), api, params, options?.signal, mode, jsonParams, meta, body)
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
    params: RequestParameters | undefined,
    signal: AbortSignal | undefined,
    mode: TransportMode,
    jsonParams: JSONParamValidator,
    meta: APISchema | undefined,
    preparedBody?: PreparedRequestBody,
  ): Promise<unknown> {
    const responsePlan = mode === 'json' ? jsonParams.response(api) : undefined
    const result = this.registerPendingCall(id, signal, responsePlan)
    if (!this.pending.has(id)) return result
    try {
      this.sendCall(id, api, params, mode, jsonParams, meta, preparedBody)
    } catch (error) {
      const pending = this.pending.get(id)
      if (pending) {
        this.pending.delete(id)
        pending.cleanup()
        pending.reject(error instanceof Error ? error : new Error(String(error)))
      }
    }
    return result
  }

  private registerPendingCall(id: UInt64, signal?: AbortSignal, responsePlan?: JSONResourcePlan): Promise<unknown> {
    // Timer and cancellation closures retain only the response ledger, not encoded inputs.
    return new Promise((resolve, reject) => {
      const cancelTimeout = scheduleDeadline(this.timeout, () => {
        if (!this.pending.delete(id)) return
        cleanup()
        reject(new LuxoError('TimeoutError', 0, `request timed out after ${this.timeout}ms`))
      })
      const cancel = () => {
        if (!this.pending.delete(id)) return
        cleanup()
        reject(new LuxoError('CancelledError', 0, 'request cancelled'))
      }
      const cleanup = () => {
        cancelTimeout()
        signal?.removeEventListener('abort', cancel)
      }
      try {
        signal?.addEventListener('abort', cancel, { once: true })
        this.pending.set(id, { resolve, reject, cleanup, responsePlan })
        if (signal?.aborted) cancel()
      } catch (error) {
        this.pending.delete(id)
        cleanup()
        reject(error)
      }
    })
  }

  private sendCall(id: UInt64, api: string, params: RequestParameters | undefined, mode: TransportMode, jsonParams: JSONParamValidator, meta: APISchema | undefined, preparedBody?: PreparedRequestBody): void {
    const ws = this.requireSocket()
    if (mode === 'json') {
      const fragment = typeof preparedBody === 'string' ? preparedBody : generatedJSONParameters(params)
      const message = fragment === undefined
        ? jsonParams.encode(api, { ...jsonParams.prepare(api, requestJSON(params)), $id: id, $api: api })
        : jsonParams.validateEncoded(api, '{"$id":' + id + ',"$api":' + encodeLuxoJSON(api) + fragment + '}')
      if (this.pending.has(id)) ws.send(message)
      return
    }
    const enc = new Encoder()
	enc.writeRawBytes(new Uint8Array([BinaryFrame.callRequest]))
    enc.writeUint64(id)
	enc.writeRawBytes(preparedBody instanceof Uint8Array ? preparedBody : this.encodeCallBody(api, params, meta))
    if (!preparedBody && !this.pending.has(id)) return
    ws.send(enc.bytes().buffer)
  }

  async subscribe<T>(api: string, params: RequestParameters, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void> {
    const mode = this.mode, jsonParams = this.jsonParams, meta = this.schema[api]
    await this.connect()
    return this.startSubscription(api, params, decode, onData, mode, jsonParams, meta, observer)
  }

  async subscribeGenerated<P extends RequestValues, T>(api: string, codec: RequestEncoder<P>, params: P | undefined, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): Promise<() => void> {
    const mode = this.mode, jsonParams = this.jsonParams, meta = this.schema[api]
    await this.connect()
    const body = prepareStaticBody(meta, mode, codec, params)
    return this.startSubscription(api, params, decode, onData, mode, jsonParams, meta, observer, body)
  }

  private startSubscription<T>(api: string, params: RequestParameters | undefined, decode: (data: unknown) => T, onData: (data: T) => void, mode: TransportMode, jsonParams: JSONParamValidator, meta: APISchema | undefined, observer?: StreamObserver, preparedBody?: PreparedRequestBody): Promise<() => void> {
    const id = this.nextRequestID()
    const plan = mode === 'json' ? jsonParams.response(api) : opaqueJSONResource
    const subscription = this.createSubscription(id, mode, plan, decode, onData, observer)
    return new Promise<() => void>((resolve, reject) => {
      const cancelTimeout = scheduleDeadline(this.timeout, () => {
        if (!subscription.acknowledgement || this.subscriptions.get(id) !== subscription) return
        try { this.unsubscribe(id) }
        catch { /* A failed cancellation cannot leave the timed-out promise pending. */ }
        subscription.acknowledgement = undefined
        reject(new LuxoError('TimeoutError', 0, `subscription timed out after ${this.timeout}ms`))
      })
      subscription.acknowledgement = { resolve, reject, cleanup: cancelTimeout }
      this.subscriptions.set(id, subscription)
      try {
        this.sendSubscription(id, api, params, subscription.mode, jsonParams, meta, preparedBody)
      } catch (error) {
        this.subscriptions.delete(id)
        subscription.acknowledgement.cleanup()
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  private createSubscription<T>(id: UInt64, mode: TransportMode, responsePlan: JSONResourcePlan, decode: (data: unknown) => T, onData: (data: T) => void, observer?: StreamObserver): WSSubscription {
    // Keep callback contexts separate from request preparation metadata.
    const subscription: WSSubscription = {
      observer, mode, responsePlan,
      onData: data => {
        let value: T
        try { value = decode(data) }
        catch (error) { this.failSubscriptionDecoding(id, subscription, error); return }
        // Application exceptions must not become protocol failures.
        onData(value)
      },
    }
    return subscription
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
      this.ws.send(enc.bytes().buffer)
      return
    }
    this.ws.send(encodeLuxoJSON({ $unsub: id }))
  }

  private sendSubscription(id: UInt64, api: string, params: RequestParameters | undefined, mode: TransportMode, jsonParams: JSONParamValidator, meta: APISchema | undefined, preparedBody?: PreparedRequestBody): void {
    const ws = this.requireSocket()
    if (mode === 'binary') {
      const enc = new Encoder()
	  enc.writeRawBytes(new Uint8Array([BinaryFrame.subscribe]))
	  enc.writeUint64(id)
	  enc.writeRawBytes(preparedBody instanceof Uint8Array ? preparedBody : this.encodeCallBody(api, params, meta))
      if (preparedBody || this.subscriptions.has(id)) ws.send(enc.bytes().buffer)
      return
    }
    const fragment = typeof preparedBody === 'string' ? preparedBody : generatedJSONParameters(params)
    const message = fragment === undefined
      ? jsonParams.encode(api, { ...jsonParams.prepare(api, requestJSON(params)), $id: id, $sub: api })
      : jsonParams.validateEncoded(api, '{"$id":' + id + ',"$sub":' + encodeLuxoJSON(api) + fragment + '}')
    if (this.subscriptions.has(id)) ws.send(message)
  }

  private encodeCallBody(api: string, params: RequestParameters | undefined, meta: APISchema | undefined): Uint8Array<ArrayBuffer> {
    if (!meta) throw new LuxoError('ConfigError', 0, `no schema for "${api}"`)
    return encodeBinaryBody(meta, params)
  }

  private dispatchJSONResponse(json: Record<string, unknown>, id: UInt64, kind: NonNullable<JSONEnvelopeSource['kind']>): void {
    if (kind === '$end') {
      this.handleJSONStreamEnd(id, json)
      return
    }
    if (kind === '$sub') {
      const failure = Object.hasOwn(json, 'error')
      if (!failure && !(Object.hasOwn(json, 'ok') && json.ok === true)) return
      this.handleSubscriptionAcknowledgement(id, failure
        ? jsonResponseError(json)
        : undefined)
      return
    }
    if (kind === '$stream') {
      if (!Object.hasOwn(json, 'data')) {
        const subscription = this.subscriptions.get(id)
        if (subscription?.mode === 'json' && !subscription.acknowledgement) {
          this.failSubscription(id, subscription, new LuxoError('ParseError', 0, 'missing JSON response data'))
        }
        return
      }
      this.deliverSubscription(id, 'json', json.data)
      return
    }
    const p = this.pending.get(id)
    if (!p) return
    this.pending.delete(id)
    p.cleanup()
    if (Object.hasOwn(json, 'error')) p.reject(jsonResponseError(json))
    else if (Object.hasOwn(json, 'data')) p.resolve(json.data)
    else p.reject(new LuxoError('ParseError', 0, 'missing JSON response data'))
  }

  private handleJSONResponse(data: string, ws: WebSocket): void {
    let source: JSONEnvelopeSource
    try { source = new JSONEnvelopeSource(data) }
    catch { this.invalidJSONMessage(ws); return }
    const { id, kind } = source
    if (id === undefined || kind === undefined) return
    const pending = kind === '$id' ? this.pending.get(id) : undefined
    const subscription = kind === '$id' ? undefined : this.subscriptions.get(id)
    if (kind === '$id' ? !pending?.responsePlan : subscription?.mode !== 'json') return
    if (kind === '$stream' && subscription?.acknowledgement) return
    const plan = pending?.responsePlan ?? subscription!.responsePlan
    try { source.validateData(plan, kind !== '$stream' && source.error) }
    catch (cause) {
      const error = new LuxoError('ParseError', 0, cause instanceof Error ? cause.message : 'invalid JSON response resources')
      if (pending) {
        this.pending.delete(id)
        pending.cleanup()
        pending.reject(error)
      } else if (kind === '$sub') this.handleSubscriptionAcknowledgement(id, error)
      else if (kind === '$end') this.finishSubscription(id, 'json', error)
      else this.failSubscription(id, subscription!, error)
      return
    }
    let json: Record<string, unknown>
    try { json = decodeJSONEnvelope(data, true) }
    catch { this.invalidJSONMessage(ws); return }
    this.dispatchJSONResponse(json, id, kind)
  }

  private invalidJSONMessage(ws: WebSocket): void {
    try { this.disconnect(ws, new LuxoError('ParseError', 0, 'invalid WebSocket JSON message'), true) }
    finally { ws.close() }
  }

  private handleJSONStreamEnd(id: UInt64, json: Record<string, unknown>): void {
    const failure = Object.hasOwn(json, 'error'), acknowledged = Object.hasOwn(json, 'ok')
    const error = failure && !acknowledged ? jsonResponseError(json)
      : !failure && acknowledged && json.ok === true && !Object.hasOwn(json, 'data') ? undefined
      : new LuxoError('ParseError', 0, 'invalid stream completion')
    this.finishSubscription(id, 'json', error)
  }

  private handleBinaryResponse(data: Uint8Array, ws: WebSocket) {
	const header = readWireUint64(data, 1)
	if (!header) { this.invalidBinaryMessage(ws); return }
	if (data[0] === BinaryFrame.subscribeSuccess || data[0] === BinaryFrame.subscribeError) {
	  const { value: id, offset } = header
      if (this.subscriptions.get(id)?.mode !== 'binary') return
      this.handleSubscriptionAcknowledgement(
        id,
        data[0] === BinaryFrame.subscribeError ? decodeBinaryError(data.subarray(offset), 0)
          : offset === data.length ? undefined
          : new LuxoError('ParseError', 0, 'trailing subscription acknowledgement bytes'),
      )
      return
    }
	if (data[0] === BinaryFrame.stream) {
	  const { value: id, offset } = header
      if (offset === data.length) {
        const subscription = this.subscriptions.get(id)
        if (subscription?.mode === 'binary' && !subscription.acknowledgement) {
          this.failSubscription(id, subscription, missingBinaryResponse())
        }
        return
      }
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
	if (data[0] !== BinaryFrame.callSuccess && data[0] !== BinaryFrame.callError) {
      this.invalidBinaryMessage(ws)
      return
    }
	const { value: id, offset: off } = header

    const p = this.pending.get(id)
    if (!p || p.responsePlan !== undefined) return
    this.pending.delete(id)
    p.cleanup()
	if (data[0] === BinaryFrame.callError) {
	  p.reject(decodeBinaryError(data.subarray(off), 0))
	  return
	}
    if (off === data.length) p.reject(missingBinaryResponse())
    else p.resolve(data.subarray(off))
  }

  private invalidBinaryMessage(ws: WebSocket): void {
    try { this.disconnect(ws, new LuxoError('ParseError', 0, 'invalid WebSocket Binary message'), true) }
    finally { ws.close() }
  }

  private deliverSubscription(id: UInt64, mode: TransportMode, data: unknown): void {
    const subscription = this.subscriptions.get(id)
    if (subscription?.mode === mode && !subscription.acknowledgement) subscription.onData(data)
  }

  private failSubscriptionDecoding(id: UInt64, subscription: WSSubscription, cause: unknown): void {
    let message = 'invalid subscription response'
    try {
      const detail = cause instanceof Error ? cause.message : undefined
      if (typeof detail === 'string') message = detail
    } catch { /* Diagnostic accessors must not prevent local retirement. */ }
    const error = new LuxoError('DecodeError', 0, message)
    this.failSubscription(id, subscription, error)
  }

  private failSubscription(id: UInt64, subscription: WSSubscription, error: LuxoError): void {
    if (this.subscriptions.get(id) !== subscription) return
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
    this.credentialRevision++
    this.refreshController?.abort()
    this.cancelReconnect()
    const ws = this.ws
    if (!ws) return
    this.disconnect(ws, new LuxoError('NetworkError', 0, 'WebSocket closed'), false)
    ws.close()
  }
}
