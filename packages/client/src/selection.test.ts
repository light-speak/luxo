import { describe, expect, it } from 'vitest'
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
