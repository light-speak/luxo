import { deepStrictEqual, throws } from 'node:assert/strict'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { Encoder } from '@luxojs/client'
import type { LuxoSchema } from '@luxojs/client'
import { generateTypes } from './codegen'
import { genResponseBody } from './codegen-response'

type RootDecoder = (data: unknown) => unknown
type Measure = (name: string, before: () => unknown, after: () => unknown) => void
export type RootDecoders = Record<string, RootDecoder>
type RootKind = 'integer' | 'boolean' | 'text' | 'bytes' | 'integers' | 'object' | 'objects'
interface RootCase {
  name: string
  kind: RootKind
  binary: Uint8Array
  json: unknown
  expected: unknown
}

const declarations: [RootKind, string, boolean][] = [
  ['integer', 'Int', false], ['boolean', 'Boolean', false], ['text', 'String', false],
  ['bytes', 'Bytes', false], ['integers', 'Int', true],
  ['object', 'Snapshot', false], ['objects', 'Snapshot', true],
]

/** Compile the production emitter before measuring; never evaluate source in the timed path. */
export async function generateNullableRootBenchmark(directory: string): Promise<void> {
  const schema: LuxoSchema = {
    models: {}, apis: {}, types: { Snapshot: { name: 'Snapshot', usage: 'output', fields: [
      { id: 1, name: 'id', type: 'Int' }, { id: 2, name: 'label', type: 'String' },
    ] } },
  }
  let source = "import { Decoder, int64FromJSON, booleanFromJSON, stringFromJSON, decodeBase64 } from '@luxojs/client'\n"
  source += "import { decodeSnapshot, decodeColumnarSnapshot, decodeJSONSnapshot, decodeJSONSnapshot$list } from './types'\n"
  for (const [name, returnType, returnList] of declarations) {
    for (const returnNullable of [false, true]) {
      const functionName = `${name}${returnNullable ? 'Nullable' : 'Plain'}`
      const api = { id: Object.keys(schema.apis).length + 1, name: functionName,
        module: 'benchmark', returnType, returnList, returnNullable }
      schema.apis[functionName] = api
      source += `export function ${functionName}(d: unknown): unknown {\n`
      source += genResponseBody(api, schema, 'unknown', '  ') + '}\n'
    }
  }
  await generateTypes(schema, directory)
  writeFileSync(join(directory, 'roots.ts'), source)
}

function encoded(write: (encoder: Encoder) => void): Uint8Array {
  const encoder = new Encoder()
  write(encoder)
  return encoder.bytes()
}

function envelope(body: Uint8Array): Uint8Array {
  const result = new Uint8Array(body.length + 1)
  result[0] = 1
  result.set(body, 1)
  return result
}

function scalarCases(): RootCase[] {
  const cases: RootCase[] = [0n, 42n, (1n << 63n) - 1n].map(value => ({
    name: `integer-${value}`, kind: 'integer', binary: encoded(e => e.writeSvarint(value)),
    json: value, expected: value,
  }))
  cases.push({ name: 'false', kind: 'boolean', binary: new Uint8Array([0]), json: false, expected: false })
  for (const text of ['', '发布冻结'.repeat(350)]) {
    cases.push({ name: `text-${new TextEncoder().encode(text).length}`, kind: 'text',
      binary: encoded(e => e.writeString(text)), json: text, expected: text })
  }
  const bytes = new Uint8Array(4096).fill(7)
  cases.push({ name: 'bytes-4096', kind: 'bytes', binary: encoded(e => e.writeBytes(bytes)),
    json: Buffer.from(bytes).toString('base64'), expected: bytes })
  for (const count of [0, 128]) {
    const values = Array.from({ length: count }, (_, index) => BigInt(index))
    cases.push({ name: `integers-${count}`, kind: 'integers', json: values, expected: values,
      binary: encoded(e => { e.writeVarint(count); for (const value of values) e.writeSvarint(value) }) })
  }
  return cases
}

function modelCases(): RootCase[] {
  const object = { id: 42n, label: '发布' }
  const objects = Array.from({ length: 128 }, (_, index) => ({ id: BigInt(index), label: '发布' }))
  const row = encoded(e => {
    e.writeVarint(6)
    e.writeFieldInt(1, object.id)
    e.writeFieldString(2, object.label)
    e.writeEnd()
  })
  const columns = encoded(e => {
    e.writeVarint(objects.length)
    e.writeVarint(objects.length * 6)
    e.writeVarint(1)
    for (const value of objects) e.writeSvarint(value.id)
    e.writeVarint(2)
    for (const value of objects) e.writeString(value.label)
    e.writeEnd()
  })
  return [
    { name: 'object', kind: 'object', binary: row, json: object, expected: object },
    { name: 'objects-128', kind: 'objects', binary: columns, json: objects, expected: objects },
  ]
}

/** Check every timed fixture, including nonzero byte offsets, before warming either version. */
export function verifyNullableRoots(decoders: RootDecoders): void {
  for (const sample of [...scalarCases(), ...modelCases()]) {
    const plain = decoders[`${sample.kind}Plain`], nullable = decoders[`${sample.kind}Nullable`]
    const bytes = envelope(sample.binary)
    const original = bytes.slice()
    const padded = new Uint8Array(bytes.length + 13).fill(255)
    padded.set(bytes, 7)
    for (const input of [bytes, padded.subarray(7, 7 + bytes.length), sample.json]) {
      deepStrictEqual(nullable(input), sample.expected, sample.name)
    }
    deepStrictEqual(plain(sample.binary), sample.expected, sample.name)
    deepStrictEqual(plain(sample.json), sample.expected, sample.name)
    deepStrictEqual(bytes, original, 'decoding must not change the envelope')
    deepStrictEqual(nullable(null), null)
    deepStrictEqual(nullable(new Uint8Array([0])), null)
    for (const malformed of [[], [1], [2], [0, 0]]) throws(() => nullable(new Uint8Array(malformed)))
    const trailing = new Uint8Array(bytes.length + 1)
    trailing.set(bytes)
    throws(() => nullable(trailing), `${sample.name}: trailing bytes`)
    if (sample.binary.length > 1) throws(() => nullable(bytes.subarray(0, bytes.length - 1)))
  }
}

/** The baseline is the current non-null decoder, not an older unchecked runtime. */
export function measureNullableRoots(decoders: RootDecoders, measure: Measure): void {
  verifyNullableRoots(decoders)
  for (const sample of [...scalarCases(), ...modelCases()]) {
    const plain = decoders[`${sample.kind}Plain`], nullable = decoders[`${sample.kind}Nullable`]
    const bytes = envelope(sample.binary)
    measure(`nullable binary ${sample.name}`, () => plain(sample.binary), () => nullable(bytes))
    measure(`nullable json ${sample.name}`, () => plain(sample.json), () => nullable(sample.json))
  }
  for (const [kind] of declarations) {
    const nullable = decoders[`${kind}Nullable`], bytes = new Uint8Array([0])
    // Both sides return null; report their absolute costs, not a legacy-functionality delta.
    measure(`null absolute json/binary ${kind}`, () => nullable(null), () => nullable(bytes))
  }
}
