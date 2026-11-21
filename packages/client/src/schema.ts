import type { LuxoSchema } from './types'

export const MAX_SELECTION_FIELD_ID = 65535
export const MAX_API_ID = 2147483647
export const MAX_PARAM_ID = MAX_API_ID - 2

const schemaFlags = {
  api: ['internal', 'returnList', 'returnNullable', 'paginated', 'stream'],
  field: ['nullable', 'isList', 'list', 'relation'],
  param: ['nullable', 'isList', 'hasDefault'],
  response: ['isList', 'paginated'],
}

/** Shared by generation and registration; never called while encoding business values. */
export function checkSchemaFlags(value: object, kind: keyof typeof schemaFlags, owner: string, name: string): void {
  const metadata = value as Record<string, unknown>
  for (const flag of schemaFlags[kind]) {
    const flagValue = metadata[flag]
    if (flagValue !== undefined && flagValue !== null && typeof flagValue !== 'boolean') {
      throw new TypeError(`${owner}.${name}.${flag} must be a boolean`)
    }
  }
}

/** Validate required and optional string slots before retaining Schema metadata. */
export function checkSchemaTypes(value: object, owner: string, name: string, optionalType = false): void {
  const metadata = value as Record<string, unknown>
  const type = metadata.type
  if ((!optionalType || (type !== undefined && type !== null)) && typeof type !== 'string') {
    throw new TypeError(`${owner}.${name}.type must be a string`)
  }
  const typeName = metadata.typeName
  if (typeName !== undefined && typeName !== null && typeof typeName !== 'string') {
    throw new TypeError(`${owner}.${name}.typeName must be a string`)
  }
}

/** Validate wire IDs and member metadata before generation; never allocate by an input ID. */
export function validateSchemaIDs(schema: LuxoSchema): void {
  for (const model of Object.values(schema.models)) {
    validateIDs(model.fields, MAX_SELECTION_FIELD_ID, `model ${model.name}`, 'field')
  }
  for (const type of Object.values(schema.types ?? {})) {
    validateIDs(type.fields, MAX_SELECTION_FIELD_ID, `type ${type.name}`, 'field')
  }
  for (const owner of Object.values(schema.sealeds ?? {})) {
    const tags = new Set<number>()
    for (const variant of owner.variants) {
      checkSchemaID(variant.id, MAX_SELECTION_FIELD_ID, tags, `sealed ${owner.name}`, variant.name)
      validateIDs(variant.fields, MAX_SELECTION_FIELD_ID, `${owner.name}.${variant.name}`, 'param')
    }
  }
  validateIDs(Object.values(schema.apis), MAX_API_ID, 'API', 'api')
  for (const api of Object.values(schema.apis)) {
    validateIDs(api.params ?? [], MAX_PARAM_ID, `API ${api.name} parameter`, 'param')
    if (api.paginated) {
      for (const name of ['page', 'pageSize']) {
        const matches = (api.params ?? []).filter(param => param.name === name)
        if (matches.length !== 1 || matches[0].type !== 'Int' || matches[0].nullable || matches[0].isList) {
          throw new RangeError(`API ${api.name} pagination requires a numbered non-null Int parameter ${name}`)
        }
      }
    }
  }
}

function validateIDs(entries: readonly { id: number; name: string }[], maximum: number, owner: string, kind: keyof typeof schemaFlags): void {
  const seen = new Set<number>()
  for (const entry of entries) {
    if (kind !== 'api') checkSchemaTypes(entry, owner, entry.name)
    checkSchemaFlags(entry, kind, owner, entry.name)
    checkSchemaID(entry.id, maximum, seen, owner, entry.name)
  }
}

/** Shared by formal generation and runtime registration; diagnostics are built only on failure. */
export function checkSchemaID(id: number, maximum: number, seen: Set<number>, owner: string, name: string): void {
  if (typeof name !== 'string') throw new TypeError(`${owner}: name must be a string`)
  if (!Number.isInteger(id) || id < 1 || id > maximum) {
    throw new RangeError(`${owner}.${name}: ID must be an integer from 1 to ${maximum}`)
  }
  if (seen.has(id)) throw new RangeError(`${owner}.${name}: duplicate ID ${id}`)
  seen.add(id)
}
