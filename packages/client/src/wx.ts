import { encodeBinaryBody, encodeStaticRequestBody } from './transport'
import { decodeBinaryError, jsonResponseError, missingBinaryResponse } from './error-codec'
import type { APISchema, CallOptions, Transport, TransportMode } from './transport'
import { LuxoError } from './error'
import { decodeJSONEnvelope, encodeLuxoJSON } from './json'
import { JSONParamValidator } from './json-params'
import { JSONEnvelopeSource, JSONResourceError } from './json-resource'
import type { JSONResourcePlan } from './json-resource'
import { snapshotAPISchema } from './schema-runtime'
import { requestJSON, staticJSONParameters, generatedJSONParameters } from './request'
import type { RequestParameters, RequestEncoder, RequestValues } from './request'

interface WxRequestOptions {
  url: string
  method: string
  header: Record<string, string>
  data: string | ArrayBuffer
  dataType?: string
  responseType?: 'text' | 'arraybuffer'
  success: (res: { statusCode: number; data: unknown }) => void
  fail: (err: { errMsg: string }) => void
}

interface WxRequestTask {
  abort(): void
}

declare const wx: {
  request(options: WxRequestOptions): WxRequestTask | undefined
}

function cancelledRequest(): LuxoError {
  return new LuxoError('CancelledError', 0, 'request cancelled')
}

function abortWxTask(task: WxRequestTask | undefined): void {
  try { task?.abort() } catch { /* The request has already settled as cancelled. */ }
}

function sendWxRequest(options: WxRequestOptions, signal: AbortSignal | undefined, reject: (error: unknown) => void): void {
  if (signal) sendCancellableWxRequest(options, signal, reject)
  else wx.request(options)
}

function sendCancellableWxRequest(options: WxRequestOptions, signal: AbortSignal, reject: (error: unknown) => void): void {
  // A parameter encoder can abort after the public entry's initial check.
  if (signal.aborted) {
    reject(cancelledRequest())
    return
  }

  const { success, fail } = options
  let settled = false
  let cancelled = false
  let task: WxRequestTask | undefined
  const finish = (): boolean => {
    if (settled) return false
    settled = true
    task = undefined
    signal.removeEventListener('abort', cancel)
    return true
  }
  const cancel = (): void => {
    const started = task
    if (!finish()) return
    cancelled = true
    reject(cancelledRequest())
    abortWxTask(started)
  }
  options.success = response => { if (finish()) success(response) }
  options.fail = error => { if (finish()) fail(error) }

  try {
    signal.addEventListener('abort', cancel, { once: true })
    if (signal.aborted) cancel()
    if (settled) return
    const started = wx.request(options)
    // The host can settle the request synchronously before returning a task.
    if (cancelled) abortWxTask(started)
    else if (!settled) task = started
  } catch (error) {
    if (finish()) reject(error)
  }
}

function decodeWxJSON(data: unknown, status: number, responsePlan: JSONResourcePlan): unknown {
  if (typeof data !== 'string') {
    throw new LuxoError('ParseError', status, 'expected raw JSON response text; pre-parsed values may have lost integer precision')
  }
  let json: Record<string, unknown>
  try {
    const source = new JSONEnvelopeSource(data)
    source.validateData(responsePlan, source.error || status !== 200)
    json = decodeJSONEnvelope(data, true)
  } catch (error) {
    throw new LuxoError('ParseError', status, error instanceof JSONResourceError ? error.message : 'invalid JSON response')
  }
  if (status !== 200 || Object.hasOwn(json, 'error')) throw jsonResponseError(json, status)
  if (!Object.hasOwn(json, 'data')) throw new LuxoError('ParseError', status, 'missing JSON response data')
  return json.data
}

function decodeWxBinary(data: unknown, status: number): Uint8Array {
  if (!(data instanceof ArrayBuffer)) throw new LuxoError('ParseError', status, 'expected binary response bytes')
  const bytes = new Uint8Array(data)
  if (status !== 200) throw decodeBinaryError(bytes, status)
  if (bytes.length === 0) throw missingBinaryResponse(status)
  return bytes
}

/** WeChat Mini Program transport (wx.request) — supports JSON and binary modes */
export class WxTransport implements Transport {
  private jsonParams: JSONParamValidator
  private headers: Record<string, string> = {}
  private mode: TransportMode
  private schema: Record<string, APISchema>

