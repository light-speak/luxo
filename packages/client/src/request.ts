import { PreparedSelection } from './selection'
import type { Encoder } from './codec'
import type { Filter, Sorter } from './types'
import { assertInt64 } from './int64'
import { encodeBoundedLuxoJSON, encodeLuxoJSON } from './json'

/** Shared controls remain on the original generated parameter object. */
export interface RequestValues {
  readonly [name: string]: unknown
  readonly $select?: string | PreparedSelection
  readonly $filters?: readonly Filter[]
  readonly $sorters?: readonly Sorter[]
}

/** One immutable encoder per API, never a per-call wrapper. */
export interface RequestEncoder<P extends RequestValues> {
  readonly apiID: number
  readonly initialCapacity: (values: P | undefined) => number
  readonly writeParameters: (encoder: Encoder, values: P | undefined) => void
  readonly writeJSONParameters?: (writer: JSONRequestWriter, values: P | undefined) => void
}

/** Generated JSON codecs write known fields directly into the wire document. */
export class JSONRequestWriter {
  private source = ''
  writeRaw(source: string): void { this.source += source }
  writeValue(value: unknown): void { this.source += encodeLuxoJSON(value) }
  writeInt64(value: bigint): void { assertInt64(value); this.source += value.toString() }
  writeJSON(value: unknown): void { this.source += encodeBoundedLuxoJSON(value) }
  writeBytes(value: Uint8Array): void {
    if (!(value instanceof Uint8Array)) throw new TypeError('Bytes requires Uint8Array')
    const bytes = Uint8Array.prototype as Uint8Array & { toBase64(): string }
    this.writeValue(bytes.toBase64.call(value))
  }
  toString(): string { return this.source }
}

/** Generated code owns parameter types and wire IDs; transports own network I/O. */
export abstract class GeneratedRequest {
  abstract readonly apiID: number
  /** A bounded allocation hint, never a substitute for parameter validation. */
  get initialCapacity(): number { return 1024 }
  get selection(): string | PreparedSelection | undefined { return undefined }
  get filters(): readonly Filter[] | undefined { return undefined }
  get sorters(): readonly Sorter[] | undefined { return undefined }
  abstract writeParameters(encoder: Encoder): void
  abstract toJSONParameters(): Record<string, unknown>
}

/** Optional static codec capability; declaration merging adds no instance slot. */
export interface GeneratedRequest {
  writeJSONParameters?(writer: JSONRequestWriter): void
}

/** Dynamic callers such as Studio explicitly retain schema-driven encoding. */
export type RequestParameters = GeneratedRequest | Record<string, unknown>

export function requestJSON(request?: RequestParameters): Record<string, unknown> | undefined {
  const params = request instanceof GeneratedRequest ? request.toJSONParameters() : request
  return params?.$select instanceof PreparedSelection ? { ...params, $select: params.$select.text } : params
}

export function staticJSONParameters<P extends RequestValues>(codec: RequestEncoder<P>, params?: P): string | undefined {
  if (!codec.writeJSONParameters) return undefined
  const writer = new JSONRequestWriter()
  codec.writeJSONParameters(writer, params)
  writeJSONControls(writer, params?.$select, params?.$filters, params?.$sorters)
  return writer.toString()
}

export function generatedJSONParameters(request?: RequestParameters): string | undefined {
  if (!(request instanceof GeneratedRequest) || !request.writeJSONParameters) return undefined
  const writer = new JSONRequestWriter()
  request.writeJSONParameters(writer)
  writeJSONControls(writer, request.selection, request.filters, request.sorters)
  return writer.toString()
}

function writeJSONControls(writer: JSONRequestWriter, select?: string | PreparedSelection, filters?: readonly Filter[], sorters?: readonly Sorter[]): void {
  if (select !== undefined) {
    writer.writeRaw(',"$select":')
    writer.writeValue(select instanceof PreparedSelection ? select.text : select)
  }
  if (filters !== undefined) { writer.writeRaw(',"$filters":'); writer.writeValue(filters) }
  if (sorters !== undefined) { writer.writeRaw(',"$sorters":'); writer.writeValue(sorters) }
}
