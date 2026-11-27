import { writeFileSync, mkdirSync, existsSync, statSync } from 'fs'
import { join } from 'path'
import type { LuxoSchema, LuxoAPI, LuxoModel, LuxoField, TypeUsage } from '@luxojs/client'
import { validateSchemaIDs } from '@luxojs/client'
import { genSealeds, sealedFields } from './codegen-sealed'
import { genRequests, requestClass } from './codegen-request'
import { genQueryBuilders, isDSLIdentifier } from './codegen-query'
import { genCompleteCheck, genResponseBody, genScalarJSONRead, hasStructuredReturn, isScalar, luxoTypeToTS, schemaEntry } from './codegen-response'

interface GenerationOptions {
  staticRequests?: boolean
  onWrite?: (file: string, contents: string) => void
}

/** Generate TypeScript types + client from Luxo schema */
export async function generateTypes(schema: LuxoSchema, outDir: string, candidate?: GenerationOptions): Promise<void> {
  validateSchemaIDs(schema)
  validateTypeReferences(schema)
  const queries = genQueryBuilders(schema)
  const usages = inferTypeUsages(schema)
  const staticRequests = candidate?.staticRequests ?? true
  const files = [
    ['types.ts', genTypes(schema)],
    ['schema.ts', genSchema(schema)],
    ['requests.ts', genRequests(schema, name => inputTypeName(name, usages), staticRequests)],
    ['client.ts', genClient(schema, staticRequests)],
    ['queries.ts', queries],
  ]
  if (!existsSync(outDir)) {
    mkdirSync(outDir, { recursive: true })
  }
  // Reject an obstructed destination before replacing any generated file.
  for (const [name] of files) {
    const file = join(outDir, name)
    const existing = statSync(file, { throwIfNoEntry: false })
    if (existing && !existing.isFile()) throw new Error(`[luxo] Cannot replace non-file SDK output: ${file}`)
  }
  for (const [name, contents] of files) {
    const file = join(outDir, name)
    writeFileSync(file, contents)
    candidate?.onWrite?.(file, contents)
  }
  console.log(`[luxo] Generated types → ${outDir}/`)
}

// --- types.ts ---

function validateTypeReferences(schema: LuxoSchema): void {
  const validateName = (key: string, name: string, kind = 'declaration', internal = false): void => {
    if (typeof name !== 'string') throw new TypeError(`${kind} name must be a string`)
    if (name.length === 0) throw new Error(`${kind} name must not be empty`)
    if (!internal && !isDSLIdentifier(name)) throw new Error(`invalid ${kind} identifier: ${name}`)
    if (key !== name) throw new Error(`${kind} name ${name} does not match key ${key}`)
  }
  for (const [key, declaration] of Object.entries(schema.enums ?? {})) {
    validateName(key, declaration.name, 'enum declaration')
    const values = declaration.values
    if (!Array.isArray(values) ||
      new Set(values).size !== values.length) {
      throw new Error(`enum ${key} has an invalid declaration`)
    }
    for (let index = 0; index < values.length; index++) {
      if (typeof values[index] !== 'string') throw new Error(`enum ${key} has an invalid declaration`)
    }
  }
  for (const [key, model] of Object.entries(schema.models)) {
    validateName(key, model.name)
    if (schemaEntry(schema.types, key) || schemaEntry(schema.enums, key)) throw new Error(`duplicate type ${key}`)
    validateFieldReferences(schema, model.fields, 'model')
  }
  for (const [key, type] of Object.entries(schema.types ?? {})) {
    validateName(key, type.name)
    if (schemaEntry(schema.enums, key)) throw new Error(`duplicate type ${key}`)
    validateFieldReferences(schema, type.fields, 'model')
  }
  for (const [key, owner] of Object.entries(schema.sealeds ?? {})) {
    validateName(key, owner.name)
    if (schemaEntry(schema.models, key) || schemaEntry(schema.types, key) || schemaEntry(schema.enums, key)) throw new Error(`duplicate type ${key}`)
    const cases = new Set<string>()
    for (const variant of owner.variants) {
      validateName(variant.name, variant.name, 'case')
      if (cases.has(variant.name)) throw new Error(`duplicate ${key} case ${variant.name}`)
      cases.add(variant.name)
      validateFieldReferences(schema, variant.fields, 'sealed payload')
    }
  }
  for (const [key, api] of Object.entries(schema.apis)) {
    validateName(key, api.name, 'declaration', api.internal)
    const type = api.returnType
    if (type === undefined || type === 'Enum' || (!isScalar(type) &&
      !schemaEntry(schema.models, type) && !schemaEntry(schema.types, type) && !schemaEntry(schema.enums, type) && !schemaEntry(schema.sealeds, type))) {
      throw new Error(`API ${api.name} has an undeclared return type ${type}`)
    }
    validateFieldReferences(schema, api.params ?? [], 'input')
  }
}

function validateFieldReferences(schema: LuxoSchema, fields: readonly Pick<LuxoField, 'name' | 'type' | 'typeName'>[], context: string): void {
  const names = new Set<string>()
  for (const field of fields) {
    if (field.name.length === 0) throw new Error(`${context}: member name must not be empty`)
    if (!isDSLIdentifier(field.name)) throw new Error(`invalid ${context} identifier: ${field.name}`)
    if (names.has(field.name)) throw new Error(`${context}: duplicate name ${field.name}`)
    names.add(field.name)
    const { type, typeName: name } = field
    if (type === 'Model' && (!name || (!schemaEntry(schema.models, name) && !schemaEntry(schema.types, name)))) {
      throw new Error(`missing ${context} type ${name ?? '<unnamed>'}`)
    }
    if (type === 'Sealed' && (!name || !schemaEntry(schema.sealeds, name))) throw new Error(`missing sealed type ${name ?? '<unnamed>'}`)
    if (type === 'Enum' && (!name || !schemaEntry(schema.enums, name))) {
      throw new Error(`missing enum type ${name ?? '<unnamed>'}`)
    }
    if (type !== 'Model' && type !== 'Enum' && type !== 'Sealed' && !isScalar(type) && !schemaEntry(schema.models, type) &&
      !schemaEntry(schema.types, type) && !schemaEntry(schema.enums, type) && !schemaEntry(schema.sealeds, type)) {
      throw new Error(`${context}: undeclared type ${type}`)
    }
  }
}

