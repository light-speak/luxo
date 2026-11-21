import type { APISchema, SelectionFieldSchema } from './transport'
import { encodeBinaryBody } from './transport'
import { JSONParamValidator } from './json-params'
import { snapshotAPISchema } from './schema-runtime'
import { measure } from './measure.bench'

/** Reproduce the old registration only for valid, immutable benchmark inputs. */
class Registration {
  private schema: Record<string, APISchema> = {}
  private readonly parameters = new JSONParamValidator()

  setSchema(schema: Record<string, APISchema>): number {
    this.parameters.setSchema(schema)
    this.schema = schema
    return Object.keys(this.schema).length
  }

  encode(params: Record<string, unknown>): number {
    return encodeBinaryBody(this.schema.api1, this.parameters.prepare('api1', params)).length
  }
}

function fixture(apis: number, fields: number, types: number): Record<string, APISchema> {
  const selections: Record<string, SelectionFieldSchema> = Object.fromEntries(
    Array.from({ length: fields }, (_, index) => [`field${index + 1}`, { fieldID: index + 1, type: 'Int' }]))
  const sharedTypes = Object.fromEntries(Array.from({ length: types }, (_, index) => [`Type${index}`, selections]))
  return Object.fromEntries(Array.from({ length: apis }, (_, index) => [`api${index + 1}`, {
    id: index + 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }], fields: selections, types: sharedTypes,
  }]))
}

for (const [apis, fields, types] of [[1, 2, 0], [16, 16, 0], [16, 16, 16]]) {
  const schema = fixture(apis, fields, types)
  const before = new Registration(), after = new Registration()
  measure(`register ${apis} APIs x ${fields} fields + ${types} shared types`,
    () => before.setSchema(schema), () => after.setSchema(snapshotAPISchema(schema)))
  const params = { value: 7n, $select: 'field1' }
  measure(`encode control ${apis} APIs x ${fields} fields + ${types} shared types`,
    () => before.encode(params), () => after.encode(params))
}
