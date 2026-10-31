import { Encoder, fieldMaskSet } from './codec'
import { LuxoError } from './error'
import type { APISchema, SelectionFieldSchema } from './transport'
import { isSelectionSnapshot } from './schema-runtime'

interface SelectedField {
  name: string
  children?: SelectedField[]
}

class SelectionParser {
  private offset = 0

  constructor(private readonly input: string) {}

  parse(): SelectedField[] {
    const fields = this.parseList(false, 0)
    this.skipSpaces()
    if (this.offset !== this.input.length) this.fail(`unexpected '${this.input[this.offset]}'`)
    return fields
  }

  private parseList(nested: boolean, depth: number): SelectedField[] {
    if (depth >= 32) this.fail('selection depth exceeds 32')
    const fields: SelectedField[] = []
    const names = new Set<string>()
    while (true) {
      this.skipSpaces()
      if (this.offset >= this.input.length || (nested && this.input[this.offset] === '}')) {
        if (fields.length > 0) this.fail('expected field after comma')
        break
      }
      const name = this.readIdentifier()
      if (!name) this.fail('expected field name')
      if (names.has(name)) this.fail(`duplicate field '${name}'`)
      names.add(name)
      this.skipSpaces()
      let children: SelectedField[] | undefined
      if (this.input[this.offset] === '{') {
        this.offset++
        children = this.parseList(true, depth + 1)
        if (children.length === 0) this.fail(`empty selection for '${name}'`)
        this.skipSpaces()
        if (this.input[this.offset] !== '}') this.fail(`missing '}' for '${name}'`)
        this.offset++
      }
      fields.push(children ? { name, children } : { name })
      this.skipSpaces()
      if (this.input[this.offset] !== ',') break
      this.offset++
    }
    return fields
  }

  private readIdentifier(): string {
    const start = this.offset
    const first = this.input.charCodeAt(this.offset)
    if (!isIdentifierStart(first)) return ''
    this.offset++
    while (isIdentifierPart(this.input.charCodeAt(this.offset))) this.offset++
    return this.input.slice(start, this.offset)
  }

  private skipSpaces(): void {
    while (/\s/.test(this.input[this.offset] ?? '')) this.offset++
  }

  private fail(message: string): never {
    throw new LuxoError('ConfigError', 0, `${message} at position ${this.offset}`)
  }
}

function isIdentifierStart(code: number): boolean {
  return code === 95 || (code >= 65 && code <= 90) || (code >= 97 && code <= 122)
}

function isIdentifierPart(code: number): boolean {
  return isIdentifierStart(code) || (code >= 48 && code <= 57)
}

function encodeSelectionNode(
  selected: SelectedField[],
  fields: Record<string, SelectionFieldSchema>,
  types: Record<string, Record<string, SelectionFieldSchema>>,
): Uint8Array {
  let mask: Uint8Array<ArrayBufferLike> = new Uint8Array(0)
  const children: Array<{ fieldID: number; data: Uint8Array }> = []
  for (const field of selected) {
    const meta = fields[field.name]
    if (!meta) throw new LuxoError('ConfigError', 0, `unknown selected field: ${field.name}`)
    mask = fieldMaskSet(mask, meta.fieldID)
    if (!field.children) continue
    const nestedFields = meta.typeName ? types[meta.typeName] : undefined
    if (!nestedFields) throw new LuxoError('ConfigError', 0, `field ${field.name} does not support nested selection`)
    children.push({ fieldID: meta.fieldID, data: encodeSelectionNode(field.children, nestedFields, types) })
  }
  const node = new Encoder()
  node.writeVarint(mask.length)
  node.writeRawBytes(mask)
  children.sort((a, b) => a.fieldID - b.fieldID)
  for (const child of children) {
    node.writeVarint(child.fieldID)
    node.writeVarint(child.data.length)
    node.writeRawBytes(child.data)
  }
  return node.bytes()
}

/** Immutable call-site selection. No global string cache or mutable byte exposure. */
export class PreparedSelection {
  readonly #selected: SelectedField[]
  readonly #masks = new WeakMap<APISchema, Uint8Array>()
  constructor(readonly text: string) {
    if (typeof text !== 'string' || !text.trim()) throw new LuxoError('ConfigError', 0, '$select is required for structured responses')
    this.#selected = new SelectionParser(text).parse()
    Object.freeze(this)
  }
  write(encoder: Encoder, schema: APISchema): void {
    const reusable = isSelectionSnapshot(schema)
    let mask = reusable ? this.#masks.get(schema) : undefined
    if (!mask) {
      mask = encodeSelectionNode(this.#selected, schema.fields ?? {}, schema.types ?? {})
      if (reusable) this.#masks.set(schema, mask)
    }
    encoder.writeVarint(mask.length)
    encoder.writeRawBytes(mask)
  }
}

export function prepareSelection(text: string): PreparedSelection {
  return new PreparedSelection(text)
}

export function writeSelectionMask(enc: Encoder, meta: APISchema, select: unknown): void {
	if (!meta.fields || Object.keys(meta.fields).length === 0) {
    enc.writeVarint(0)
    return
  }
	if (typeof select !== 'string' || select.trim() === '') {
    if (select instanceof PreparedSelection) { select.write(enc, meta); return }
		throw new LuxoError('ConfigError', 0, '$select is required for structured responses')
	}
  const selected = new SelectionParser(select).parse()
  const mask = encodeSelectionNode(selected, meta.fields, meta.types ?? {})
  enc.writeVarint(mask.length)
  enc.writeRawBytes(mask)
}

