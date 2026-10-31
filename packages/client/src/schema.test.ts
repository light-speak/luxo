import { describe, expect, expectTypeOf, it } from 'vitest'
import type { LuxoComputedSource, LuxoSchema } from './index'
import { MAX_API_ID, MAX_PARAM_ID, MAX_SELECTION_FIELD_ID, validateSchemaIDs } from './schema'

describe('stable Schema IDs', () => {
  it('keeps classified limits distinct from business integer ranges', () => {
    expect(MAX_SELECTION_FIELD_ID).toBe(65535)
    expect(MAX_API_ID).toBe(2147483647)
    expect(MAX_PARAM_ID).toBe(2147483645)
    validateSchemaIDs({ models: {}, apis: {} })
  })

  it('validates without mutating the caller directory', () => {
    const field = Object.freeze({ id: MAX_SELECTION_FIELD_ID, name: 'value', type: 'Int' })
    const schema: LuxoSchema = {
      models: { Value: { name: 'Value', fields: [field] } },
      types: { Input: { name: 'Input', fields: [field] } },
      apis: { read: { id: MAX_API_ID, name: 'read', module: 'test', params: [{ id: MAX_PARAM_ID, name: 'input', type: 'Int' }] } },
    }
    validateSchemaIDs(schema)
    expect(schema.models.Value.fields[0]).toBe(field)
    expect(schema.apis.read.id).toBe(MAX_API_ID)
  })

  it('rejects duplicates even when both values are in range', () => {
    const schema: LuxoSchema = { models: {}, apis: {
      read: { id: 1, name: 'read', module: 'test' },
      write: { id: 1, name: 'write', module: 'test' },
    } }
    expect(() => validateSchemaIDs(schema)).toThrow('duplicate ID 1')
  })

  it.each([0, -1, 65536, 1.5, NaN, Infinity])('rejects field ID %s', id => {
    expect(() => validateSchemaIDs({ models: {
      Value: { name: 'Value', fields: [{ id, name: 'value', type: 'Int' }] },
    }, apis: {} })).toThrow(RangeError)
  })
})

describe('schema computed sources', () => {
  it('keeps aggregate dependencies separate from field wire types', () => {
    const schema = {
      models: {
        User: {
          name: 'User',
          fields: [{ id: 1, name: 'postCount', type: 'Int' }],
          computedSources: { postCount: { model: 'Post', module: 'blog' } },
        },
        Plain: { name: 'Plain', fields: [] },
      },
      apis: {},
    } satisfies LuxoSchema
    expectTypeOf(schema.models.User.computedSources.postCount).toExtend<LuxoComputedSource>()
    expect(schema.models.User.computedSources.postCount.module).toBe('blog')
    expect(schema.models.User.fields[0]?.type).toBe('Int')
  })
})
