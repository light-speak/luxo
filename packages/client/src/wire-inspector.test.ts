import { describe, expect, it } from 'vitest'
import { Encoder } from './codec'
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
