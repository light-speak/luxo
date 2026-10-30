import { PreparedSelection } from './selection'
import type { Encoder } from './codec'
import type { Filter, Sorter } from './types'

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

/** Dynamic callers such as Studio explicitly retain schema-driven encoding. */
export type RequestParameters = GeneratedRequest | Record<string, unknown>

export function requestJSON(request?: RequestParameters): Record<string, unknown> | undefined {
  const params = request instanceof GeneratedRequest ? request.toJSONParameters() : request
  return params?.$select instanceof PreparedSelection ? { ...params, $select: params.$select.text } : params
}