/** Resolve field type to TypeScript, using typeName for model/enum references */
type FieldDirection = 'input' | 'output'

function resolveFieldType(
  field: { type: string; typeName?: string; isList?: boolean; list?: boolean },
  schema: LuxoSchema,
  usages: TypeUsages,
  direction: FieldDirection = 'output',
  selectionType?: string,
): string {
  const isList = field.isList || field.list
  const typeName = field.typeName || field.type
  let ts: string

  // Check if it's an enum
  if (schemaEntry(schema.enums, typeName)) {
    ts = typeName
  } else if (schemaEntry(schema.models, typeName)) {
    ts = direction === 'input' ? inputTypeName(typeName, usages) : outputTypeName(typeName, selectionType)
  } else if (schemaEntry(schema.types, typeName) || schemaEntry(schema.sealeds, typeName)) {
    ts = direction === 'input' ? inputTypeName(typeName, usages) : outputTypeName(typeName, selectionType)
  } else {
    ts = luxoTypeToTS(field.type)
  }

  return isList ? `${ts}[]` : ts
}

function outputTypeName(name: string, selectionType?: string): string {
  return selectionType ? `${name}<${selectionType}>` : name
}

function hasInputUsage(usage: TypeUsage): boolean {
  return usage === 'input' || usage === 'inputOutput'
}

function hasOutputUsage(usage: TypeUsage): boolean {
  return usage === 'output' || usage === 'inputOutput'
}

// This catalog belongs to generation, never to a generated request.
class TypeUsages extends Map<string, TypeUsage> {
  inputAliases?: ReadonlyMap<string, string>
}

function planInputAliases(schema: LuxoSchema, usages: TypeUsages): ReadonlyMap<string, string> | undefined {
  let collisions: string[] | undefined
  for (const [name, usage] of usages) {
    if (usage !== 'inputOutput') continue
    const preferred = `${name}Input`
    if ((usages.get(preferred) ?? 'unused') !== 'unused' || schemaEntry(schema.enums, preferred)) {
      (collisions ??= []).push(name)
    }
  }
  if (!collisions) return undefined
  const reserved = new Set(Object.keys(schema.enums ?? {}))
  for (const [name, usage] of usages) {
    if (usage !== 'unused') reserved.add(name)
    if (usage === 'inputOutput') reserved.add(`${name}Input`)
  }
  const aliases = new Map<string, string>()
  for (const name of collisions.sort()) {
    const fields = schemaEntry(schema.models, name)?.fields ?? schemaEntry(schema.types, name)?.fields ?? []
    const members = new Set(fields.map(field => field.name))
    const base = `Luxo${name}Input`
    let candidate = base
    let ordinal = 2
    while (reserved.has(candidate) || members.has(candidate)) candidate = `${base}${ordinal++}`
    reserved.add(candidate)
    aliases.set(name, candidate)
  }
  return aliases
}

function inputTypeName(name: string, usages: TypeUsages): string {
  return usages.inputAliases?.get(name) ?? (usages.get(name) === 'inputOutput' ? `${name}Input` : name)
}

function mergeUsage(current: TypeUsage, next: TypeUsage): TypeUsage {
  if (current === 'unused') return next
  if (current === next || current === 'inputOutput') return current
  return 'inputOutput'
}

/** Infer usage for schemas produced before the core exposed the usage field. */
function inferTypeUsages(schema: LuxoSchema): TypeUsages {
  const declarations = new Map<string, LuxoModel>()
  for (const model of Object.values(schema.models)) declarations.set(model.name, model)
  for (const type of Object.values(schema.types ?? {})) declarations.set(type.name, type)
  for (const owner of Object.values(schema.sealeds ?? {})) declarations.set(owner.name, { name:owner.name, usage:owner.usage, fields:sealedFields(owner) })

  const usages = new TypeUsages()
  for (const name of declarations.keys()) usages.set(name, 'unused')
  const visited = new Set<string>()
  const mark = (name: string | undefined, usage: 'input' | 'output'): void => {
    if (!name || !declarations.has(name)) return
    const visit = `${usage}:${name}`
    if (visited.has(visit)) return
    visited.add(visit)
    usages.set(name, mergeUsage(usages.get(name) ?? 'unused', usage))
    for (const field of declarations.get(name)?.fields ?? []) {
      mark(field.typeName || field.type, usage)
    }
  }

  for (const api of Object.values(schema.apis)) {
    mark(api.returnType, 'output')
    for (const param of api.params ?? []) mark(param.typeName || param.type, 'input')
  }
  for (const declaration of declarations.values()) {
    if (declaration.usage === 'input' || declaration.usage === 'inputOutput') mark(declaration.name, 'input')
    if (declaration.usage === 'output' || declaration.usage === 'inputOutput') mark(declaration.name, 'output')
    if (!declaration.usage && usages.get(declaration.name) === 'unused') mark(declaration.name, 'output')
  }
  usages.inputAliases = planInputAliases(schema, usages)
  return usages
}

