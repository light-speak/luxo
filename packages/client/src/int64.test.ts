import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { ColumnarDecoder, Decoder, Encoder } from './codec'
import { encodeBinaryBody } from './transport'
import { inspectBinaryRequest } from './wire-inspector'
import { writeWireInt64 } from './int64'

const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/protocol-v1.json', import.meta.url), 'utf8')) as {
  int64Cases: { value: string; wire: string }[]
  invalidInt64: string[]
}

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
}

function reference(value: bigint): Uint8Array {
  let unsigned = (value << 1n) ^ (value >> 63n)
  const bytes: number[] = []
  while (unsigned >= 128n) {
    bytes.push(Number(unsigned & 127n) | 128)
    unsigned >>= 7n
  }
  bytes.push(Number(unsigned))
  return Uint8Array.from(bytes)
}

describe('full-width int64', () => {
  it('preserves destination boundaries and validates before writing', () => {
    for (const value of [0n, -1n, 2147483648n, -2147483649n, 9007199254740993n, -(1n << 63n), (1n << 63n) - 1n]) {
      const expected = reference(value)
      const output = new Uint8Array(20).fill(165)
      expect(writeWireInt64(output, 3, value)).toBe(3 + expected.length)
      expect(output.subarray(3, 3 + expected.length)).toEqual(expected)
      expect(output.subarray(0, 3)).toEqual(new Uint8Array(3).fill(165))
      expect(output.subarray(3 + expected.length)).toEqual(new Uint8Array(17 - expected.length).fill(165))
    }
    for (const value of [null, undefined, 1, '1', {}, -(1n << 63n) - 1n, 1n << 63n]) {
      const output = new Uint8Array(20).fill(165)
      expect(() => writeWireInt64(output, 3, value as bigint)).toThrow()
      expect(output).toEqual(new Uint8Array(20).fill(165))
    }
  })

  it('keeps extracted words independent when destination writes reenter the encoder', () => {
    const output = new Uint8Array(10)
    const nested = new Uint8Array(10)
    let entered = false
    const destination = new Proxy(output, { set(target, property, value) {
      if (!entered) {
        entered = true
        expect(writeWireInt64(nested, 0, -(1n << 63n))).toBe(10)
      }
      return Reflect.set(target, property, value)
    } })
    expect(writeWireInt64(destination, 0, (1n << 63n) - 1n)).toBe(10)
    expect(output).toEqual(reference((1n << 63n) - 1n))
    expect(nested).toEqual(reference(-(1n << 63n)))
  })

  it('inspects full-width request values without a number conversion', () => {
    const schema = {
      sample: { id: 1, params: [
        { fieldID: 1, name: 'id', type: 'Int' },
        { fieldID: 2, name: 'durations', type: 'Duration', isList: true },
      ] },
    }
    for (const item of fixture.int64Cases) {
      const value = BigInt(item.value)
      const body = encodeBinaryBody(schema.sample, { id: value, durations: [value] })
      const inspection = inspectBinaryRequest(body, schema)
      expect(inspection.fields.map(field => field.encodedBytes)).toEqual([item.wire.length / 2, item.wire.length / 2 + 1])
    }
    for (const bytes of [[128], [...Array(9).fill(128), 2]]) {
      expect(() => inspectBinaryRequest(Uint8Array.from([1, 0, 1, ...bytes]), schema)).toThrow()
    }
  })

  it('matches the shared Go protocol fixtures', () => {
    for (const item of fixture.int64Cases) {
      const value = BigInt(item.value)
      const enc = new Encoder()
      enc.writeSvarint(value)
      expect(hex(enc.bytes())).toBe(item.wire)
      expect(new Decoder(enc.bytes()).readInt()).toBe(value)
    }
  })

  it('matches an independent BigInt reference for 10,000 signed values', () => {
    let state = 0x123456789abcdef0n
    const enc = new Encoder()
    for (let index = 0; index < 10000; index++) {
      state = BigInt.asUintN(64, state * 6364136223846793005n + 1442695040888963407n)
      const value = BigInt.asIntN(64, state)
      enc.reset()
      enc.writeSvarint(value)
      expect(enc.bytes()).toEqual(reference(value))
      expect(new Decoder(enc.bytes()).readInt()).toBe(value)
    }
  })

  it('rejects overflow and malformed wire values', () => {
    for (const value of [1n << 63n, -(1n << 63n) - 1n]) {
      expect(() => new Encoder().writeSvarint(value)).toThrow(/int64/)
    }
    for (const bytes of [[], [128], [...Array(9).fill(128), 2], [...Array(10).fill(128), 0]]) {
      const dec = new Decoder(Uint8Array.from(bytes))
      dec.readInt()
      expect(dec.error).not.toBeNull()
    }
    expect(() => new Encoder().writeSvarint(1 as unknown as bigint)).toThrow(/bigint/)
  })

  it('preserves arrays, nullable columns and pagination metadata', () => {
    const values = [-(1n << 63n), 9007199254740993n, (1n << 63n) - 1n]
    const enc = new Encoder(0)
    enc.writeFieldIntArray(1, values)
    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.readIntArray()).toEqual(values)
    enc.reset()
    enc.writeVarint(3)
    enc.writeVarint(0)
    enc.writeVarint(1)
    enc.writeBool(false)
    enc.writeBool(true)
    enc.writeSvarint(values[1])
    enc.writeBool(true)
    enc.writeSvarint(values[2])
    enc.writeEnd()
    enc.writeSvarint(values[2])
    const col = new ColumnarDecoder(enc.bytes())
    expect(col.nextColumn()).toBe(true)
    expect(col.readColumnIntPtr()).toEqual([null, values[1], values[2]])
    expect(col.nextColumn()).toBe(false)
    expect(col.readSvarint()).toBe(values[2])
    expect(col.error).toBeNull()
  })
})
