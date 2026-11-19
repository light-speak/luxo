import { assertJSONUnicode } from './unicode'
import type { APISchema, ParamSchema, SelectionFieldSchema, JSONResponseSchema } from './transport'
import { UINT64_MAX } from './uint64'
import type { UInt64 } from './uint64'

type Types = NonNullable<APISchema['types']>
interface KeyPlan { readonly name: string; readonly value: JSONResourcePlan; readonly next?: KeyPlan }

/** Resource-only metadata; it never converts or constructs a business value. */
export interface JSONResourcePlan {
  readonly fields?: ReadonlyMap<number, KeyPlan>
  readonly element?: JSONResourcePlan
  readonly model?: boolean
  readonly missingType?: string
  readonly opaque?: boolean
  readonly sealed?: boolean
  readonly strict?: boolean
}

export const genericJSONResource: JSONResourcePlan = Object.freeze({})
export const opaqueJSONResource: JSONResourcePlan = Object.freeze({ opaque: true })
export class JSONResourceError extends RangeError {}
const stringSpecial = /["\\\x00-\x1f]/g
const escapes: Readonly<Record<number, number>> = { 34: 34, 47: 47, 92: 92, 98: 8, 102: 12, 110: 10, 114: 13, 116: 9 }
const hashStep = (hash: number, code: number): number => Math.imul(hash ^ code, 16777619)

function setSyntaxState(stack: number[], depth: number, state: number): void {
  const index = Math.floor(depth / 10), shift = (depth % 10) * 3
  stack[index] = ((stack[index] ?? 0) & ~(7 << shift)) | (state << shift)
}

function syntaxState(stack: number[], depth: number): number {
  return (stack[Math.floor(depth / 10)] >>> ((depth % 10) * 3)) & 7
}

function nameHash(name: string): number {
  let hash = 2166136261
  for (let i = 0; i < name.length; i++) hash = hashStep(hash, name.charCodeAt(i))
  return hash & 0x3fffffff
}

/** Compile recursive declarations once; no schema objects are read during validation. */
export class JSONResourceCompiler {
  private readonly models = new Map<string, JSONResourcePlan>()

  constructor(private readonly types: Types = {}) {}

  parameters(params: readonly ParamSchema[]): JSONResourcePlan {
    const fields = new Map<number, KeyPlan>()
    for (const field of params) this.add(fields, field.name, this.value(field))
    return { fields }
  }

  response(response: JSONResponseSchema): JSONResourcePlan {
    const value = this.value(response)
    if (!response.paginated) return value
    const fields = new Map<number, KeyPlan>()
    this.add(fields, 'items', { element: this.value({ ...response, isList: false }) })
    return { fields }
  }

  model(name = '<unknown>', strict = false): JSONResourcePlan {
    const key = strict ? `@payload:${name}` : name
    const cached = this.models.get(key)
    if (cached) return cached
    const fields = new Map<number, KeyPlan>()
    const schema = Object.hasOwn(this.types, name) ? this.types[name] : undefined
    const plan = strict ? { fields, model: true, strict: true, missingType: schema ? undefined : name }
      : { fields, model: true, missingType: schema ? undefined : name }
    this.models.set(key, plan)
    if (schema) for (const [key, field] of Object.entries(schema)) this.add(fields, key, this.value(field))
    return plan
  }

  value(field: Omit<SelectionFieldSchema, 'fieldID'>): JSONResourcePlan {
    const value = field.type === 'Sealed' ? this.sealed(field.typeName)
      : field.type === 'Model' ? this.model(field.typeName)
      : field.type === undefined ? opaqueJSONResource : genericJSONResource
    return field.isList ? { element: value } : value
  }

  private sealed(name = '<unknown>'): JSONResourcePlan {
    const key = `@sealed:${name}`
    const cached = this.models.get(key)
    if (cached) return cached
    const fields = new Map<number, KeyPlan>()
    const schema = Object.hasOwn(this.types, key) ? this.types[key] : undefined
    const plan = { fields, model: true, sealed: true, missingType: schema ? undefined : name }
    this.models.set(key, plan)
    if (schema) for (const [key, field] of Object.entries(schema)) this.add(fields, key, this.model(field.typeName, true))
    return plan
  }

  private add(fields: Map<number, KeyPlan>, name: string, value: JSONResourcePlan): void {
    const hash = nameHash(name)
    fields.set(hash, { name, value, next: fields.get(hash) })
  }
}

function hex4(text: string, offset: number): number {
  let value = 0
  for (let i = 0; i < 4; i++) {
    const code = text.charCodeAt(offset + i)
    const digit = code >= 48 && code <= 57 ? code - 48
      : (code | 32) >= 97 && (code | 32) <= 102 ? (code | 32) - 87 : -1
    if (digit < 0) throw new SyntaxError('invalid JSON Unicode escape')
    value = (value << 4) | digit
  }
  return value
}

function keyEquals(text: string, start: number, end: number, name: string): boolean {
  if (end - start === name.length + 2 && text.startsWith(name, start + 1)) return true
  let key = 0
  for (let i = start + 1; i < end - 1; i++) {
    let code = text.charCodeAt(i)
    if (code === 92) {
      const escaped = text.charCodeAt(++i)
      code = escaped === 117 ? hex4(text, i + 1) : escapes[escaped]
      if (escaped === 117) i += 4
    }
    if (name.charCodeAt(key++) !== code) return false
  }
  return key === name.length
}

/** Check serialized text before native parsing can discard duplicate values. */
export function assertJSONResourceSource(text: string, plan: JSONResourcePlan = genericJSONResource, unicodeValidated = false): void {
  if (!unicodeValidated) assertJSONUnicode(text)
  const reader = new ResourceReader(text)
  reader.value(plan, 0, 0)
  reader.end()
}

class ResourceReader {
  protected offset = 0
  private failure?: RangeError
  private syntaxStack?: number[]

  constructor(protected readonly text: string) {}

  protected validateRange(start: number, end: number, plan: JSONResourcePlan): void {
    this.offset = start
    this.failure = undefined
    this.value(plan, 0, 0)
    if (this.offset !== end) this.invalid()
    if (this.failure) throw this.failure
  }

  end(): void {
    this.space()
    if (this.offset !== this.text.length) this.invalid()
    if (this.failure) throw this.failure
  }

  value(plan: JSONResourcePlan, modelDepth: number, jsonDepth: number): void {
    this.space()
    if (plan.opaque || this.failure) { this.opaque(); return }
    switch (this.text.charCodeAt(this.offset)) {
      case 123: this.object(plan, modelDepth, jsonDepth); return
      case 91: this.array(plan, modelDepth, jsonDepth); return
      case 34: this.string(false); return
      case 116: this.literal('true'); return
      case 102: this.literal('false'); return
      case 110: this.literal('null'); return
      default: this.number()
    }
  }

  private object(plan: JSONResourcePlan, modelDepth: number, jsonDepth: number): void {
    if (plan.sealed || plan.strict) { this.checkedObject(plan, modelDepth); return }
    const fields = plan.fields
    const depth = modelDepth + (fields && plan.model ? 1 : 0)
    if (depth > 64) {
      this.failure = new JSONResourceError('model nesting exceeds 64 levels')
      this.opaque()
      return
    }
    if (fields && plan.missingType !== undefined) throw new TypeError(`missing schema for structured type ${plan.missingType}`)
    const nestedJSON = fields ? 0 : this.jsonLayer(jsonDepth)
    if (this.failure) { this.opaque(); return }
    this.offset++
    if (this.take(125)) return
    for (;;) {
      this.space()
      const start = this.offset
      const hash = this.string(true), end = this.offset
      let field = fields?.get(hash)
      while (field && !keyEquals(this.text, start, end, field.name)) field = field.next
      this.require(58)
      this.value(fields ? field?.value ?? opaqueJSONResource : genericJSONResource, depth, nestedJSON)
      if (this.take(125)) return
      this.require(44)
    }
  }

  private checkedObject(plan: JSONResourcePlan, modelDepth: number): void {
    if (modelDepth + 1 > 64) {
      this.failure = new JSONResourceError('model nesting exceeds 64 levels')
      this.opaque()
      return
    }
    if (plan.missingType !== undefined) throw new TypeError(`missing schema for structured type ${plan.missingType}`)
    this.offset++
    if (this.take(125)) {
      if (plan.sealed) throw new TypeError('sealed value requires exactly one case')
      return
    }
    let count = 0
    for (;;) {
      this.space()
      const start = this.offset
      const hash = this.string(true), end = this.offset
      let field = plan.fields?.get(hash)
      while (field && !keyEquals(this.text, start, end, field.name)) field = field.next
      if (!field) throw new TypeError('unknown sealed case or payload field')
      if (plan.sealed && ++count !== 1) throw new TypeError('sealed value requires exactly one case')
      this.require(58)
      if (plan.sealed) {
        this.space()
        if (this.text.charCodeAt(this.offset) !== 123) throw new TypeError('sealed payload must be an object')
      }
      this.value(field.value, modelDepth + 1, 0)
      if (this.take(125)) return
      this.require(44)
    }
  }

  private array(plan: JSONResourcePlan, modelDepth: number, jsonDepth: number): void {
    const element = plan.element
    const nestedJSON = element ? 0 : this.jsonLayer(jsonDepth)
    if (this.failure) { this.opaque(); return }
    this.offset++
    if (this.take(93)) return
    for (;;) {
      this.value(element ?? genericJSONResource, modelDepth, nestedJSON)
      if (this.take(93)) return
      this.require(44)
    }
  }

  private jsonLayer(depth: number): number {
    if (depth >= 128) this.failure = new JSONResourceError('JSON nesting exceeds 128 levels')
    return depth + 1
  }

  protected string(hashKey: boolean): number {
    if (this.text.charCodeAt(this.offset++) !== 34) this.invalid()
    let hash = 2166136261
    for (;;) {
      stringSpecial.lastIndex = this.offset
      if (!stringSpecial.test(this.text)) this.invalid()
      const end = stringSpecial.lastIndex - 1
      if (hashKey) for (let i = this.offset; i < end; i++) hash = hashStep(hash, this.text.charCodeAt(i))
      const code = this.text.charCodeAt(end)
      this.offset = end + 1
      if (code === 34) return hash & 0x3fffffff
      if (code !== 92) this.invalid()
      const escaped = this.text.charCodeAt(this.offset++)
      let value = escapes[escaped]
      if (escaped === 117) { value = hex4(this.text, this.offset); this.offset += 4 }
      else if (value === undefined) this.invalid()
      if (hashKey) hash = hashStep(hash, value)
    }
  }

  private number(): void {
    if (this.text.charCodeAt(this.offset) === 45) this.offset++
    if (this.text.charCodeAt(this.offset) === 48) this.offset++
    else { if (!this.digit()) this.invalid(); while (this.digit()) this.offset++ }
    if (this.text.charCodeAt(this.offset) === 46) { this.offset++; this.digits() }
    const code = this.text.charCodeAt(this.offset) | 32
    if (code !== 101) return
    this.offset++
    const sign = this.text.charCodeAt(this.offset)
    if (sign === 43 || sign === 45) this.offset++
    this.digits()
  }

  private digit(): boolean {
    const code = this.text.charCodeAt(this.offset)
    return code >= 48 && code <= 57
  }

  private digits(): void {
    if (!this.digit()) this.invalid()
    while (this.digit()) this.offset++
  }

  private literal(value: string): void {
    if (!this.text.startsWith(value, this.offset)) this.invalid()
    this.offset += value.length
  }

  protected space(): void {
    for (;;) {
      const code = this.text.charCodeAt(this.offset)
      if (code !== 32 && code !== 9 && code !== 10 && code !== 13) return
      this.offset++
    }
  }

  protected take(code: number): boolean {
    this.space()
    if (this.text.charCodeAt(this.offset) !== code) return false
    this.offset++
    return true
  }

  protected require(code: number): void {
    if (!this.take(code)) this.invalid()
  }

  // Undeclared paths have no known DSL resource boundary. Validate their syntax
  // iteratively; ten three-bit frames share one small integer, never a value tree.
  protected opaque(): void {
    let depth = 0, value = true
    for (;;) {
      this.space()
      if (value) {
        const code = this.text.charCodeAt(this.offset)
        if (code === 123 || code === 91) {
          setSyntaxState(this.syntaxStack ??= [], depth++, code === 123 ? 0 : 3)
          this.offset++
        } else { this.scalar(); if (depth === 0) return }
        value = false
        continue
      }
      const stack = this.syntaxStack!, state = syntaxState(stack, depth - 1)
      const object = state < 3, close = object ? 125 : 93
      if ((state === 0 || state === 1 || state === 3 || state === 4) && this.take(close)) {
        if (--depth === 0) return
        continue
      }
      if (state === 1 || state === 4) {
        this.require(44)
        setSyntaxState(stack, depth - 1, object ? 2 : 5)
        continue
      }
      if (object) { this.string(false); this.require(58) }
      setSyntaxState(stack, depth - 1, object ? 1 : 4)
      value = true
    }
  }

  private scalar(): void {
    switch (this.text.charCodeAt(this.offset)) {
      case 34: this.string(false); return
      case 116: this.literal('true'); return
      case 102: this.literal('false'); return
      case 110: this.literal('null'); return
      default: this.number()
    }
  }

  protected invalid(): never { throw new SyntaxError('invalid JSON source') }
}

type RouteKind = '$id' | '$stream' | '$sub' | '$end'
const routePriority: Readonly<Record<string, number>> = { $id: 1, $stream: 2, $sub: 3, $end: 4 }
const envelopeKeys = new Map(['data', 'error', '$id', '$stream', '$sub', '$end'].map(name => [nameHash(name), name]))

/** Syntax and routing first; all data occurrences remain source ranges, never value trees. */
export class JSONEnvelopeSource extends ResourceReader {
  kind?: RouteKind
  id?: UInt64
  error = false
  private dataStart = -1
  private dataEnd = 0
  private moreData?: number[]

  constructor(text: string) {
    super(text)
    assertJSONUnicode(text)
    this.envelope()
    this.end()
  }

  validateData(plan: JSONResourcePlan, errorData = this.error): void {
    if (this.dataStart < 0) return
    const resource = errorData ? genericJSONResource : plan
    if (resource.opaque) return
    this.validateRange(this.dataStart, this.dataEnd, resource)
    if (this.moreData) for (let i = 0; i < this.moreData.length; i += 2) {
      this.validateRange(this.moreData[i], this.moreData[i + 1], resource)
    }
  }

  private envelope(): void {
    this.require(123)
    if (this.take(125)) return
    let priority = 0, routes = 0, routeStart = 0, routeEnd = 0
    for (;;) {
      this.space()
      const keyStart = this.offset, hash = this.string(true), keyEnd = this.offset
      const name = envelopeKeys.get(hash)
      const key = name && keyEquals(this.text, keyStart, keyEnd, name) ? name : undefined
      this.require(58)
      this.space()
      const start = this.offset
      this.opaque()
      const end = this.offset
      if (key === 'data') this.addData(start, end)
      else if (key === 'error') this.error = true
      else if (key) {
        const rank = routePriority[key]
        routes |= 1 << rank
        if (rank >= priority) { priority = rank; this.kind = key as RouteKind; routeStart = start; routeEnd = end }
      }
      if (this.take(125)) break
      this.require(44)
    }
    if (this.kind === '$end' && routes !== 16) this.kind = undefined
    if (this.kind) this.id = sourceUint64(this.text, routeStart, routeEnd)
  }

  private addData(start: number, end: number): void {
    if (this.dataStart < 0) { this.dataStart = start; this.dataEnd = end }
    else (this.moreData ??= []).push(start, end)
  }
}

function sourceUint64(text: string, start: number, end: number): UInt64 | undefined {
  const length = end - start
  if (length > 20) return undefined
  let value = 0
  for (let i = start; i < end; i++) {
    const digit = text.charCodeAt(i) - 48
    if (digit < 0 || digit > 9) return undefined
    value = value * 10 + digit
  }
  if (Number.isSafeInteger(value)) return value
  const wide = BigInt(text.slice(start, end))
  return wide <= UINT64_MAX ? wide : undefined
}
