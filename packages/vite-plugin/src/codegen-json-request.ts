import type { LuxoAPI, LuxoField, LuxoParam, LuxoSchema } from '@luxojs/client'
import { schemaEntry } from './codegen-response'
import { sealedCaseName, sealedFields } from './codegen-sealed'

type Field = LuxoField | LuxoParam
type InputName = (name: string) => string

export function needsStaticJSON(api: LuxoAPI, schema: LuxoSchema): boolean {
  const seen = new Set<string>()
  const visit = (field: Field): boolean => {
    if (field.type === 'Sealed') return true
    if (field.type !== 'Model' || !field.typeName || seen.has(field.typeName)) return false
    seen.add(field.typeName)
    const declaration = schemaEntry(schema.types, field.typeName) ?? schemaEntry(schema.models, field.typeName)
    return declaration?.fields.some(visit) ?? false
  }
  return api.params?.some(visit) ?? false
}

export function genJSONParameters(api: LuxoAPI, staticEncoding: boolean): string {
  let out = staticEncoding ? '  writeJSONParameters(writer, values): void {\n'
    : '  writeJSONParameters(writer: JSONRequestWriter): void {\n    const values = this.values\n'
  for (const field of api.params ?? []) {
    const source = `values?.[${JSON.stringify(field.name)}]`
    if (!field.hasDefault && !(api.paginated && ['page','pageSize'].includes(field.name))) out += `    if (${source} === undefined) throw new TypeError('missing required ${api.name}.${field.name}')\n`
    out += writeField(field, source, `,${JSON.stringify(field.name)}:`, '1', '    ')
  }
  return out + `  }${staticEncoding ? ',' : ''}\n`
}

export function genJSONInputCodecs(schema: LuxoSchema, names: Iterable<string>, inputName: InputName): string {
  let out = ''
  for (const name of names) {
    const owner = schemaEntry(schema.sealeds, name)
    if (owner) {
      out += `function writeJSON$${name}(writer: JSONRequestWriter, source: Types.${inputName(name)}, depth = 1): void {\n`
      out += "  if (depth + 1 > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
      if (!owner.variants.length) { out += `  throw new TypeError('${name} has no values')\n}\n\n`; continue }
      out += `  if (!source || typeof source !== 'object' || Array.isArray(source)) throw new TypeError('${name} must be a sealed value')\n  switch (source.$case) {\n`
      for (const variant of owner.variants) {
        out += `    case '${variant.name}':\n      writer.writeRaw(${JSON.stringify(`{${JSON.stringify(variant.name)}:`)})\n`
        out += `      writeJSON$${sealedCaseName(name, variant)}(writer, source, depth + 1)\n      writer.writeRaw('}')\n      return\n`
      }
      out += `    default: throw new TypeError('unknown ${name} sealed case')\n  }\n}\n\n`
      for (const variant of owner.variants) out += genObject(sealedCaseName(name, variant),
        `Types.${sealedCaseName(inputName(name), variant)}`, variant.fields, true)
    } else {
      const declaration = schemaEntry(schema.types, name) ?? schemaEntry(schema.models, name)
      if (!declaration) throw new Error(`missing input type ${name}`)
      out += genObject(name, `Types.${inputName(name)}`, declaration.fields, false)
    }
  }
  return out
}

function genObject(name: string, type: string, fields: readonly Field[], required: boolean): string {
  let out = `function writeJSON$${name}(writer: JSONRequestWriter, source: ${type}, depth = 1): void {\n`
  out += "  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n"
  out += `  if (!source || typeof source !== 'object' || Array.isArray(source)) throw new TypeError('${name} payload must be an object')\n`
  if (required) for (const field of fields) {
    if (!('hasDefault' in field && field.hasDefault)) out += `  if (source[${JSON.stringify(field.name)}] === undefined) throw new TypeError('missing required ${name}.${field.name}')\n`
  }
  out += "  writer.writeRaw('{')\n"
  if (fields.length) out += '  let first = true\n'
  for (const field of fields) out += writeField(field, `source[${JSON.stringify(field.name)}]`, undefined, 'depth + 1', '  ')
  return out + "  writer.writeRaw('}')\n}\n\n"
}

function writeField(field: Field, expression: string, prefix: string | undefined, depth: string, indent: string): string {
  let out = `${indent}{ const value = ${expression}\n${indent}  if (value !== undefined) {\n`
  if (prefix === undefined) out += `${indent}    if (!first) writer.writeRaw(',')\n${indent}    first = false\n`
  out += `${indent}    writer.writeRaw(${JSON.stringify(prefix ?? `${JSON.stringify(field.name)}:`)})\n`
  if (field.nullable) out += `${indent}    if (value === null) { writer.writeRaw('null') } else {\n`
  else if (field.type !== 'JSON' || field.isList || ('list' in field && field.list)) out += `${indent}    if (value === null) throw new TypeError('${field.name} is not nullable')\n`
  if (field.isList || ('list' in field && field.list)) {
    out += `${indent}    if (!Array.isArray(value)) throw new TypeError('${field.name} must be a list')\n${indent}    writer.writeRaw('[')\n`
    out += `${indent}    for (let index = 0; index < value.length; index++) {\n${indent}      if (index) writer.writeRaw(',')\n`
    out += writeScalar(field, 'value[index]', depth, indent + '      ')
    out += `${indent}    }\n${indent}    writer.writeRaw(']')\n`
  } else out += writeScalar(field, 'value', depth, indent + '    ')
  if (field.nullable) out += `${indent}    }\n`
  return out + `${indent}  }\n${indent}}\n`
}

function writeScalar(field: Field, source: string, depth: string, indent: string): string {
  switch (field.type) {
    case 'Model': case 'Sealed': return `${indent}writeJSON$${field.typeName}(writer, ${source}, ${depth})\n`
    case 'Int': case 'Duration': return `${indent}writer.writeInt64(${source})\n`
    case 'Bytes': return `${indent}writer.writeBytes(${source})\n`
    case 'JSON': return `${indent}writer.writeJSON(${source})\n`
    case 'DateTime': return `${indent}writer.writeValue(dateTimeFromJSON(${source}))\n`
    case 'Enum': return `${indent}writer.writeValue(enumRawValue(${source}, ${JSON.stringify(field.typeName)}))\n`
    case 'String': case 'UUID': case 'Decimal': return `${indent}writer.writeValue(stringFromJSON(${source}))\n`
    case 'Boolean': return `${indent}writer.writeValue(booleanFromJSON(${source}))\n`
    case 'Float': return `${indent}writer.writeValue(${source})\n`
    default: throw new Error(`unsupported static JSON type ${field.type}`)
  }
}

export function jsonInputNames(schema: LuxoSchema, apis: readonly LuxoAPI[]): Set<string> {
  const names = new Set<string>()
  const visit = (field: Field): void => {
    if ((field.type !== 'Model' && field.type !== 'Sealed') || !field.typeName || names.has(field.typeName)) return
    names.add(field.typeName)
    const owner = schemaEntry(schema.sealeds, field.typeName)
    const fields = owner ? sealedFields(owner)
      : (schemaEntry(schema.types, field.typeName) ?? schemaEntry(schema.models, field.typeName))?.fields ?? []
    for (const nested of fields) visit(nested)
  }
  for (const api of apis) for (const field of api.params ?? []) visit(field)
  return names
}