function genTypes(schema: LuxoSchema): string {
  const usages = inferTypeUsages(schema)
  let out = '// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n'
  out += "import type { Page, Selected } from '@luxojs/client'\n"
  const runtimeImports = ['Decoder', 'ColumnarDecoder']
  if (Object.keys(schema.enums ?? {}).length) runtimeImports.push('UnknownEnumValue as $UnknownEnumValue')
  const fields = [
    ...Object.values(schema.models).filter(model => hasOutputUsage(usages.get(model.name) ?? 'unused')).flatMap(model => model.fields),
    ...Object.values(schema.types ?? {}).filter(type => hasOutputUsage(usages.get(type.name) ?? 'unused')).flatMap(type => type.fields),
    ...Object.values(schema.sealeds ?? {}).filter(type => hasOutputUsage(usages.get(type.name) ?? 'unused')).flatMap(sealedFields),
  ]
  // Without any structured edge, every response/list item has one object layer.
  const trackDepth = fields.some(field => isNestedField(field, schema))
  if (fields.some(field => field.type === 'Int' || field.type === 'Duration')) runtimeImports.push('int64FromJSON')
  if (fields.some(field => field.type === 'Float')) runtimeImports.push('floatFromJSON')
  if (fields.some(field => field.type === 'Boolean')) runtimeImports.push('booleanFromJSON')
  if (fields.some(field => field.type === 'DateTime')) runtimeImports.push('dateTimeFromJSON')
  if (fields.some(field => ['String', 'DateTime', 'UUID', 'Decimal', 'Enum', 'Bytes'].includes(field.type)
    || schemaEntry(schema.enums, field.typeName || field.type))) runtimeImports.push('stringFromJSON')
  if (fields.some(field => field.type === 'JSON')) runtimeImports.push('normalizeLuxoJSON')
  if (fields.some(field => field.type === 'Bytes')) runtimeImports.push('decodeBase64')
  if (fields.some(field => field.type === 'JSON')) runtimeImports.push('decodeJSONValue')
  if (fields.some(field => field.type === 'JSON' && !field.isList && !field.list)) runtimeImports.push('validateJSONValue as $validateJSONValue')
  if (fields.some(field => field.type === 'JSON' && (field.isList || field.list))) runtimeImports.push('validateJSONArray as $validateJSONArray')
  if (fields.some(field => (field.isList || field.list) && !isNestedField(field, schema))) runtimeImports.push('decodeScalarArray')
  out += `import { ${runtimeImports.join(', ')} } from '@luxojs/client'\n\n`

  // Known members remain primitives; only unknown members need an owned wrapper.
  if (schema.enums) {
    for (const e of Object.values(schema.enums)) {
      out += genEnum(e)
    }
  }

  // Type declarations (non-DB: AuthPayload, ProjectOverview, etc.)
  if (schema.types) {
    for (const t of Object.values(schema.types)) {
      const usage = usages.get(t.name) ?? 'unused'
      if (hasOutputUsage(usage)) {
        out += genInterface(t.name, t.fields, schema, usages, 'output', false)
        out += genJSONDecode(t as unknown as LuxoModel, schema, trackDepth)

        // Binary decoder for type declarations (same logic as models)
        out += genBinaryDecode(t as unknown as LuxoModel, schema, trackDepth)

        // Columnar + paginated decoder for type declarations
        out += genColumnarDecode(t as unknown as LuxoModel, schema, true, usages, trackDepth)
        out += genPaginatedDecode(t as unknown as LuxoModel, schema, true, usages)
      }
      if (hasInputUsage(usage)) {
        out += genInterface(inputTypeName(t.name, usages), t.fields, schema, usages, 'input')
      }
    }
  }

  // Model interfaces
  for (const model of Object.values(schema.models)) {
    const usage = usages.get(model.name) ?? 'unused'
    if (hasOutputUsage(usage)) {
      out += genInterface(model.name, model.fields, schema, usages, 'output', true)
      out += genJSONDecode(model, schema, trackDepth)
      out += genBinaryDecode(model, schema, trackDepth)
      out += genColumnarDecode(model, schema, true, usages, trackDepth)
      out += genPaginatedDecode(model, schema, true, usages)
    }
    if (hasInputUsage(usage)) {
      out += genInterface(inputTypeName(model.name, usages), model.fields, schema, usages, 'input')
    }
  }

  out += genSealeds(schema, { usage:name => usages.get(name) ?? 'unused', inputName:name => inputTypeName(name, usages),
    fieldType:(field, input) => resolveFieldType(field, schema, usages, input ? 'input' : 'output', input ? undefined : 'Selection'),
    binary:field => genFieldRead(field, schema), json:(field, value) => jsonValueDecode(field, schema, value),
  })
  out += 'export type { Page }\n'
  return out
}

function genEnum(declaration: { name: string; values: string[] }): string {
  const { name, values } = declaration
  const members = values.map(value => JSON.stringify(value))
  let out = `export type ${name} = ${[...members, `$UnknownEnumValue<'${name}'>`].join(' | ')}\n`
  out += `export const ${name}Values = [${members.join(', ')}] as const\n\n`
  out += `export function decodeEnum$${name}(raw: string): ${name} {\n  switch (raw) {\n`
  for (const member of members) out += `    case ${member}: return raw\n`
  out += `    default: return new $UnknownEnumValue('${name}', raw)\n  }\n}\n\n`
  return out
}

function genInterface(
  name: string,
  fields: LuxoField[],
  schema: LuxoSchema,
  usages: TypeUsages,
  direction: FieldDirection,
  model: boolean = false,
): string {
  const selectionType = direction === 'output' ? 'Selection' : undefined
  let out = direction === 'output'
    ? `export interface ${name}<Selection extends boolean = false> {\n`
    : `export interface ${name} {\n`
  for (const field of fields) {
    const ts = resolveFieldType(field, schema, usages, direction, selectionType)
    const value = field.nullable ? `${ts} | null` : ts
    if (direction === 'input') {
      out += `  ${field.name}: ${value}\n`
    } else if (model && field.relation) {
      out += `  ${field.name}: Selected<${value}>\n`
    } else {
      out += `  ${field.name}: true extends Selection ? Selected<${value}> : ${value}\n`
    }
  }
  return out + '}\n\n'
}

const inheritedObjectFields = new Set([
  '__proto__', 'constructor', 'toString', 'toLocaleString', 'valueOf',
  'hasOwnProperty', 'isPrototypeOf', 'propertyIsEnumerable',
  '__defineGetter__', '__defineSetter__', '__lookupGetter__', '__lookupSetter__',
])

function decodedObjectLiteral(model: LuxoModel): string {
  const fields = model.fields.filter(field => inheritedObjectFields.has(field.name))
  if (fields.length === 0) return '{}'
  // Own slots preserve Selected<T> and bypass the inherited __proto__ setter.
  return `{ ${fields.map(field => `[${JSON.stringify(field.name)}]: undefined`).join(', ')} }`
}

function literalPropertyName(name: string, quoted = false): string {
  return name === '__proto__' ? "['__proto__']" : quoted ? `'${name}'` : name
}

function genBinaryDecode(model: LuxoModel, schema: LuxoSchema, trackDepth: boolean): string {
  let out = `export function decode${model.name}(dec: Decoder): ${model.name}<true> {\n`
  if (trackDepth) {
    out += `  return decode${model.name}$value(dec, 1)\n}\n\n`
    out += `function decode${model.name}$value(dec: Decoder, depth: number): ${model.name}<true> {\n`
    out += `  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n`
  }
  out += `  const obj: { [key: string]: unknown } = ${decodedObjectLiteral(model)}\n`
  out += `  dec.skipArenaHeader()\n`
  out += `  while (dec.nextField()) {\n`
  out += `    switch (dec.fieldID) {\n`
  for (const field of model.fields) {
    out += `      case ${field.id}: obj['${field.name}'] = ${genFieldRead(field, schema)}; break\n`
  }
  out += `      default: throw new Error(\`unknown ${model.name} field ID \${dec.fieldID}\`)\n`
  out += `    }\n`
  out += `  }\n`
  out += genCompleteCheck('dec', 'dec.remaining !== 0', `${model.name} response`)
  out += `  return obj as unknown as ${model.name}<true>\n`
  return out + '}\n\n'
}

