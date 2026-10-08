import type { LuxoAPI, LuxoSchema } from '@luxojs/client'

const scalarTypes: Record<string, string> = {
  Int: 'bigint', Duration: 'bigint', Float: 'number', Boolean: 'boolean',
  String: 'string', DateTime: 'string', UUID: 'string', Decimal: 'string',
  Enum: 'string', Bytes: 'Uint8Array', JSON: 'unknown',
}

export function isScalar(type: string): boolean {
  return Object.prototype.hasOwnProperty.call(scalarTypes, type)
}

export function luxoTypeToTS(type: string): string {
  return isScalar(type) ? scalarTypes[type] : type
}

export function hasStructuredReturn(api: LuxoAPI, schema: LuxoSchema): boolean {
  return Boolean(api.returnType && !isScalar(api.returnType) && !schema.enums?.[api.returnType])
}

export function genCompleteCheck(decoder: string, trailing: string, context: string, indent = '  '): string {
  return `${indent}if (${decoder}.error !== null || ${trailing}) throw new Error(${decoder}.error ?? 'trailing ${context} data')\n`
}

/** Emit one decoder shared by unary responses and subscription decoding. */
export function genResponseBody(api: LuxoAPI, schema: LuxoSchema, returnType: string, indent: string): string {
  const prefix = api.returnNullable ? genNullableRoot(indent) : ''
  return prefix + genPresentResponseBody(api, schema, returnType, indent)
}

function genNullableRoot(indent: string): string {
  return `${indent}if (d instanceof Uint8Array) {\n` +
    `${indent}  if (d.length === 0 || d[0] > 1 || (d[0] === 0 ? d.length !== 1 : d.length === 1)) throw new Error('invalid nullable root envelope')\n` +
    `${indent}  if (d[0] === 0) return null\n` +
    `${indent}  d = d.subarray(1)\n` +
    `${indent}} else if (d === null) return null\n`
}

function genPresentResponseBody(api: LuxoAPI, schema: LuxoSchema, returnType: string, indent: string): string {
  const emit = (value: string, prefix = indent) => `${prefix}return ${value}\n`
  if (hasStructuredReturn(api, schema)) return emit(`(${modelDecode(api)}) as unknown as ${returnType}`)
  const enumReturn = Boolean(api.returnType && schema.enums?.[api.returnType])
  const type = enumReturn ? 'Enum' : api.returnType || 'Int'

  const inner = indent + '  '
  const list = Boolean(api.returnList)
  let out = `${indent}if (d instanceof Uint8Array) {\n${inner}const dec = new Decoder(d)\n`
  out += `${inner}const value = ${scalarRead(type, list)}\n`
  out += genCompleteCheck('dec', 'dec.remaining !== 0', `${type}${list ? '[]' : ''} response`, inner)
  const decoder = `decodeEnum$${api.returnType}`
  out += emit(enumReturn ? (list ? `value.map(${decoder})` : `${decoder}(value)`) : 'value', inner) + `${indent}}\n`
  const jsonItem = (value: string) => enumReturn ? `${decoder}(${genScalarJSONRead(type, value)})` : genScalarJSONRead(type, value)
  const json = list ? `(d as unknown[]).map(item => ${jsonItem('item')})` : jsonItem('d')
  return out + emit(json)
}

function scalarRead(type: string, list: boolean): string {
  const suffix = list ? 'Array' : ''
  switch (type) {
    case 'Int': case 'Duration': return `dec.readInt${suffix}()`
    case 'Float': return `dec.readFloat${suffix}()`
    case 'Boolean': return `dec.readBool${suffix}()`
    case 'UUID': return `dec.readUUID${suffix}()`
    case 'Bytes': return `dec.readBytes${suffix}()`
    case 'DateTime': return list ? 'dec.readDateTimeArray()' : 'dec.readDateTime()'
    case 'JSON': return list ? 'dec.readBytesArray().map(decodeJSONValue)' : 'decodeJSONValue(dec.readBytes())'
    default: return `dec.readString${suffix}()`
  }
}

export function genScalarJSONRead(type: string, value: string): string {
  switch (type) {
    case 'Int': case 'Duration': return `int64FromJSON(${value})`
    case 'Float': return `floatFromJSON(${value})`
    case 'Boolean': return `booleanFromJSON(${value})`
    case 'DateTime': return `dateTimeFromJSON(${value})`
    case 'String': case 'UUID': case 'Decimal': case 'Enum': return `stringFromJSON(${value})`
    case 'Bytes': return `decodeBase64(stringFromJSON(${value}))`
    case 'JSON': return `normalizeLuxoJSON(${value})`
    default: return `${value} as ${luxoTypeToTS(type)}`
  }
}

function modelDecode(api: LuxoAPI): string {
  const type = api.returnType
  if (api.paginated) {
    const page = `(d as Record<string, unknown>)`
    return `d instanceof Uint8Array ? decodePaginated${type}(d) : { items: decodeJSON${type}$list(${page}['items']), total: int64FromJSON(${page}['total']), page: int64FromJSON(${page}['page']), pageSize: int64FromJSON(${page}['pageSize']) }`
  }
  if (api.returnList) {
    return `d instanceof Uint8Array ? decodeColumnar${type}(d) : decodeJSON${type}$list(d)`
  }
  return `d instanceof Uint8Array ? decode${type}(new Decoder(d)) : decodeJSON${type}(d as Record<string, unknown>)`
}
