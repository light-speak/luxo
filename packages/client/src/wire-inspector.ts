import { fieldMaskHas } from './codec'
import type { APISchema } from './transport'

const FILTERS_FIELD_ID = 0x7ffffffe
const SORTERS_FIELD_ID = 0x7fffffff

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

  readBytes(length: number, label: string): Uint8Array {
    const start = this.offset
    this.skip(length, label)
    return this.data.subarray(start, this.offset)
  }

  readBool(label: string): void {
    const value = this.readBytes(1, label)[0]
    if (value !== 0 && value !== 1) throw new Error(`invalid ${label}`)
  }

  get done(): boolean {
    return this.offset === this.data.length
  }
}

function skipString(reader: WireReader, label: string): void {
  reader.skip(reader.readVarint(`${label} length`), label)
}

function inspectListControl(reader: WireReader, fieldID: number): WireFieldInspection {
  const valueStart = reader.offset
  const itemCount = reader.readVarint(fieldID === FILTERS_FIELD_ID ? '$filters count' : '$sorters count')
  const isFilter = fieldID === FILTERS_FIELD_ID
  const limit = isFilter ? 1000 : 100
  if (itemCount > limit) throw new Error(`${isFilter ? '$filters' : '$sorters'} exceeds ${limit} entries`)
  for (let index = 0; index < itemCount; index++) {
    skipString(reader, `${isFilter ? 'filter' : 'sorter'} field`)
    if (isFilter) {
      const operatorID = reader.readVarint('filter operator')
      if (operatorID < 1 || operatorID > 10) throw new Error(`invalid filter operator ${operatorID}`)
      skipString(reader, 'filter value')
    } else {
      reader.readBool('sorter direction')
    }
  }
  return {
    fieldID,
    name: isFilter ? '$filters' : '$sorters',
    type: isFilter ? 'Filter' : 'Sorter',
    isList: true,
    itemCount,
    encodedBytes: reader.offset - valueStart,
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
	if (mask.length === 0 || !meta.fields) return []
  return selectedNodeFields(mask, meta.fields, meta.types ?? {})
}

function selectedNodeFields(
  node: Uint8Array,
  fields: NonNullable<APISchema['fields']>,
  types: NonNullable<APISchema['types']>,
): string[] {
  const reader = new WireReader(node)
  const bitmap = reader.readBytes(reader.readVarint('selection bitmap length'), 'selection bitmap')
  const selected = Object.entries(fields)
    .filter(([, field]) => fieldMaskHas(bitmap, field.fieldID))
    .map(([name]) => name)
  const byID = new Map(Object.entries(fields).map(([name, field]) => [field.fieldID, { name, field }]))
  while (!reader.done) {
    const fieldID = reader.readVarint('selection child field ID')
    const child = reader.readBytes(reader.readVarint('selection child length'), 'selection child')
    const parent = byID.get(fieldID)
    if (!parent || !selected.includes(parent.name) || !parent.field.typeName) {
      throw new Error(`invalid nested selection field ${fieldID}`)
    }
    const nested = types[parent.field.typeName]
    if (!nested) throw new Error(`unknown nested selection type ${parent.field.typeName}`)
    const children = selectedNodeFields(child, nested, types)
    selected[selected.indexOf(parent.name)] = `${parent.name}{${children.join(',')}}`
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
    if (fieldID === FILTERS_FIELD_ID || fieldID === SORTERS_FIELD_ID) {
      fields.push(inspectListControl(reader, fieldID))
      continue
    }
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