  constructor(
    private endpoint: string,
    options?: { token?: string; mode?: TransportMode; schema?: Record<string, APISchema> },
  ) {
    this.mode = options?.mode ?? 'json'
    this.schema = snapshotAPISchema(options?.schema ?? {})
    this.jsonParams = new JSONParamValidator(this.schema)
    if (options?.token) {
      this.headers['Authorization'] = `Bearer ${options.token}`
    }
  }

  async call<T = unknown>(api: string, params?: RequestParameters, options?: CallOptions): Promise<T> {
    const signal = options?.signal
    if (signal?.aborted) throw cancelledRequest()
    if (this.mode === 'binary') {
      return this.callBinary<T>(api, params, signal)
    }
    return this.callJSON<T>(api, params, signal)
  }

  async callGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P, options?: CallOptions): Promise<unknown> {
    const signal = options?.signal
    if (signal?.aborted) throw cancelledRequest()
    const meta = this.schema[api]
    if (!meta || meta.id !== codec.apiID) throw new LuxoError('ConfigError', 0, 'generated request API ID differs from registered schema')
    const jsonParams = this.jsonParams
    return this.mode === 'binary'
      ? this.callBinary(api, undefined, signal, encodeStaticRequestBody(meta, codec, params))
      : this.callJSON(api, params, signal, staticJSONParameters(codec, params), jsonParams)
  }

  private callJSON<T>(api: string, params: RequestParameters | undefined, signal: AbortSignal | undefined, prepared?: string, jsonParams = this.jsonParams): Promise<T> {
    const responsePlan = jsonParams.response(api)
    const fragment = prepared ?? generatedJSONParameters(params)
    let body: string
    if (fragment === undefined) {
      const values = { $api: api, ...jsonParams.prepare(api, requestJSON(params)) }
      values.$api = api
      body = jsonParams.encode(api, values)
    } else body = jsonParams.validateEncoded(api, '{"$api":' + encodeLuxoJSON(api) + fragment + '}')

    return new Promise((resolve, reject) => {
      sendWxRequest({
        url: this.endpoint,
        method: 'POST',
        header: { 'Content-Type': 'application/json', ...this.headers },
        data: body,
        dataType: 'text',
        responseType: 'text',
        success(res) {
          try { resolve(decodeWxJSON(res.data, res.statusCode, responsePlan) as T) } catch (error) { reject(error) }
        },
        fail(err) {
          reject(new LuxoError('NetworkError', 0, err.errMsg))
        },
      }, signal, reject)
    })
  }

  private callBinary<T>(api: string, params: RequestParameters | undefined, signal: AbortSignal | undefined, preparedBody?: Uint8Array<ArrayBuffer>): Promise<T> {
    let body = preparedBody
    if (!body) {
      const apiMeta = this.schema[api]
      if (!apiMeta) {
        return Promise.reject(new LuxoError('ConfigError', 0, `no schema for API "${api}" — binary mode requires schema`))
      }
      body = encodeBinaryBody(apiMeta, params)
    }

    return new Promise((resolve, reject) => {
      sendWxRequest({
        url: this.endpoint,
        method: 'POST',
        header: { 'Content-Type': 'application/x-luxo', 'X-Luxo-Mode': 'binary', ...this.headers },
		data: new Uint8Array(body).buffer,
        responseType: 'arraybuffer',
        success(res) {
          try { resolve(decodeWxBinary(res.data, res.statusCode) as T) } catch (error) { reject(error) }
        },
        fail(err) {
          reject(new LuxoError('NetworkError', 0, err.errMsg))
        },
      }, signal, reject)
    })
  }

  setToken(token: string | null): void {
    if (token) {
      this.headers['Authorization'] = `Bearer ${token}`
      return
    }
    delete this.headers['Authorization']
  }

  setMode(mode: TransportMode): void {
    this.mode = mode
  }

  setSchema(schema: Record<string, APISchema>): void {
    const snapshot = snapshotAPISchema(schema)
    this.jsonParams = new JSONParamValidator(snapshot)
    this.schema = snapshot
  }

  subscribe<T>(
	_api: string,
	_params: RequestParameters,
	_decode: (data: unknown) => T,
	_onData: (data: T) => void,
  ): Promise<() => void> {
    return Promise.reject(new LuxoError('ConfigError', 0, 'subscriptions require a WebSocket endpoint'))
  }

  subscribeGenerated<P extends RequestValues, T>(_api: string, _codec: RequestEncoder<P>, _params: P | undefined, _decode: (data: unknown) => T, _onData: (data: T) => void): Promise<() => void> {
    return Promise.reject(new LuxoError('ConfigError', 0, 'subscriptions require a WebSocket endpoint'))
  }
}