// Keep unary decoders usable as one-argument map callbacks. List items share the
// field pass; '$' helper names cannot collide with a DSL model identifier.
function genJSONDecode(model: LuxoModel, schema: LuxoSchema, trackDepth: boolean): string {
  let out = `export function decodeJSON${model.name}(data: { [key: string]: unknown }): ${model.name}<true> {\n`
  out += `  return decodeJSON${model.name}$value(data, false${trackDepth ? ', 1' : ''})\n}\n\n`
  out += genJSONListDecode(model.name, trackDepth)
  out += `function decodeJSON${model.name}$value(data: { [key: string]: unknown }, requireField: boolean${trackDepth ? ', depth: number' : ''}): ${model.name}<true> {\n`
  if (trackDepth) out += `  if (depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n`
  out += `  if (requireField && (data === null || typeof data !== 'object' || Array.isArray(data))) throw new TypeError('${model.name} list item must be an object')\n`
  out += `  const obj = ${decodedObjectLiteral(model)} as ${model.name}<true>\n`
  out += `  let hasField = false\n`
  for (const field of model.fields) {
    const raw = `data['${field.name}']`
    const decoded = jsonValueDecode(field, schema, raw)
    out += `  if (Object.prototype.hasOwnProperty.call(data, '${field.name}')) {\n`
    out += `    hasField = true\n`
    if (field.nullable) {
      out += `    obj.${field.name} = ${raw} == null ? null : ${decoded}\n`
    } else {
      const jsonScalar = field.type === 'JSON' && !field.isList && !field.list
      out += `    if (${raw} ${jsonScalar ? '=== undefined' : '== null'}) throw new TypeError('${model.name}.${field.name} cannot be ${jsonScalar ? 'undefined' : 'null'}')\n`
      out += `    obj.${field.name} = ${decoded}\n`
    }
    out += `  }\n`
  }
  out += `  if (requireField && !hasField) throw new TypeError('${model.name} list item requires at least one Schema field')\n`
  out += `  return obj\n`
  out += `}\n\n`
  return out
}

function genJSONListDecode(name: string, trackDepth: boolean): string {
  let out = `export function decodeJSON${name}$list(data: unknown): ${name}<true>[] {\n`
  if (trackDepth) {
    out += `  return decodeJSON${name}$items(data, 1)\n}\n\n`
    out += `function decodeJSON${name}$items(data: unknown, depth: number): ${name}<true>[] {\n`
  }
  out += `  if (!Array.isArray(data)) throw new TypeError('${name} list must be an array')\n`
  out += `  const items = new Array<${name}<true>>(data.length)\n`
  out += `  for (let i = 0; i < items.length; i++) {\n`
  out += `    items[i] = decodeJSON${name}$value(data[i] as { [key: string]: unknown }, true${trackDepth ? ', depth' : ''})\n`
  out += `  }\n  return items\n}\n\n`
  return out
}

function jsonValueDecode(field: LuxoField, schema: LuxoSchema, value: string): string {
  const typeName = field.typeName || field.type
  const list = field.isList || field.list
  const raw = list ? 'item' : value
  const enumField = Boolean(schemaEntry(schema.enums, typeName))
  if (schemaEntry(schema.sealeds, typeName)) {
    return list ? `decodeJSON${typeName}$items(${value}, depth + 1)` : `decodeJSON${typeName}$value(${raw} as Record<string, unknown>, depth + 1)`
  }
  if (isNestedField(field, schema)) {
    return list ? `decodeJSON${typeName}$items(${value}, depth + 1)` : `decodeJSON${typeName}$value(${raw} as { [key: string]: unknown }, false, depth + 1)`
  }
  const decoded = enumField ? `decodeEnum$${typeName}(stringFromJSON(${raw}))` : genScalarJSONRead(field.type, raw)
  return list ? `(${value} as unknown[]).map(item => ${decoded})` : decoded
}

/** TypeScript array type for a column variable */
function isNestedField(field: LuxoField, schema: LuxoSchema): boolean {
  const typeName = field.typeName || field.type
  return Boolean(field.relation || schemaEntry(schema.models, typeName) || schemaEntry(schema.types, typeName) || schemaEntry(schema.sealeds, typeName))
}

function columnTSType(field: LuxoField, schema: LuxoSchema): string {
  if (isNestedField(field, schema) || field.isList || field.list || field.type === 'Bytes' || field.type === 'JSON') {
    return field.nullable ? '(Uint8Array | null)[]' : 'Uint8Array[]'
  }
  const n = field.nullable
  switch (field.type) {
    case 'Int': case 'Duration': return n ? '(bigint | null)[]' : 'bigint[]'
    case 'Float': return n ? '(number | null)[]' : 'number[]'
    // DateTime is represented as an RFC3339 string in both JSON and binary modes.
    case 'String': case 'Enum': case 'UUID': case 'Decimal': case 'DateTime':
      return n ? '(string | null)[]' : 'string[]'
    case 'Boolean':
      return n ? '(boolean | null)[]' : 'boolean[]'
    default:
      return n ? '(string | null)[]' : 'string[]'
  }
}

function genColumnRead(field: LuxoField, schema: LuxoSchema): string {
  if (isNestedField(field, schema) || field.isList || field.list || field.type === 'Bytes' || field.type === 'JSON') {
    return field.nullable ? 'r.readColumnBytesPtr()' : 'r.readColumnBytes()'
  }
  const n = field.nullable
  switch (field.type) {
    case 'Int': case 'Duration':
      return n ? 'r.readColumnIntPtr()' : 'r.readColumnInt()'
    // DateTime columns carry seconds/nanoseconds pairs, matching JSON precision.
    case 'DateTime':
      return n ? 'r.readColumnDateTimePtr()' : 'r.readColumnDateTime()'
    case 'Float':
      return n ? 'r.readColumnFloatPtr()' : 'r.readColumnFloat()'
    case 'UUID':
      return n ? 'r.readColumnUUIDPtr()' : 'r.readColumnUUID()'
    case 'String': case 'Enum': case 'Decimal':
      return n ? 'r.readColumnStringPtr()' : 'r.readColumnString()'
    case 'Boolean':
      return n ? 'r.readColumnBoolPtr()' : 'r.readColumnBool()'
    default:
      return n ? 'r.readColumnStringPtr()' : 'r.readColumnString()'
  }
}

