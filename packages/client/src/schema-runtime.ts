import type { APISchema, ParamSchema, SelectionFieldSchema, JSONResponseSchema } from './transport'
import { checkSchemaFlags, checkSchemaTypes, checkSchemaID, MAX_API_ID, MAX_PARAM_ID, MAX_SELECTION_FIELD_ID } from './schema'

const selectionSnapshots = new WeakSet<APISchema>()

export function isSelectionSnapshot(schema: APISchema): boolean {
  return selectionSnapshots.has(schema)
}

type Fields = Record<string, SelectionFieldSchema>
type Types = Record<string, Fields>

/** Own a validated snapshot without losing shared type-directory identity. Registration only. */
export function snapshotAPISchema(schema: Record<string, APISchema>): Record<string, APISchema> {
  const seen = new Set<number>()
  const snapshots = new SchemaSnapshots()
  return Object.fromEntries(Object.entries(schema).map(([name, api]) => {
    const id = api.id
    const response = api.response
    checkSchemaID(id, MAX_API_ID, seen, 'API', name)
    const snapshot = Object.freeze({
      id,
      params: api.params && snapshotParams(api.params, name),
      fields: api.fields && snapshots.fields(api.fields, `API ${name}`),
      types: api.types && snapshots.types(api.types),
      response: response && snapshotResponse(response, name),
    })
    selectionSnapshots.add(snapshot)
    return [name, snapshot]
  }))
}

function snapshotResponse(response: JSONResponseSchema, api: string): JSONResponseSchema {
  const copy = { type: response.type, typeName: response.typeName, isList: response.isList, paginated: response.paginated }
  checkSchemaTypes(copy, 'API response', api)
  checkSchemaFlags(copy, 'response', 'API response', api)
  return Object.freeze(copy)
}

function snapshotParams(params: readonly ParamSchema[], api: string): ParamSchema[] {
  const seen = new Set<number>()
  const names = params.length > 1 ? new Set<string>() : undefined
  const owner = `API ${api} parameter`
  return params.map((param, index) => {
    const copy = { name: param.name, type: param.type, fieldID: param.fieldID,
      typeName: param.typeName, isList: param.isList, nullable: param.nullable }
    checkSchemaTypes(copy, owner, copy.name)
    checkSchemaFlags(copy, 'param', owner, copy.name)
    checkSchemaID(copy.fieldID, MAX_PARAM_ID, seen, owner, copy.name)
    if (names && names.add(copy.name).size !== index + 1) {
      throw new RangeError(`${owner}: duplicate name ${copy.name}`)
    }
    return copy
  })
}

function snapshotField(field: SelectionFieldSchema): SelectionFieldSchema {
  return Object.freeze({ fieldID: field.fieldID, type: field.type, typeName: field.typeName,
    isList: field.isList, nullable: field.nullable })
}

class SchemaSnapshots {
  private readonly fieldCopies = new Map<Fields, Fields>()
  private readonly typeCopies = new Map<Types, Types>()

  fields(fields: Fields, owner: string): Fields {
    const cached = this.fieldCopies.get(fields)
    if (cached) return cached
    const seen = new Set<number>()
    const copy = Object.fromEntries(Object.entries(fields).map(([name, field]) => {
      const value = snapshotField(field)
      checkSchemaTypes(value, owner, name, true)
      checkSchemaFlags(value, 'field', owner, name)
      checkSchemaID(value.fieldID, MAX_SELECTION_FIELD_ID, seen, owner, name)
      return [name, value]
    }))
    Object.freeze(copy)
    this.fieldCopies.set(fields, copy)
    return copy
  }

  types(types: Types): Types {
    const cached = this.typeCopies.get(types)
    if (cached) return cached
    const copy = Object.fromEntries(Object.entries(types).map(([name, fields]) =>
      [name, this.fields(fields, `type ${name}`)]))
    Object.freeze(copy)
    this.typeCopies.set(types, copy)
    return copy
  }
}
