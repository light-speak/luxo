import type { APISchema } from './transport'

export interface WireFieldInspection {
  fieldID: number
  name: string
  type: string
  isList: boolean
  itemCount?: number
  encodedBytes: number
}

export interface WireRequestInspection {
  apiID: number
  api: string
  frameBytes: number
  selectionBytes: number
  selectedFields: string[]
  fields: WireFieldInspection[]
}

class WireReader {
  offset = 0

  constructor(private readonly data: Uint8Array) {}

  readVarint(label: string): number {
    let value = 0
    let multiplier = 1
    while (this.offset < this.data.length) {
      const byte = this.data[this.offset++]
      value += (byte & 0x7f) * multiplier
      if (byte < 0x80) return value
      multiplier *= 128
      if (multiplier > Number.MAX_SAFE_INTEGER) throw new Error(`${label} exceeds safe integer range`)
    }
    throw new Error(`truncated ${label}`)
  }

  skip(length: number, label: string): void {
    if (!Number.isSafeInteger(length) || length < 0 || this.offset + length > this.data.length) {
      throw new Error(`truncated ${label}`)
    }
    this.offset += length
  }
}

function skipScalar(reader: WireReader, type: string): void {
  switch (type) {
    case 'Int':
    case 'Duration':
    case 'DateTime':
    case 'Boolean':
      reader.readVarint(`${type} value`)
      return
    case 'Float':
      reader.skip(8, 'Float value')
      return
    case 'UUID':
      reader.skip(16, 'UUID value')
      return
    case 'String':
    case 'Enum':
    case 'Decimal':
    case 'Bytes':
    case 'Model': {
      const length = reader.readVarint(`${type} length`)
      reader.skip(length, `${type} value`)
      return
    }
    default:
      throw new Error(`unsupported field type ${type}`)
  }
}

function selectedFieldNames(mask: Uint8Array, meta: APISchema): string[] {
  if (!meta.fields) return []
  const selected: string[] = []
  for (const [name, fieldID] of Object.entries(meta.fields)) {
    const byte = mask[fieldID >>> 3]
    if (byte !== undefined && (byte & (1 << (fieldID & 7))) !== 0) selected.push(name)
  }
  return selected
}

/** Inspect a binary HTTP request without decoding or retaining parameter values. */
export function inspectBinaryRequest(
  data: Uint8Array,
  schema: Record<string, APISchema>,
): WireRequestInspection {
  const reader = new WireReader(data)
  const apiID = reader.readVarint('API ID')
  const apiEntry = Object.entries(schema).find(([, meta]) => meta.id === apiID)
  if (!apiEntry) throw new Error(`unknown API ID ${apiID}`)
  const [api, meta] = apiEntry
  const selectionBytes = reader.readVarint('field mask length')
  const maskStart = reader.offset
  reader.skip(selectionBytes, 'field mask')
  const mask = data.subarray(maskStart, reader.offset)
  const params = new Map((meta.params ?? []).map(param => [param.fieldID, param]))
  const fields: WireFieldInspection[] = []

  while (true) {
    const fieldID = reader.readVarint('field ID')
    if (fieldID === 0) break
    const param = params.get(fieldID)
    if (!param) throw new Error(`unknown field ID ${fieldID} for API ${api}`)
    const valueStart = reader.offset
    let itemCount: number | undefined
    if (param.isList) {
      itemCount = reader.readVarint(`${param.name} item count`)
      for (let index = 0; index < itemCount; index++) skipScalar(reader, param.type)
    } else {
      skipScalar(reader, param.type)
    }
    fields.push({
      fieldID,
      name: param.name,
      type: param.type,
      isList: param.isList ?? false,
      ...(itemCount === undefined ? {} : { itemCount }),
      encodedBytes: reader.offset - valueStart,
    })
  }
  if (reader.offset !== data.length) throw new Error(`unexpected ${data.length - reader.offset} trailing bytes`)

  return {
    apiID,
    api,
    frameBytes: data.length,
    selectionBytes,
    selectedFields: selectedFieldNames(mask, meta),
    fields,
  }
}
