import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { decodeJSONEnvelope, decodeLuxoJSON, encodeLuxoJSON, normalizeLuxoJSON, stringFromJSON } from './json'
import { decodeJSONValue, Decoder, Encoder } from './codec'

const utf8 = new TextEncoder()
const strictUTF8 = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })
const nativeJSON = JSON as JSON & { rawJSON(text: string): unknown }
const malformed = ['\ud800', '\udbff', '\udc00', '\udfff', '\ud800x', '\udc00\ud800', '\ud800\ud800']

describe('complete Unicode across JSON and Binary', () => {
  it('uses the shared protocol cases, including invalid overwritten values', () => {
    const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/protocol-v1.json', import.meta.url), 'utf8')) as {
      jsonUnicodeCases: { name: string; raw: string; valid: boolean }[]
    }
    expect(fixture.jsonUnicodeCases.length).toBe(23)
    for (const { name, raw, valid } of fixture.jsonUnicodeCases) {
      for (const decode of [() => decodeLuxoJSON(raw), () => decodeJSONValue(utf8.encode(raw)),
        () => decodeJSONEnvelope('{"data":' + raw + '}').data]) {
        if (!valid) expect(decode, name).toThrow()
        else {
          const value = decode()
          expect(decodeLuxoJSON(encodeLuxoJSON(value)), name).toEqual(value)
        }
      }
    }
  })

  it('rejects malformed host strings, keys, boxed strings and toJSON values', () => {
    for (const text of malformed) {
      expect(() => stringFromJSON(text)).toThrow(/Unicode|surrogate/)
      for (const value of [text, { text }, [text], { [text]: 1 }, { nested: [{ [text]: text }] },
        new String(text), { toJSON: () => text }, { toJSON: () => ({ [text]: 1 }) }]) {
        expect(() => encodeLuxoJSON(value)).toThrow(/Unicode|surrogate/)
      }
      for (const value of [text, { [text]: true }, { items: [text] }]) {
        expect(() => normalizeLuxoJSON(value)).toThrow(/Unicode|surrogate/)
      }
      for (const raw of ['"' + text + '"', JSON.stringify(text)]) {
        expect(() => decodeLuxoJSON(raw)).toThrow(/Unicode|surrogate/)
        const fragment = nativeJSON.rawJSON(raw)
        expect(() => encodeLuxoJSON({ fragment })).toThrow(/Unicode|surrogate/)
        expect(() => normalizeLuxoJSON(fragment)).toThrow(/Unicode|surrogate/)
      }
    }
  })

  it('preserves pairs, literal escapes, noncharacters and native raw primitives', () => {
    for (const text of ['', 'ascii', '東京', '\ud800\udc00', '\udbff\udfff', '\ufffd', '\u0000\ufeff',
      '\ud7ff\ue000\uffff', '\\uD800', '\\\\uDC00', 'e\u0301']) {
      expect(stringFromJSON(text)).toBe(text)
      const value = { [text]: [text, 123456789012345678901234567890n] }
      expect(normalizeLuxoJSON(value)).toBe(value)
      expect(decodeLuxoJSON(encodeLuxoJSON(value))).toEqual(value)
      const enc = new Encoder()
      enc.writeString(text)
      const dec = new Decoder(enc.bytes())
      expect(dec.readString()).toBe(text)
      expect(dec.error).toBeNull()
      expect(dec.remaining).toBe(0)
    }
    for (const raw of ['true', 'null', '"text"', '"\\uD800\\uDC00"']) {
      const fragment = nativeJSON.rawJSON(raw)
      expect(encodeLuxoJSON(fragment)).toBe(raw)
      expect(normalizeLuxoJSON(fragment)).toBe(fragment)
    }
  })

  it('does not publish a partial string or string field/array after a failed write', () => {
    for (const text of malformed) {
      for (const write of [
        (enc: Encoder) => enc.writeString(text),
        (enc: Encoder) => enc.writeFieldString(2, text),
        (enc: Encoder) => enc.writeFieldStringArray(3, ['valid', text, 'after']),
      ]) {
        const enc = new Encoder()
        enc.writeFieldInt(1, 42n)
        const prefix = new Uint8Array(enc.bytes())
        expect(() => write(enc)).toThrow(/Unicode|surrogate/)
        expect(enc.bytes()).toEqual(prefix)
        enc.writeString('next')
        expect(enc.bytes().length).toBeGreaterThan(prefix.length)
      }
    }
  })

  it('matches independently decoded JSON string tokens on deterministic escape mixtures', () => {
    const fragments = ['a', '\\\\', '\\"', '\\n', '\\u0000', '\\uD800', '\\uDC00', '\\udbff', '\\udfff',
      '\\u0061', '\\uD7FF', '\\uE000', '東京', '\ud800', '\udc00', '\ud800\udc00']
    let state = 0x51a7c0de
    for (let sample = 0; sample < 4096; sample++) {
      let value = ''
      for (let index = 0; index < 1 + sample % 8; index++) {
        state = (Math.imul(state, 1664525) + 1013904223) >>> 0
        value += fragments[state >>> 28]
      }
      for (const raw of ['"' + value + '"', '{"' + value + '":1}', '{"x":"' + value + '","x":"valid"}']) {
        let valid = strictUTF8.decode(utf8.encode(raw)) === raw
        try {
          JSON.parse(raw)
          for (const token of raw.matchAll(/"(?:[^"\\]|\\[\s\S])*"/g)) {
            const text = JSON.parse(token[0]) as string
            if (strictUTF8.decode(utf8.encode(text)) !== text) valid = false
          }
        } catch { valid = false }
        if (valid) expect(() => decodeLuxoJSON(raw), raw).not.toThrow()
        else expect(() => decodeLuxoJSON(raw), raw).toThrow()
      }
    }
  })
})
