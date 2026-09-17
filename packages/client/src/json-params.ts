import { assertInt64 } from './int64'
import { dateTimeFromJSON } from './datetime'
import type { APISchema, SelectionFieldSchema } from './transport'

type TypeSchema = NonNullable<APISchema['types']>
type FieldSchema = SelectionFieldSchema & { name: string }

interface ObjectPlan {
  fields: FieldPlan[]
  missingType?: string
}

interface FieldPlan {
  name: string
  isList: boolean
  nullable: boolean
  dateTime?: boolean
  object?: ObjectPlan
}

interface ParameterPlan {
  checks: FieldPlan[]
  bytes?: FieldPlan[]
}

const emptyTypes: TypeSchema = Object.freeze({})
const checkedScalarTypes = new Set(['Int', 'Duration', 'DateTime'])
const byteTypes = new Set(['Bytes'])
const nativeBytes = Uint8Array.prototype as Uint8Array & { toBase64(): string }

/** Compile scalar checks and byte projections once, outside the request path. */
export class JSONParamValidator {
  private plans = new Map<string, ParameterPlan>()

  setSchema(schema: Record<string, APISchema>): void {
    const plans = new Map<string, ParameterPlan>()
    const compilers = new Map<TypeSchema, { checks: FieldPlanCompiler; bytes: FieldPlanCompiler }>()
    for (const [api, meta] of Object.entries(schema)) {
      if (!meta.params?.length) continue
      const types = meta.types ?? emptyTypes
      let compiler = compilers.get(types)
      if (!compiler) {
        compiler = { checks: new FieldPlanCompiler(types, checkedScalarTypes), bytes: new FieldPlanCompiler(types, byteTypes) }
        compilers.set(types, compiler)
      }
      const checks = compiler.checks.fields(meta.params)
      const bytes = compiler.bytes.fields(meta.params)
      const hasBytes = hasScalarLeaf(bytes, new Set())
      if (checks.length || hasBytes) plans.set(api, { checks, bytes: hasBytes ? bytes : undefined })
    }
    this.plans = plans
  }

  validate(api: string, params?: Record<string, unknown>): void {
    if (!params) return
    const plan = this.plans.get(api)
    if (plan) validateObject(plan.checks, params, 0)
  }

  prepare(api: string, params?: Record<string, unknown>): Record<string, unknown> | undefined {
    if (!params) return params
    const plan = this.plans.get(api)
    if (!plan) return params
    validateObject(plan.checks, params, 0)
    return plan.bytes ? projectBytes(plan.bytes, params, 0) : params
  }
}

class FieldPlanCompiler {
  private objects = new Map<string, ObjectPlan>()

  constructor(private readonly types: TypeSchema, private readonly scalars: ReadonlySet<string>) {}

  fields(schema: readonly FieldSchema[]): FieldPlan[] {
    const fields: FieldPlan[] = []
    for (const field of schema) {
      if (field.type === undefined || (!this.scalars.has(field.type) && field.type !== 'Model')) continue
      fields.push({ name: field.name, isList: field.isList ?? false, nullable: field.nullable ?? false,
        dateTime: field.type === 'DateTime',
        object: field.type === 'Model' ? this.object(field.typeName) : undefined,
      })
    }
    return fields
  }

  private object(name = '<unknown>'): ObjectPlan {
    const cached = this.objects.get(name)
    if (cached) return cached
    const plan: ObjectPlan = { fields: [] }
    this.objects.set(name, plan)
    const schema = Object.hasOwn(this.types, name) ? this.types[name] : undefined
    if (!schema) plan.missingType = name
    else plan.fields = this.fields(Object.entries(schema).map(([name, field]) => ({ ...field, name })))
    return plan
  }
}

function hasScalarLeaf(fields: readonly FieldPlan[], visited: Set<ObjectPlan>): boolean {
  for (const field of fields) {
    if (!field.object) return true
    if (visited.has(field.object)) continue
    visited.add(field.object)
    if (hasScalarLeaf(field.object.fields, visited)) return true
  }
  return false
}

function projectBytes(fields: readonly FieldPlan[], value: Record<string, unknown>, depth: number): Record<string, unknown> {
  if (depth > 128) throw new RangeError('JSON parameter nesting exceeds 128 levels')
  let result = value
  for (const field of fields) {
    const item = value[field.name]
    if (item === undefined || (item === null && field.nullable)) continue
    const projected = field.isList ? projectByteList(field, item, depth) : projectByteValue(field, item, depth)
    if (projected === item) continue
    if (result === value) result = { ...value }
    // Define an own data property even for protocol names such as __proto__.
    Object.defineProperty(result, field.name, { value: projected, enumerable: true, writable: true, configurable: true })
  }
  return result
}

function projectByteList(field: FieldPlan, value: unknown, depth: number): unknown[] {
  if (!Array.isArray(value)) throw new TypeError(`${field.name} must be an array`)
  let result: unknown[] = value
  for (let index = 0; index < value.length; index++) {
    const projected = projectByteValue(field, value[index], depth)
    if (projected === value[index]) continue
    if (result === value) result = value.slice()
    result[index] = projected
  }
  return result
}

function projectByteValue(field: FieldPlan, value: unknown, depth: number): unknown {
  if (!field.object) {
    // The native brand check also accepts Uint8Array values from another realm.
    const encode = nativeBytes.toBase64
    if (typeof encode !== 'function') throw new TypeError('JSON Bytes require native Uint8Array.toBase64 support')
    return encode.call(value as Uint8Array)
  }
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${field.name} must be an object`)
  }
  return projectBytes(field.object.fields, value as Record<string, unknown>, depth + 1)
}

function validateObject(fields: readonly FieldPlan[], value: Record<string, unknown>, depth: number): void {
  if (depth > 128) throw new RangeError('JSON parameter nesting exceeds 128 levels')
  for (const field of fields) {
    const item = value[field.name]
    if (item === undefined || (item === null && field.nullable)) continue
    if (!field.isList) validateValue(field, item, depth)
    else {
      if (!Array.isArray(item)) throw new TypeError(`${field.name} must be an array`)
      for (const element of item) validateValue(field, element, depth)
    }
  }
}

function validateValue(field: FieldPlan, value: unknown, depth: number): void {
  if (!field.object) {
    if (field.dateTime) dateTimeFromJSON(value)
    else assertInt64(value as bigint)
    return
  }
  if (field.object.missingType !== undefined) throw new TypeError(`missing schema for structured type ${field.object.missingType}`)
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${field.name} must be an object`)
  }
  validateObject(field.object.fields, value as Record<string, unknown>, depth + 1)
}
