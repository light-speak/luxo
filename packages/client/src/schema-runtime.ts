import type { APISchema, ParamSchema, SelectionFieldSchema } from './transport'
import { checkSchemaID, MAX_API_ID, MAX_PARAM_ID, MAX_SELECTION_FIELD_ID } from './schema'

type Fields = Record<string, SelectionFieldSchema>
type Types = Record<string, Fields>

/** Own a validated snapshot without losing shared type-directory identity. Registration only. */
export function snapshotAPISchema(schema: Record<string, APISchema>): Record<string, APISchema> {
  const seen = new Set<number>()
  const snapshots = new SchemaSnapshots()
  return Object.fromEntries(Object.entries(schema).map(([name, api]) => {
    const id = api.id
    checkSchemaID(id, MAX_API_ID, seen, 'API', name)
    return [name, {
      id,
      params: api.params && snapshotParams(api.params, name),
      fields: api.fields && snapshots.fields(api.fields, `API ${name}`),
      types: api.types && snapshots.types(api.types),
    }]
  }))
}

function snapshotParams(params: readonly ParamSchema[], api: string): ParamSchema[] {
  const seen = new Set<number>()
  const owner = `API ${api} parameter`
  return params.map(param => {
    const copy = { name: param.name, type: param.type, fieldID: param.fieldID,
      typeName: param.typeName, isList: param.isList, nullable: param.nullable }
    checkSchemaID(copy.fieldID, MAX_PARAM_ID, seen, owner, copy.name)
    return copy
  })
}

function snapshotField(field: SelectionFieldSchema): SelectionFieldSchema {
  return { fieldID: field.fieldID, type: field.type, typeName: field.typeName,
    isList: field.isList, nullable: field.nullable }
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
      checkSchemaID(value.fieldID, MAX_SELECTION_FIELD_ID, seen, owner, name)
      return [name, value]
    }))
    this.fieldCopies.set(fields, copy)
    return copy
  }

  types(types: Types): Types {
    const cached = this.typeCopies.get(types)
    if (cached) return cached
    const copy = Object.fromEntries(Object.entries(types).map(([name, fields]) =>
      [name, this.fields(fields, `type ${name}`)]))
    this.typeCopies.set(types, copy)
    return copy
  }
}
