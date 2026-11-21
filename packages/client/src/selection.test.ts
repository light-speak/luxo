import { describe, expect, it, vi } from 'vitest'
import { prepareSelection } from './selection'
import { encodeStaticRequestBody } from './transport'
import { snapshotAPISchema } from './schema-runtime'
import { requestJSON } from './request'

const codec = { apiID: 1, initialCapacity: () => 64, writeParameters: () => {} }
const schema = (id: number) => snapshotAPISchema({ api: {
  id: 1, fields: { user: { fieldID: 1, typeName: 'User' } },
  types: { User: { id: { fieldID: id }, name: { fieldID: 2 } } },
} }).api!

describe('prepared selections', () => {
  it('keeps ASCII identifier scanning outside Unicode code-point decoding', () => {
    const expected = encodeStaticRequestBody(schema(1), codec, { $select: 'user{id,name}' })
    const meta = { id: 1, fields: { _user1: { fieldID: 1, typeName: 'Nested' } },
      types: { Nested: { id2: { fieldID: 1 }, _name3: { fieldID: 2 } } } }
    const codePoints = vi.spyOn(String.prototype, 'codePointAt')
    let plan: ReturnType<typeof prepareSelection>
    let reads: number
    try {
      plan = prepareSelection('_user1{id2,_name3}')
      reads = codePoints.mock.calls.length
    } finally { codePoints.mockRestore() }
    expect(reads).toBe(0)
    expect(encodeStaticRequestBody(meta, codec, { $select: plan })).toEqual(expected)
  })

  it('transitions between ASCII and Unicode identifiers without changing wire IDs', () => {
    const ascii = { id: 1, fields: { parent: { fieldID: 1, typeName: 'Nested' } },
      types: { Nested: { field: { fieldID: 2 } } } }
    const expected = encodeStaticRequestBody(ascii, codec, { $select: 'parent{field}' })
    for (const [parent, field] of [['aé2', 'b𐐀_9'], ['éclair', 'a中文2'], ['𐐀_parent9', 'field𐐀2']]) {
      const meta = { id: 1, fields: { [parent!]: { fieldID: 1, typeName: 'Nested' } },
        types: { Nested: { [field!]: { fieldID: 2 } } } }
      const text = `${parent}{${field}}`
      expect(encodeStaticRequestBody(meta, codec, { $select: text })).toEqual(expected)
      expect(encodeStaticRequestBody(meta, codec, { $select: prepareSelection(text) })).toEqual(expected)
    }
  })

  it('rejects isolated surrogates and non-letter continuations after ASCII', () => {
    for (const text of ['\ud800', '\udc00', 'a\ud800', 'a\udc00', 'a\ud800b', 'a\udc00b', 'a😀', 'a２']) {
      expect(() => prepareSelection(text)).toThrow()
      expect(() => encodeStaticRequestBody(schema(1), codec, { $select: text })).toThrow()
    }
  })

  it('preserves Unicode DSL letters and exact prepared wire IDs', () => {
    const meta = { id: 1, fields: { café: { fieldID: 1 }, '中文2': { fieldID: 2, typeName: 'Nested' } },
      types: { Nested: { '𐐀_name': { fieldID: 3 } } } }
    const ascii = { id: 1, fields: { cafe: { fieldID: 1 }, nested: { fieldID: 2, typeName: 'Nested' } },
      types: { Nested: { name: { fieldID: 3 } } } }
    const text = 'café,中文2{𐐀_name}'
    const expected = encodeStaticRequestBody(ascii, codec, { $select: 'cafe,nested{name}' })
    expect(encodeStaticRequestBody(meta, codec, { $select: text })).toEqual(expected)
    expect(encodeStaticRequestBody(meta, codec, { $select: prepareSelection(text) })).toEqual(expected)
    for (const invalid of ['2café', 'café²', 'e\u0301', '😀', '字\u200d', '\ud800']) {
      expect(() => prepareSelection(invalid)).toThrow()
    }
  })
  it('rejects trailing separators in dynamic and prepared selections', () => {
    for (const text of ['user,', 'user,  ', 'user{id,}', 'user{id, }', 'user{id},']) {
      expect(() => prepareSelection(text)).toThrow()
      expect(() => encodeStaticRequestBody(schema(1), codec, { $select: text })).toThrow()
    }
  })
  it('preserves nested wire and separates Schema identities', () => {
    const plan = prepareSelection('user{id}')
    for (const meta of [schema(1), schema(3)]) {
      const expected = encodeStaticRequestBody(meta, codec, { $select: 'user{id}' })
      for (let i = 0; i < 3; i++) {
        const bytes = encodeStaticRequestBody(meta, codec, { $select: plan })
        expect(bytes).toEqual(expected)
        bytes.fill(255)
      }
    }
  })
  it('keeps call sites and manual overrides independent', () => {
    const meta = schema(1)
    const id = prepareSelection('user{id}')
    const name = prepareSelection('user{name}')
    expect(encodeStaticRequestBody(meta, codec, { $select: id }))
      .not.toEqual(encodeStaticRequestBody(meta, codec, { $select: name }))
    const manual: { $select?: string } = { $select: 'user{name}' }
    const params = { $select: id, ...manual }
    expect(encodeStaticRequestBody(meta, codec, params))
      .toEqual(encodeStaticRequestBody(meta, codec, { $select: name }))
    expect(requestJSON({ $select: id })).toEqual({ $select: 'user{id}' })
  })
  it('does not cache mutable external schema or expose plan state', () => {
    const meta = { id: 1, fields: { id: { fieldID: 1 } } }
    const plan = prepareSelection('id')
    const before = encodeStaticRequestBody(meta, codec, { $select: plan })
    meta.fields.id.fieldID = 2
    expect(encodeStaticRequestBody(meta, codec, { $select: plan })).not.toEqual(before)
    expect(Object.isFrozen(plan)).toBe(true)
  })
  it('retains syntax and schema rejection', () => {
    expect(() => prepareSelection('user{')).toThrow()
    expect(() => prepareSelection('id,id')).toThrow()
    expect(() => encodeStaticRequestBody(schema(1), codec, { $select: prepareSelection('missing') })).toThrow()
  })
})
