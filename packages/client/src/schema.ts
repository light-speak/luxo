import type { LuxoSchema } from './types'

export const MAX_SELECTION_FIELD_ID = 65535
export const MAX_API_ID = 2147483647
export const MAX_PARAM_ID = MAX_API_ID - 2

/** Validate stable wire IDs before generating code; never allocate by an input ID. */
export function validateSchemaIDs(schema: LuxoSchema): void {
  for (const model of Object.values(schema.models)) {
    validateIDs(model.fields, MAX_SELECTION_FIELD_ID, `model ${model.name}`)
  }
  for (const type of Object.values(schema.types ?? {})) {
    validateIDs(type.fields, MAX_SELECTION_FIELD_ID, `type ${type.name}`)
  }
  validateIDs(Object.values(schema.apis), MAX_API_ID, 'API')
  for (const api of Object.values(schema.apis)) {
    validateIDs(api.params ?? [], MAX_PARAM_ID, `API ${api.name} parameter`)
  }
}

function validateIDs(entries: readonly { id: number; name: string }[], maximum: number, owner: string): void {
  const seen = new Set<number>()
  for (const entry of entries) {
    checkSchemaID(entry.id, maximum, seen, owner, entry.name)
  }
}

/** Shared by formal generation and runtime registration; diagnostics are built only on failure. */
export function checkSchemaID(id: number, maximum: number, seen: Set<number>, owner: string, name: string): void {
  if (!Number.isInteger(id) || id < 1 || id > maximum) {
    throw new RangeError(`${owner}.${name}: ID must be an integer from 1 to ${maximum}`)
  }
  if (seen.has(id)) throw new RangeError(`${owner}.${name}: duplicate ID ${id}`)
  seen.add(id)
}
