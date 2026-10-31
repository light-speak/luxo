import { measure } from './measure.bench'

type Client = typeof import('./index')

/** Compare complete, independently built packages; fixtures are outside timing. */
export function measureUnicode(before: Client, after: Client): void {
  const texts: [string, string][] = [
    ['ascii16', 'abcdefghijklmnop'],
    ['chinese24', '发布检查数据完整'],
    ['chinese1026', '文'.repeat(342)],
    ['chinese72000', '文'.repeat(24000)],
    ['pairs128', '\ud800\udc00'.repeat(128)],
    ['literalEscapes128', '\\uD800'.repeat(128)],
  ]
  for (const [name, text] of texts) {
    measure(`readString/${name}`, () => before.stringFromJSON(text), () => after.stringFromJSON(text))
    measureJSON(before, after, name, { value: text })
    measureBinary(before, after, name, text)
  }
  const escaped = '{"value":"' + '\\uD800\\uDC00'.repeat(128) + '"}'
  measure('decode/escapedPairs128', () => before.decodeLuxoJSON(escaped), () => after.decodeLuxoJSON(escaped))
  const rows = Array.from({ length: 128 }, (_, index) => ({ name: 'row' + index, label: '发布检查', valid: true }))
  measureJSON(before, after, 'objects128', rows)
  const precision = '{"integer":9223372036854775807,"fraction":0.1234567890123456789,"label":"发布检查"}'
  measure('decode/precision', () => before.decodeLuxoJSON(precision), () => after.decodeLuxoJSON(precision))
  const value = after.decodeLuxoJSON(precision)
  measure('encode/precision', () => before.encodeLuxoJSON(value), () => after.encodeLuxoJSON(value))
}

function measureJSON(before: Client, after: Client, name: string, value: unknown): void {
  const text = before.encodeLuxoJSON(value)
  measure(`decode/${name}`, () => before.decodeLuxoJSON(text), () => after.decodeLuxoJSON(text))
  measure(`encode/${name}`, () => before.encodeLuxoJSON(value), () => after.encodeLuxoJSON(value))
  measure(`normalize/${name}`, () => before.normalizeLuxoJSON(value), () => after.normalizeLuxoJSON(value))
}

function measureBinary(before: Client, after: Client, name: string, text: string): void {
  const oldEncoder = new before.Encoder(), newEncoder = new after.Encoder()
  measure(`writeString/${name}`, () => {
    oldEncoder.reset(); oldEncoder.writeString(text); return oldEncoder.bytes()
  }, () => {
    newEncoder.reset(); newEncoder.writeString(text); return newEncoder.bytes()
  })
  measure(`writeFieldString/${name}`, () => {
    oldEncoder.reset(); oldEncoder.writeFieldString(1, text); return oldEncoder.bytes()
  }, () => {
    newEncoder.reset(); newEncoder.writeFieldString(1, text); return newEncoder.bytes()
  })
  if (name === 'chinese72000') return
  const values = Array.from({ length: 128 }, () => text)
  measure(`writeStrings128/${name}`, () => {
    oldEncoder.reset(); oldEncoder.writeFieldStringArray(1, values); return oldEncoder.bytes()
  }, () => {
    newEncoder.reset(); newEncoder.writeFieldStringArray(1, values); return newEncoder.bytes()
  })
}
