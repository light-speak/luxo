import { describe, expect, it, vi } from 'vitest'
import { JSONEnvelopeSource, JSONResourceCompiler, genericJSONResource, opaqueJSONResource } from './json-resource'
import { decodeJSONEnvelope } from './json'

const json = (depth: number): string => '['.repeat(depth) + '0' + ']'.repeat(depth)
const node = (depth: number, leaf = '{}'): string => '{"next":'.repeat(depth - 1) + leaf + '}'.repeat(depth - 1)
const compiler = new JSONResourceCompiler({ Node: {
  next: { fieldID: 1, type: 'Model', typeName: 'Node' },
  json: { fieldID: 2, type: 'JSON' }, jsons: { fieldID: 3, type: 'JSON', isList: true },
} })

describe('response source resources', () => {
  it.each([false, true])('checks overwritten fields and envelope data before native parsing (list=%s)', list => {
    const plan = compiler.response({ type: 'Model', typeName: 'Node', isList: list })
    const wrap = (text: string): string => list ? '[' + text + ']' : text
    const bad = [node(65), '{"next":' + node(64) + ',"next":null}',
      '{"json":' + json(129) + ',"j\\u0073on":0}']
    const parse = vi.spyOn(JSON, 'parse')
    try {
      for (const value of bad) {
        const source = new JSONEnvelopeSource('{"d\\u0061ta":' + wrap(value) + ',"data":null}')
        expect(() => source.validateData(plan)).toThrow(/64|128/)
      }
      expect(parse).not.toHaveBeenCalled()
    } finally { parse.mockRestore() }
    const valid = wrap(node(64, '{"json":' + json(128) + ',"jsons":[' + json(128) + ']}'))
    const text = '{"data":' + valid + ',"data":42}'
    new JSONEnvelopeSource(text).validateData(plan)
    expect(decodeJSONEnvelope(text).data).toBe(42)
  })

  it('separates page/list wrappers from model and generic JSON roots', () => {
    for (const type of ['Model', 'JSON']) {
      const value = type === 'Model' ? node(64, '{"json":' + json(128) + '}') : json(128)
      for (const [isList, paginated] of [[false, false], [true, false], [true, true]]) {
        const plan = compiler.response({ type, typeName: 'Node', isList, paginated })
        const data = paginated ? '{"items":[' + value + '],"total":1}' : isList ? '[' + value + ']' : value
        expect(() => new JSONEnvelopeSource('{"data":' + data + '}').validateData(plan)).not.toThrow()
      }
    }
    const plan = compiler.response({ type: 'Model', typeName: 'Node', paginated: true })
    expect(() => new JSONEnvelopeSource('{"data":{"items":[' + node(65) + '],"items":[]}}').validateData(plan)).toThrow(/64/)
  })

  it('uses final error presence and checks every error data occurrence independently', () => {
    const validModel = node(64, '{"json":' + json(128) + '}')
    const plan = compiler.model('Node')
    new JSONEnvelopeSource('{"data":' + validModel + '}').validateData(plan)
    for (const tail of [',"error":"bad"', ',"error":null', ',"error":"old","error":"bad"']) {
      expect(() => new JSONEnvelopeSource('{"data":' + json(129) + ',"data":0' + tail + '}').validateData(plan)).toThrow(/128/)
    }
    expect(() => new JSONEnvelopeSource('{"data":' + json(129) + '}').validateData(plan, true)).toThrow(/128/)
    expect(() => new JSONEnvelopeSource('{"data":' + json(200) + '}').validateData(opaqueJSONResource)).not.toThrow()
  })

  it('validates the complete syntax before reporting resources or routing', () => {
    for (const text of ['[]', 'null', '{"data":' + json(129) + '} trailing',
      '{"data":' + json(129) + ',"unknown":[0,]}', '{"data":' + json(129).replace('0', '[0,]') + '}']) {
      expect(() => new JSONEnvelopeSource(text).validateData(genericJSONResource)).toThrow(SyntaxError)
    }
  })

  it('routes by final full-width integer tokens and existing control-key precedence', () => {
    for (const [text, kind, id] of [
      ['{"$id":1,"$i\\u0064":2}', '$id', 2],
      ['{"$id":1,"$stream":9007199254740992}', '$stream', 9007199254740992n],
      ['{"$sub":18446744073709551615}', '$sub', 18446744073709551615n],
      ['{"$end":7}', '$end', 7],
      ['{"$stream":2,"$sub":3}', '$sub', 3],
    ] as const) {
      const source = new JSONEnvelopeSource(text)
      expect(source.kind).toBe(kind)
      expect(source.id).toBe(id)
    }
    for (const token of ['-0', '-1', '1.0', '1e0', '"1"', 'true', '{}', '[]', '18446744073709551616']) {
      expect(new JSONEnvelopeSource('{"$id":1,"$id":' + token + '}').id).toBeUndefined()
    }
    expect(new JSONEnvelopeSource('{"$end":1,"$id":2}').kind).toBeUndefined()
    expect(new JSONEnvelopeSource('{"$id":1,"$stream":null}').id).toBeUndefined()
  })

  it('rejects arbitrarily wide routing integers without native parsing or BigInt construction', () => {
    const parse = vi.spyOn(JSON, 'parse'), wide = vi.spyOn(globalThis, 'BigInt')
    try {
      for (const digits of [21, 10_000]) {
        const source = new JSONEnvelopeSource('{"$id":' + '9'.repeat(digits) + ',"data":0}')
        expect(source.kind).toBe('$id')
        expect(source.id).toBeUndefined()
      }
      expect(parse).not.toHaveBeenCalled()
      expect(wide).not.toHaveBeenCalled()
    } finally { parse.mockRestore(); wide.mockRestore() }
  })

  it('matches native object syntax across deterministic envelope mutations', () => {
    const bases = ['{}', '{"data":0}', '{"data":{"a":[true,false,null,-1.25e+3]}}',
      '{"$id":1,"$id":2,"data":{},"error":null}', '{"data":"a\\\"b","d\\u0061ta":[]}']
    const alphabet = '[]{}:,0123eE+-."\\ abcdefnulrt\n\t'
    let seed = 0x4f1c88
    const random = (limit: number): number => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
      return seed % limit
    }
    for (let sample = 0; sample < 5000; sample++) {
      let text = bases[random(bases.length)]
      for (let i = 0, count = random(4); i < count; i++) {
        const index = random(text.length + 1), char = alphabet[random(alphabet.length)]
        text = random(2) ? text.slice(0, index) + char + text.slice(index) : text.slice(0, index) + text.slice(index + 1)
      }
      let native = true, source = true
      try {
        const value: unknown = JSON.parse(text)
        native = value !== null && typeof value === 'object' && !Array.isArray(value)
      } catch { native = false }
      try { new JSONEnvelopeSource(text).validateData(opaqueJSONResource) } catch { source = false }
      expect(source, `sample ${sample}: ${JSON.stringify(text)}`).toBe(native)
    }
  })
})