function columnValue(field: LuxoField, schema: LuxoSchema, usages: TypeUsages, depth = '1'): string {
  const column = `_${field.name}`
  const typeName = field.typeName || field.type
  if (isNestedField(field, schema)) {
    if (field.isList || field.list) {
      const item = field.nullable ? `${column}[i]!` : `${column}[i]`
      const decoded = schemaEntry(schema.sealeds, typeName)
        ? `decodeSealedList${typeName}$value(new Decoder(${item}), ${depth} + 1, true)`
        : `decodeColumnar${typeName}$value(${item}, ${depth} + 1)`
      return selectedColumnValue(column, decoded, field.nullable)
    }
    const decoded = `decode${typeName}$value(new Decoder(${column}[i]!), ${depth} + 1)`
    if (field.nullable) {
      return `${column} ? (${column}[i] ? ${decoded} : null) : undefined`
    }
    return `${column} ? ${decoded} : undefined`
  }
  if (field.isList || field.list) {
    const ts = resolveFieldType({ ...field, isList: false, list: false }, schema, usages)
    const item = field.nullable ? `${column}[i]!` : `${column}[i]`
    const decoded = schemaEntry(schema.enums, typeName)
      ? `(decodeScalarArray(${item}, 'Enum') as string[]).map(decodeEnum$${typeName})`
      : `decodeScalarArray(${item}, '${field.type}') as ${ts}[]`
    return selectedColumnValue(column, decoded, field.nullable)
  }
  if (schemaEntry(schema.enums, typeName)) return selectedColumnValue(column, `decodeEnum$${typeName}(${column}[i]!)`, field.nullable)
  if (field.type === 'JSON') {
    const decoded = `decodeJSONValue(${column}![i]!)`
    return field.nullable
      ? `${column} ? (${column}[i] ? ${decoded} : null) : undefined`
      : `${column} ? ${decoded} : undefined`
  }
  return `${column} ? ${column}[i] : undefined`
}

function selectedColumnValue(column: string, decoded: string, nullable?: boolean): string {
  const value = nullable ? `(${column}[i] === null ? null : ${decoded})` : decoded
  return `${column} ? ${value} : undefined`
}

function genColumnCase(field: LuxoField, schema: LuxoSchema, usages: TypeUsages, depth = '1'): string {
  let out = `      case ${field.id}:`
  if (isNestedField(field, schema) || field.isList || field.list || field.type === 'JSON') {
    // Raw cells are decoded after reading columns; validate every retired value.
    // Only duplicates need checking; generic JSON never needs a discarded tree.
    out += `\n        if (_${field.name} !== undefined) {\n`
    out += `          for (let i = 0; i < _${field.name}.length; i++) {\n`
    out += `            ${columnResourceCheck(field, schema, usages, depth)}\n`
    out += `          }\n        }\n       `
  }
  return out + ` _${field.name} = ${genColumnRead(field, schema)}; break\n`
}

function columnResourceCheck(field: LuxoField, schema: LuxoSchema, usages: TypeUsages, depth: string): string {
  if (field.type !== 'JSON' || isNestedField(field, schema)) return `void (${columnValue(field, schema, usages, depth)})`
  const value = `_${field.name}[i]!`
  const check = `${field.isList || field.list ? '$validateJSONArray' : '$validateJSONValue'}(${value})`
  return field.nullable ? `if (_${field.name}[i] !== null) ${check}` : check
}

function columnarFields(model: LuxoModel, includeNested: boolean): LuxoField[] {
  return includeNested ? model.fields : model.fields.filter(f => !f.relation)
}

function genColumnarDecode(model: LuxoModel, schema: LuxoSchema, includeNested: boolean, usages: TypeUsages, trackDepth: boolean): string {
  const fields = columnarFields(model, includeNested)
  let out = `export function decodeColumnar${model.name}(data: Uint8Array): ${model.name}<true>[] {\n`
  if (trackDepth) {
    out += `  return decodeColumnar${model.name}$value(data, 1)\n}\n\n`
    out += `function decodeColumnar${model.name}$value(data: Uint8Array, depth: number): ${model.name}<true>[] {\n`
  }
  out += `  const r = new ColumnarDecoder(data)\n`
  if (trackDepth) out += `  if (r.count !== 0 && depth > 64) throw new RangeError('model nesting exceeds 64 levels')\n`
  // Typed column variables — zero Record overhead, zero type assertion
  for (const f of fields) {
    out += `  let _${f.name}: ${columnTSType(f, schema)} | undefined\n`
  }
  out += `  while (r.nextColumn()) {\n`
  out += `    switch (r.fieldID) {\n`
  for (const f of fields) {
    out += genColumnCase(f, schema, usages, 'depth')
  }
  out += `      default: throw new Error(\`unknown ${model.name} column ID \${r.fieldID}\`)\n`
  out += `    }\n`
  out += `  }\n`
  out += genCompleteCheck('r', 'r.offset() !== data.length', `${model.name} columnar response`)
  out += `  const items: ${model.name}<true>[] = new Array(r.count)\n`
  out += `  for (let i = 0; i < r.count; i++) {\n`
  out += `    items[i] = {\n`
  for (const f of fields) {
    out += `      ${literalPropertyName(f.name)}: ${columnValue(f, schema, usages, 'depth')},\n`
  }
  out += `    } as unknown as ${model.name}<true>\n`
  out += `  }\n`
  out += `  return items\n`
  out += '}\n\n'
  return out
}

