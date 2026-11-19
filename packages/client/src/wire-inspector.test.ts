import { describe, expect, it } from 'vitest'
import { Encoder } from './codec'
import { encodeParam } from './transport'
import { inspectBinaryRequest } from './wire-inspector'

describe('inspectBinaryRequest', () => {
  const schema = {
    login: {
      id: 7,
      fields: { id: { fieldID: 1 }, name: { fieldID: 2 } },
      params: [
        { fieldID: 1, name: 'username', type: 'String' },
        { fieldID: 2, name: 'password', type: 'String' },
        { fieldID: 3, name: 'scopes', type: 'String', isList: true },
      ],
    },
  }

  it.each([
    ['DateTime', false, false, '2026-10-31T00:00:00.123456789Z'],
    ['DateTime', true, false, ['1969-12-31T23:59:59.999999999Z', '0000-01-01T00:00:00Z']],
    ['Int', false, true, null],
    ['Int', false, true, 42n],
    ['String', true, true, ['private']],
    ['String', true, true, null],
    ['JSON', false, false, { secret: 'private' }],
    ['JSON', true, false, [null, { secret: 'private' }]],
  ] as const)('inspects %s list=%s nullable=%s without losing the next field', (type, isList, nullable, value) => {
    const param = { fieldID: 1, name: 'value', type, isList, nullable }
    const tail = { fieldID: 2, name: 'tail', type: 'Int' }
    const enc = new Encoder()
    enc.writeVarint(7)
    enc.writeVarint(0)
    encodeParam(enc, param, Array.isArray(value) ? [...value] : value)
    encodeParam(enc, tail, 99n)
    enc.writeEnd()
    const result = inspectBinaryRequest(enc.bytes(), { echo: { id: 7, params: [param, tail] } })
    expect(result.fields.map(field => field.name)).toEqual(['value', 'tail'])
    expect(JSON.stringify(result)).not.toContain('private')
  })

  it.each([1_000_000_000, 1_000_000_001])('rejects invalid DateTime fraction %s', nanos => {
    const enc = new Encoder()
    enc.writeVarint(7)
    enc.writeVarint(0)
    enc.writeVarint(1)
    enc.writeSvarint(0n)
    enc.writeVarint(nanos)
    enc.writeEnd()
    expect(() => inspectBinaryRequest(enc.bytes(), {
      echo: { id: 7, params: [{ fieldID: 1, name: 'at', type: 'DateTime' }] },
    })).toThrow('DateTime')
  })

  it('reports protocol structure without exposing values or raw bytes', () => {
    const secret = 'DO_NOT_LOG_THIS_SECRET'
    const enc = new Encoder()
    enc.writeVarint(7)
		enc.writeVarint(2)
		enc.writeRawBytes(new Uint8Array([1, 0b00000010]))
    enc.writeFieldString(1, 'admin')
    enc.writeFieldString(2, secret)
    enc.writeFieldStringArray(3, ['read', 'write'])
    enc.writeEnd()

    const result = inspectBinaryRequest(enc.bytes(), schema)

    expect(result).toEqual({
      apiID: 7,
      api: 'login',
      frameBytes: enc.bytes().length,
      selectionBytes: 2,
      selectedFields: ['name'],
      fields: [
        { fieldID: 1, name: 'username', type: 'String', isList: false, encodedBytes: 6 },
        { fieldID: 2, name: 'password', type: 'String', isList: false, encodedBytes: 23 },
        { fieldID: 3, name: 'scopes', type: 'String', isList: true, itemCount: 2, encodedBytes: 12 },
      ],
    })
    expect(JSON.stringify(result)).not.toContain(secret)
    expect(result).not.toHaveProperty('hex')
    expect(result).not.toHaveProperty('payload')
  })

  it('rejects malformed and schema-unknown frames', () => {
    expect(() => inspectBinaryRequest(new Uint8Array([7]), schema)).toThrow('field mask length')

    const enc = new Encoder()
    enc.writeVarint(7)
    enc.writeVarint(0)
    enc.writeFieldString(9, 'unknown')
    enc.writeEnd()
    expect(() => inspectBinaryRequest(enc.bytes(), schema)).toThrow('unknown field ID 9')
  })

  it('reports list controls without exposing filter values', () => {
    const enc = new Encoder()
    enc.writeVarint(7)
    enc.writeVarint(0)
    enc.writeVarint(0x7ffffffe)
    enc.writeVarint(1)
    enc.writeString('password')
    enc.writeVarint(1)
    enc.writeString('DO_NOT_LOG_THIS_SECRET')
    enc.writeVarint(0x7fffffff)
    enc.writeVarint(1)
    enc.writeString('createdAt')
    enc.writeBool(true)
    enc.writeEnd()

    const result = inspectBinaryRequest(enc.bytes(), schema)

    expect(result.fields).toEqual([
      { fieldID: 0x7ffffffe, name: '$filters', type: 'Filter', isList: true, itemCount: 1, encodedBytes: 34 },
      { fieldID: 0x7fffffff, name: '$sorters', type: 'Sorter', isList: true, itemCount: 1, encodedBytes: 12 },
    ])
    expect(JSON.stringify(result)).not.toContain('DO_NOT_LOG_THIS_SECRET')
  })

  it('rejects non-canonical list controls', () => {
    const enc = new Encoder()
    enc.writeVarint(7)
    enc.writeVarint(0)
    enc.writeVarint(0x7fffffff)
    enc.writeVarint(1)
    enc.writeString('createdAt')
    enc.writeRawBytes(new Uint8Array([2]))
    enc.writeEnd()

    expect(() => inspectBinaryRequest(enc.bytes(), schema)).toThrow('invalid sorter direction')
  })
})
