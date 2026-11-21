import { encodeBinaryBody, encodeStaticRequestBody } from './transport'
import { decodeBinaryError, jsonResponseError, missingBinaryResponse } from './error-codec'
import type { Transport, TransportMode, APISchema } from './transport'
import { LuxoError } from './error'
import { decodeJSONEnvelope, encodeLuxoJSON } from './json'
import { JSONParamValidator } from './json-params'
import { JSONEnvelopeSource, JSONResourceError } from './json-resource'
import type { JSONResourcePlan } from './json-resource'
import { snapshotAPISchema } from './schema-runtime'
import { requestJSON, staticJSONParameters, generatedJSONParameters } from './request'
import type { RequestParameters, RequestEncoder, RequestValues } from './request'

declare const wx: {
  request(options: {
    url: string
    method: string
    header: Record<string, string>
    data: string | ArrayBuffer
    dataType?: string
    responseType?: 'text' | 'arraybuffer'
    success: (res: { statusCode: number; data: unknown }) => void
    fail: (err: { errMsg: string }) => void
  }): void
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

  async call<T = unknown>(api: string, params?: RequestParameters): Promise<T> {
    if (this.mode === 'binary') {
      return this.callBinary<T>(api, params)
    }
    return this.callJSON<T>(api, params)
  }

  async callGenerated<P extends RequestValues>(api: string, codec: RequestEncoder<P>, params?: P): Promise<unknown> {
    const meta = this.schema[api]
    if (!meta || meta.id !== codec.apiID) throw new LuxoError('ConfigError', 0, 'generated request API ID differs from registered schema')
    const jsonParams = this.jsonParams
    return this.mode === 'binary'
      ? this.callBinary(api, undefined, encodeStaticRequestBody(meta, codec, params))
      : this.callJSON(api, params, staticJSONParameters(codec, params), jsonParams)
  }

  private callJSON<T>(api: string, params?: RequestParameters, prepared?: string, jsonParams = this.jsonParams): Promise<T> {
    const responsePlan = jsonParams.response(api)
    const fragment = prepared ?? generatedJSONParameters(params)
    let body: string
    if (fragment === undefined) {
      const values = { $api: api, ...jsonParams.prepare(api, requestJSON(params)) }
      values.$api = api
      body = jsonParams.encode(api, values)
    } else body = jsonParams.validateEncoded(api, '{"$api":' + encodeLuxoJSON(api) + fragment + '}')

    return new Promise((resolve, reject) => {
      wx.request({
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
      })
    })
  }

  private callBinary<T>(api: string, params?: RequestParameters, preparedBody?: Uint8Array<ArrayBuffer>): Promise<T> {
    let body = preparedBody
    if (!body) {
      const apiMeta = this.schema[api]
      if (!apiMeta) {
        return Promise.reject(new LuxoError('ConfigError', 0, `no schema for API "${api}" — binary mode requires schema`))
      }
      body = encodeBinaryBody(apiMeta, params)
    }

    return new Promise((resolve, reject) => {
      wx.request({
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
      })
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
