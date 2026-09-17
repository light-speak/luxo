import { describe, expect, it } from 'vitest'
import { ColumnarDecoder, Decoder, Encoder } from './codec'

function wire(value: number): Uint8Array {
  const bytes = new Uint8Array(8)
  new DataView(bytes.buffer).setFloat64(0, value, true)
  return bytes
}

describe('finite protocol Float', () => {
  for (const value of [NaN, Infinity, -Infinity]) {
    it(`rejects ${value} in the writer and all readers`, () => {
      expect(() => new Encoder().writeFixed64(value)).toThrow(/finite/i)
      const decoder = new Decoder(wire(value))
      decoder.readFloat()
      expect(decoder.error).toMatch(/finite/i)
      const array = new Decoder(new Uint8Array([1, ...wire(value)]))
      array.readFloatArray()
      expect(array.error).toMatch(/finite/i)
      for (const nullable of [false, true]) {
        const column = new ColumnarDecoder(new Uint8Array([1, 0, 1, ...(nullable ? [1] : []), ...wire(value), 0]))
        column.nextColumn()
        expect(nullable ? column.readColumnFloatPtr() : column.readColumnFloat()).toEqual([])
        expect(column.error).toMatch(/finite/i)
      }
    })
  }
  it('preserves zero signs, subnormals and maximum finite values', () => {
    for (const value of [0, -0, Number.MIN_VALUE, -Number.MIN_VALUE, Number.MAX_VALUE, -Number.MAX_VALUE]) {
      const encoder = new Encoder()
      encoder.writeFixed64(value)
      expect(encoder.bytes()).toEqual(wire(value))
      const decoder = new Decoder(encoder.bytes())
      expect(Object.is(decoder.readFloat(), value)).toBe(true)
      expect(decoder.error).toBeNull()
    }
  })
})