function genPaginatedDecode(model: LuxoModel, schema: LuxoSchema, includeNested: boolean, usages: TypeUsages): string {
  const fields = columnarFields(model, includeNested)
  let out = `export function decodePaginated${model.name}(data: Uint8Array): Page<${model.name}<true>> {\n`
  out += `  const r = new ColumnarDecoder(data)\n`
  for (const f of fields) {
    out += `  let _${f.name}: ${columnTSType(f, schema)} | undefined\n`
  }
  out += `  while (r.nextColumn()) {\n`
  out += `    switch (r.fieldID) {\n`
  for (const f of fields) {
    out += genColumnCase(f, schema, usages)
  }
  out += `      default: throw new Error(\`unknown ${model.name} column ID \${r.fieldID}\`)\n`
  out += `    }\n`
  out += `  }\n`
  out += `  if (r.error) throw new Error(r.error)\n`
  out += `  const items: ${model.name}<true>[] = new Array(r.count)\n`
  out += `  for (let i = 0; i < r.count; i++) {\n`
  out += `    items[i] = {\n`
  for (const f of fields) {
    out += `      ${literalPropertyName(f.name)}: ${columnValue(f, schema, usages)},\n`
  }
  out += `    } as unknown as ${model.name}<true>\n`
  out += `  }\n`
  out += `  const total = r.readSvarint()\n`
  out += `  const page = r.readSvarint()\n`
  out += `  const pageSize = r.readSvarint()\n`
  out += genCompleteCheck('r', 'r.offset() !== data.length', `${model.name} pagination`)
  out += `  return { items, total, page, pageSize }\n`
  out += '}\n\n'
  return out
}

function genFieldRead(field: { type: string; typeName?: string; nullable?: boolean; isList?: boolean; list?: boolean; relation?: boolean }, schema: LuxoSchema): string {
  if (field.list) return genFieldRead({ ...field, isList: true, list: false }, schema)
  const n = field.nullable
  if (field.isList && n) {
    return `dec.readBool() ? ${genFieldRead({ ...field, nullable: false }, schema)} : null`
  }

  // Check for nested model/type reference first (relation fields or typeName pointing to a model/type)
  const tn = field.typeName || field.type
  if (schemaEntry(schema.sealeds, tn)) {
    const read = field.isList ? `decodeSealedList${tn}$value(dec, depth + 1)` : `decode${tn}$value(new Decoder(dec.readBytes()), depth + 1)`
    return n ? `dec.readNullable(() => ${read})` : read
  }
  if (schemaEntry(schema.enums, tn)) {
    if (field.isList) return `dec.readStringArray().map(decodeEnum$${tn})`
    const decoded = `decodeEnum$${tn}(dec.readString())`
    return n ? `dec.readNullable(() => ${decoded})` : decoded
  }
  if (field.relation || (tn && (schemaEntry(schema.models, tn) || schemaEntry(schema.types, tn)))) {
    if (field.isList) {
      return `decodeColumnar${tn}$value(dec.readBytes(), depth + 1)`
    }
    const decoded = `decode${tn}$value(new Decoder(dec.readBytes()), depth + 1)`
    return n ? `dec.readNullable(() => ${decoded})` : decoded
  }

  if (field.isList) {
    switch (field.type) {
      case 'Int': case 'Duration': return 'dec.readIntArray()'
      case 'DateTime': return 'dec.readDateTimeArray()'
      case 'Float': return 'dec.readFloatArray()'
      case 'String': case 'Decimal': case 'Enum': return 'dec.readStringArray()'
      case 'Boolean': return 'dec.readBoolArray()'
      case 'UUID': return 'dec.readUUIDArray()'
      case 'Bytes': return 'dec.readBytesArray()'
      case 'JSON': return 'dec.readBytesArray().map(decodeJSONValue)'
      default: return '[]'
    }
  }

  switch (field.type) {
    case 'Int': case 'Duration': return n ? 'dec.readIntPtr()' : 'dec.readInt()'
    case 'Float': return n ? 'dec.readFloatPtr()' : 'dec.readFloat()'
    // Decode exact seconds/nanoseconds pairs to RFC3339 strings, like JSON mode.
    case 'DateTime': return n ? 'dec.readDateTimePtr()' : 'dec.readDateTime()'
    // UUID is a fixed 16-byte wire value (not a length-prefixed string) — use the
    // dedicated codec method so generated code matches the protocol.
    case 'UUID': return n ? 'dec.readUUIDPtr()' : 'dec.readUUID()'
    case 'String': case 'Decimal': case 'Enum':
      return n ? 'dec.readStringPtr()' : 'dec.readString()'
    case 'Boolean': return n ? 'dec.readBoolPtr()' : 'dec.readBool()'
    case 'Bytes': return n ? 'dec.readNullable(() => dec.readBytes())' : 'dec.readBytes()'
    case 'JSON': return n ? 'dec.readNullable(() => decodeJSONValue(dec.readBytes()))' : 'decodeJSONValue(dec.readBytes())'
    default: return 'null' // unknown type — should not happen with valid schema
  }
}

// --- schema.ts ---

function genSchema(schema: LuxoSchema): string {
  let out = '// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n'
  out += "import type { APISchema } from '@luxojs/client'\n\n"

  out += 'const LUXO_SELECTION_TYPES = {\n'
  const selectionTypes: Record<string, { name: string; fields: LuxoField[] }> = { ...(schema.types ?? {}), ...schema.models }
  for (const owner of Object.values(schema.sealeds ?? {})) {
    const fields = owner.variants.map(variant => ({ id: variant.id, name: variant.name, type: 'Model', typeName: `${owner.name}.${variant.name}` }))
    selectionTypes[owner.name] = { name: owner.name, fields }
    selectionTypes[`@sealed:${owner.name}`] = { name: `@sealed:${owner.name}`, fields }
    for (const variant of owner.variants) selectionTypes[`${owner.name}.${variant.name}`] = { name: `${owner.name}.${variant.name}`, fields: variant.fields }
  }
  for (const type of Object.values(selectionTypes)) {
    const fields = type.fields.map(field => {
      const typeName = field.typeName || field.type
	  const declaredType = field.typeName ? `, typeName: '${typeName}'` : ''
	  return `${literalPropertyName(field.name, true)}: { fieldID: ${field.id}, type: '${field.type}'${declaredType}${field.isList || field.list ? ', isList: true' : ''}${field.nullable ? ', nullable: true' : ''} }`
    }).join(', ')
    out += `  ${literalPropertyName(type.name, true)}: { ${fields} },\n`
  }
  out += '}\n\n'

  out += '/** API schema map — binary encoding and declared JSON resource validation. */\n'
  out += 'export const LUXO_SCHEMA: { [key: string]: APISchema } = {\n'
  for (const api of Object.values(schema.apis)) {
    if (api.internal) continue
    out += `  ${literalPropertyName(api.name, true)}: { id: ${api.id}`
    const returnFields = api.returnType
      ? schemaEntry(selectionTypes, api.returnType)?.fields
      : undefined
    const returnEnum = api.returnType && schemaEntry(schema.enums, api.returnType)
    const responseType = schemaEntry(schema.sealeds, api.returnType ?? '') ? 'Sealed' : returnFields ? 'Model' : returnEnum ? 'Enum' : api.returnType || 'Int'
    out += `, response: { type: '${responseType}'${returnFields || returnEnum ? `, typeName: '${api.returnType}'` : ''}${api.returnList && !api.paginated ? ', isList: true' : ''}${api.paginated ? ', paginated: true' : ''} }`
    if (returnFields && returnFields.length > 0) {
	  out += `, fields: LUXO_SELECTION_TYPES['${api.returnType}']`
	}
	if (returnFields || api.params?.some(param => param.type === 'Model' || param.type === 'Sealed')) {
	  out += ', types: LUXO_SELECTION_TYPES'
    }
    if (api.params && api.params.length > 0) {
      out += ', params: [\n'
      for (const p of api.params) {
		out += `    { fieldID: ${p.id}, name: '${p.name}', type: '${p.type}'${p.typeName ? `, typeName: '${p.typeName}'` : ''}${p.isList ? ', isList: true' : ''}${p.nullable ? ', nullable: true' : ''} },\n`
      }
      out += '  ]'
    }
    out += ' },\n'
  }
  out += '}\n'
  return out
}

// --- client.ts ---

function genClientImports(schema: LuxoSchema, usages: TypeUsages, staticRequests: boolean): string {
  let out = '// Auto-generated by @luxo/vite-plugin. DO NOT EDIT.\n\n'
  const streamImport = Object.values(schema.apis).some(api => api.stream) ? ', StreamObserver' : ''
  out += `import type { ${staticRequests ? 'GeneratedTransport as Transport' : 'Transport'}, TransportOptions, CallOptions, Page, Filter, Sorter, PreparedSelection${streamImport} } from '@luxojs/client'\n`
  const runtimeImports = ['FetchTransport', 'WsTransport']
  const decodeExpressions = Object.values(schema.apis).map(api => genResponseBody(api, schema, getReturnType(api, schema), '  ')).join('\n')
  for (const helper of ['Decoder', 'decodeBase64', 'decodeJSONValue', 'unixSecondsToISO', 'int64FromJSON', 'floatFromJSON', 'booleanFromJSON', 'stringFromJSON', 'dateTimeFromJSON', 'normalizeLuxoJSON']) {
    if (new RegExp(`\\b${helper}\\b`).test(decodeExpressions)) runtimeImports.push(helper)
  }
  out += `import { ${runtimeImports.join(', ')} } from '@luxojs/client'\n`
  out += "import { LUXO_SCHEMA } from './schema.js'\n"
  const requests = Object.values(schema.apis).filter(api => !api.internal).map(requestClass)
  if (requests.length) out += `import { ${requests.join(', ')} } from './requests.js'\n`

  // Collect model type imports + decoder imports
  const typeImports = new Set<string>()
  const decoderImports = new Set<string>()
  for (const api of Object.values(schema.apis)) {
    if (api.returnType && !isScalar(api.returnType)) {
      typeImports.add(api.returnType)
    }
    if (api.returnType && schemaEntry(schema.enums, api.returnType)) decoderImports.add(`decodeEnum$${api.returnType}`)
    if (hasStructuredReturn(api, schema)) {
      if (api.paginated) {
        decoderImports.add(`decodePaginated${api.returnType}`)
      } else if (api.returnList) {
        decoderImports.add(`${schemaEntry(schema.sealeds, api.returnType!) ? 'decodeSealedList' : 'decodeColumnar'}${api.returnType}`)
      } else {
        decoderImports.add(`decode${api.returnType}`)
      }
      decoderImports.add(`decodeJSON${api.returnType}${api.paginated || api.returnList ? '$list' : ''}`)
    }
		for (const param of api.params ?? []) {
			const typeName = param.typeName || param.type
			if (schemaEntry(schema.enums, typeName) || schemaEntry(schema.models, typeName) || schemaEntry(schema.types, typeName) || schemaEntry(schema.sealeds, typeName)) {
				typeImports.add(inputTypeName(typeName, usages))
			}
		}
  }
  if (typeImports.size > 0) {
    out += `import type { ${Array.from(typeImports).join(', ')} } from './types.js'\n`
	}
	if (decoderImports.size > 0) {
    out += `import { ${Array.from(decoderImports).join(', ')} } from './types.js'\n`
  }
  return out
}

function genClient(schema: LuxoSchema, staticRequests: boolean): string {
  const usages = inferTypeUsages(schema)
  let out = genClientImports(schema, usages, staticRequests)
  out += '\nexport class LuxoClient {\n'
  out += '  private transport: Transport\n\n'

  // Constructor: accepts Transport directly
  out += '  constructor(transport: Transport) {\n'
  out += '    this.transport = transport\n'
  out += '    transport.setSchema(LUXO_SCHEMA)\n'
  out += '  }\n\n'

  // Factory: auto-detect transport from URL scheme
  out += '  /** Create client from URL — auto-detects HTTP vs WebSocket transport. */\n'
  out += '  static create(endpoint: string, options?: TransportOptions): LuxoClient {\n'
  out += '    const transport = endpoint.startsWith(\'ws\') ? new WsTransport(endpoint, options) : new FetchTransport(endpoint, options)\n'
  out += '    return new LuxoClient(transport)\n'
  out += '  }\n\n'

  out += '  /** Switch transport mode (json for debug, binary for production). */\n'
  out += '  setMode(mode: \'json\' | \'binary\'): void { this.transport.setMode(mode) }\n\n'
  out += '  /** Update auth token. */\n'
  out += '  setToken(token: string | null): void { this.transport.setToken(token) }\n\n'
  out += '  /** Close transport (WebSocket). */\n'
  out += '  close(): void { this.transport.close?.() }\n\n'
  out += '  static readonly schema = LUXO_SCHEMA\n\n'

  for (const api of Object.values(schema.apis)) {
    if (api.internal) continue
    out += genMethod(api, schema, usages, staticRequests)
  }

  out += '}\n'
  for (const api of Object.values(schema.apis)) {
    if (api.internal || api.stream) continue
    out += genResponseDecoder(api, schema)
  }
  return out
}

function resolveParamType(
  p: { type: string; typeName?: string; name: string; isList?: boolean },
  schema: LuxoSchema,
  usages: TypeUsages,
): string {
  let type: string
  const typeName = p.typeName || p.type
  if (schemaEntry(schema.enums, typeName)) type = typeName
  else if (schemaEntry(schema.models, typeName)) type = inputTypeName(typeName, usages)
  else if (schemaEntry(schema.types, typeName) || schemaEntry(schema.sealeds, typeName)) type = inputTypeName(typeName, usages)
  else type = luxoTypeToTS(p.type)
  return p.isList ? `${type}[]` : type
}

function hasSelectableReturn(api: LuxoAPI, schema: LuxoSchema): boolean {
  const owner = schemaEntry(schema.sealeds, api.returnType ?? '')
  return owner ? owner.variants.length !== 0 : hasStructuredReturn(api, schema)
}

function genMethod(api: LuxoAPI, schema: LuxoSchema, usages: TypeUsages, staticRequests: boolean): string {
  const structuredReturn = hasSelectableReturn(api, schema)
  const selectionFlag = structuredReturn ? 'Select extends string | PreparedSelection ? true : false' : undefined
  const retTS = getReturnType(api, schema, selectionFlag)
  if (api.stream) return genStreamMethod(api, schema, usages, retTS, staticRequests)
  const paginationNames = new Set(['page', 'pageSize'])
  const typeParams = structuredReturn ? '<Select extends string | PreparedSelection | undefined = undefined>' : ''
  const call = (params: string) =>
    `    try {\n` +
    `      return decodeResponse$${api.name}${structuredReturn ? '<Select>' : ''}(this.transport.${staticRequests ? 'callGenerated' : 'call'}('${api.name}', ${staticRequests ? `${requestClass(api)}, ${params || 'undefined'}` : `new ${requestClass(api)}(${params})`}, options))\n` +
    `    } catch (error) { return globalThis.Promise.reject(error) }\n`

  // If API has explicit params, always use them for the signature (no guessing by name prefix)
  if (api.params && api.params.length > 0) {
    const paramFields = api.params.map(p => {
      if ((api.paginated && paginationNames.has(p.name)) || p.hasDefault) {
        return `${p.name}?: ${resolveParamType(p, schema, usages)}${p.nullable ? ' | null' : ''}`
      }
      return `${p.name}: ${resolveParamType(p, schema, usages)}${p.nullable ? ' | null' : ''}`
    })
    if (structuredReturn) paramFields.push('$select?: Select')
    if (api.paginated) {
      paramFields.push('$filters?: Filter[]', '$sorters?: Sorter[]')
    }
    const hasRequiredParam = api.params.some(p => !p.hasDefault && !(api.paginated && paginationNames.has(p.name)))
    const optional = hasRequiredParam ? '' : '?'
    return `  ${api.name}${typeParams}(params${optional}: { ${paramFields.join('; ')} }, options?: CallOptions): globalThis.Promise<${retTS}> {\n` +
           call('params') + `  }\n\n`
  }


  if (structuredReturn) {
    return `  ${api.name}${typeParams}(params?: { $select?: Select }, options?: CallOptions): globalThis.Promise<${retTS}> {\n` +
           call('params') + `  }\n\n`
  }

  return `  ${api.name}(options?: CallOptions): globalThis.Promise<${retTS}> {\n` +
         call('') + `  }\n\n`
}

function genResponseDecoder(api: LuxoAPI, schema: LuxoSchema): string {
  const structuredReturn = hasSelectableReturn(api, schema)
  const typeParams = structuredReturn ? '<Select extends string | PreparedSelection | undefined = undefined>' : ''
  const retTS = getReturnType(api, schema, structuredReturn ? 'Select extends string | PreparedSelection ? true : false' : undefined)
  return `\nasync function decodeResponse$${api.name}${typeParams}(pending: globalThis.Promise<unknown>): globalThis.Promise<${retTS}> {\n` +
    `  ${api.returnNullable ? 'let' : 'const'} d = await pending\n` +
    genResponseBody(api, schema, retTS, '  ') + '}\n'
}

function genStreamMethod(api: LuxoAPI, schema: LuxoSchema, usages: TypeUsages, retTS: string, staticRequests: boolean): string {
  const methodName = `subscribe${api.name.charAt(0).toUpperCase()}${api.name.slice(1)}`
  const structuredReturn = hasSelectableReturn(api, schema)
  const paramFields = api.params?.map(param => {
    const optional = param.hasDefault ? '?' : ''
    const nullable = param.nullable ? ' | null' : ''
    return `${param.name}${optional}: ${resolveParamType(param, schema, usages)}${nullable}`
  }) ?? []
  if (structuredReturn) paramFields.push('$select?: Select')
  const fields = paramFields.join('; ')
  const params = fields ? `params: { ${fields} }, ` : ''
  const values = staticRequests ? `${requestClass(api)}, ${fields ? 'params' : 'undefined'}` : `new ${requestClass(api)}(${fields ? 'params' : ''})`
  const typeParams = structuredReturn ? '<Select extends string | PreparedSelection | undefined = undefined>' : ''
  return `  ${methodName}${typeParams}(${params}onData: (data: ${retTS}) => void, observer?: StreamObserver): globalThis.Promise<() => void> {\n` +
    `    return this.transport.${staticRequests ? 'subscribeGenerated' : 'subscribe'}('${api.name}', ${values}, d => {\n` +
    genResponseBody(api, schema, retTS, '      ') + `    }, onData, observer)\n` +
    `  }\n\n`
}

function getReturnType(api: LuxoAPI, schema: LuxoSchema, selectionFlag?: string): string {
  if (!api.returnType) return 'bigint'
  const base = luxoTypeToTS(api.returnType)
  const ts = selectionFlag && hasStructuredReturn(api, schema) ? `${base}<${selectionFlag}>` : base
  const result = api.paginated ? `Page<${ts}>` : api.returnList ? `${ts}[]` : ts
  return api.returnNullable ? `${result} | null` : result
}
