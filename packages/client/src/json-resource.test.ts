import { describe, expect, it, vi } from 'vitest'
import { assertJSONResourceSource, JSONResourceCompiler, genericJSONResource } from './json-resource'
import { encodeLuxoJSON } from './json'
import { Encoder, validateJSONValue, validateJSONArray } from './codec'

const compiler = (): JSONResourceCompiler => new JSONResourceCompiler({ Node: {
  next: { fieldID: 1, type: 'Model', typeName: 'Node', nullable: true },
  children: { fieldID: 2, type: 'Model', typeName: 'Node', isList: true },
  json: { fieldID: 3, type: 'JSON' }, jsons: { fieldID: 4, type: 'JSON', isList: true },
  ['__proto__']: { fieldID: 5, type: 'JSON' }, ['中😀']: { fieldID: 6, type: 'JSON' },
} })
const generic = (depth: number, leaf = '0'): string => '['.repeat(depth) + leaf + ']'.repeat(depth)
const node = (depth: number, leaf = '{}', list = false): string =>
  (list ? '{"children":[' : '{"next":').repeat(depth - 1) + leaf + (list ? ']}' : '}').repeat(depth - 1)

describe('serialized JSON resource validation', () => {
  it('checks sealed payload hash collisions and missing declarations through real JSON', () => {
    const types = {
      '@sealed:Outcome': { Done: { fieldID: 1, type: 'Model', typeName: 'Outcome.Done' } },
      'Outcome.Done': { costarring: { fieldID: 1, type: 'JSON' }, liquid: { fieldID: 2, type: 'Model', typeName: 'Child' } },
      Child: { value: { fieldID: 1, type: 'String' } },
    }
    const plan = new JSONResourceCompiler(types).response({ type: 'Sealed', typeName: 'Outcome' })
    expect(() => assertJSONResourceSource('{"Done":{"costarring":0,"liquid":{"value":"ok"}}}', plan)).not.toThrow()
    expect(() => assertJSONResourceSource('{"Done":{"costarring":' + generic(129) + '}}', plan)).toThrow(/128/)
    const missing = new JSONResourceCompiler().response({ type: 'Sealed', typeName: 'Missing' })
    expect(() => assertJSONResourceSource('{"Done":{}}', missing)).toThrow(/missing schema/)
    const missingPayload = new JSONResourceCompiler({ '@sealed:Outcome': types['@sealed:Outcome'] }).response({ type: 'Sealed', typeName: 'Outcome' })
    expect(() => assertJSONResourceSource('{"Done":{}}', missingPayload)).toThrow(/missing schema/)
  })
  it('checks Binary JSON cells and typed arrays without parsing or materializing discarded values', () => {
    const bytes = new TextEncoder().encode(generic(128))
    const enc = new Encoder()
    enc.writeArrayHeader(2)
    enc.writeBytes(bytes)
    enc.writeBytes(new TextEncoder().encode('1e400'))
    const parse = vi.spyOn(JSON, 'parse').mockImplementation(() => { throw new Error('unexpected native parse') })
    try {
      expect(() => validateJSONValue(bytes)).not.toThrow()
      expect(() => validateJSONArray(enc.bytes())).not.toThrow()
      expect(parse).not.toHaveBeenCalled()
    } finally { parse.mockRestore() }
    for (const cell of [Uint8Array.of(255), new TextEncoder().encode('[0,]'), new TextEncoder().encode(generic(129))]) {
      expect(() => validateJSONValue(cell)).toThrow()
      const array = new Encoder()
      array.writeArrayHeader(1)
      array.writeBytes(cell)
      expect(() => validateJSONArray(array.bytes())).toThrow()
    }
    for (const array of [Uint8Array.of(1), Uint8Array.of(1, 5, 48), Uint8Array.of(0, 0)]) {
      expect(() => validateJSONArray(array)).toThrow()
    }
    const oversized = new Encoder()
    oversized.writeVarint(1_000_001)
    expect(() => validateJSONArray(oversized.bytes())).toThrow(/array/)
  })

  it.each(['0', '[]', '{}', '1e400', '"[]{}"'])('counts generic containers with scalar syntax preserved (%s)', leaf => {
    for (const depth of [0, 127, 128, 129]) {
      const run = () => assertJSONResourceSource(generic(depth, leaf), genericJSONResource)
      if (depth + (leaf === '[]' || leaf === '{}' ? 1 : 0) <= 128) expect(run).not.toThrow()
      else expect(run).toThrow(/JSON nesting exceeds 128 levels/)
    }
  })

  it.each([false, true])('counts model layers independently of lists and JSON (list=%s)', list => {
    const plan = compiler().parameters([{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Node', isList: list }])
    for (const depth of [63, 64, 65]) {
      const value = node(depth, '{"json":' + generic(128) + ',"jsons":[' + generic(128) + ']}', list)
      const text = '{"value":' + (list ? '[' + value + ']' : value) + '}'
      if (depth <= 64) expect(() => assertJSONResourceSource(text, plan)).not.toThrow()
      else expect(() => assertJSONResourceSource(text, plan)).toThrow(/model nesting exceeds 64 levels/)
    }
    for (const leaf of ['{"children":[]}', '{"next":null}', '{}']) {
      expect(() => assertJSONResourceSource('{"value":' + (list ? '[' : '') + node(64, leaf) + (list ? ']' : '') + '}', plan)).not.toThrow()
    }
  })

  it.each(['json', '__proto__', '中😀'])('checks every repeated field including escaped names (%s)', name => {
    const plan = compiler().model('Node')
    const key = encodeLuxoJSON(name), escaped = '"' + [...name].map(char => {
      const units = char.length === 2 ? [char.charCodeAt(0), char.charCodeAt(1)] : [char.charCodeAt(0)]
      return units.map(code => '\\u' + code.toString(16).padStart(4, '0')).join('')
    }).join('') + '"'
    expect(() => assertJSONResourceSource('{' + key + ':' + generic(129) + ',' + escaped + ':0}', plan)).toThrow(/128/)
    expect(() => assertJSONResourceSource('{' + key + ':' + generic(128) + ',' + escaped + ':0}', plan)).not.toThrow()
  })

  it('checks old nested models without enforcing discarded business scalar types', () => {
    const plan = compiler().model('Node')
    expect(() => assertJSONResourceSource('{"next":' + node(64) + ',"next":null}', plan)).toThrow(/64/)
    expect(() => assertJSONResourceSource('{"next":' + node(63) + ',"next":null}', plan)).not.toThrow()
    expect(() => assertJSONResourceSource('{"next":"old wrong type","next":null}', plan)).not.toThrow()
  })

  it('resolves colliding key hashes by the full decoded name', () => {
    const plan = compiler().parameters([
      { fieldID: 1, name: 'costarring', type: 'JSON' },
      { fieldID: 2, name: 'liquid', type: 'Model', typeName: 'Node' },
    ])
    expect(() => assertJSONResourceSource('{"costarring":' + generic(129) + '}', plan)).toThrow(/128/)
    expect(() => assertJSONResourceSource('{"liquid":' + node(65) + '}', plan)).toThrow(/64/)
    expect(() => assertJSONResourceSource('{"costarring":0,"liquid":{}}', plan)).not.toThrow()
    const escaped = compiler().parameters([{ fieldID: 1, name: '\b\f\n\r\t/\\"', type: 'JSON' }])
    const key = encodeLuxoJSON('\b\f\n\r\t/\\"')
    expect(() => assertJSONResourceSource('{' + key + ':' + generic(129) + '}', escaped)).toThrow(/128/)
  })

  it('requires missing model metadata only for an object that is actually present', () => {
    const plan = compiler().model('Missing')
    expect(() => assertJSONResourceSource('null', plan)).not.toThrow()
    expect(() => assertJSONResourceSource('{}', plan)).toThrow('missing schema for structured type Missing')
  })

  it('finishes syntax validation after a resource limit and rejects malformed opaque fields', () => {
    expect(() => assertJSONResourceSource(generic(129) + ' trailing', genericJSONResource)).toThrow(SyntaxError)
    expect(() => assertJSONResourceSource(generic(129, '[0,]'), genericJSONResource)).toThrow(SyntaxError)
    expect(() => assertJSONResourceSource('{"next":' + node(64) + ',"unknown":[0,]}', compiler().model('Node'))).toThrow(SyntaxError)
    expect(() => assertJSONResourceSource('{"unknown":{"a":[0,]}}', compiler().model('Node'))).toThrow(SyntaxError)
    expect(() => assertJSONResourceSource(generic(20_000), genericJSONResource)).toThrow(/JSON nesting exceeds 128 levels/)
  })

  it.each(['', '[', '{', '[0,]', '{"x":}', '{"x" 0}', '{"x":0,}', '00', '-', '1.', '1e', '+1', 'trueX', 'null 0', '"\\q"', '"\\u00xz"', '"x\n"', '"unterminated', '[0}'])
  ('rejects malformed generic source without native parsing (%s)', text => {
    const parse = vi.spyOn(JSON, 'parse').mockImplementation(() => { throw new Error('unexpected native parse') })
    try { expect(() => assertJSONResourceSource(text, genericJSONResource)).toThrow(SyntaxError) }
    finally { parse.mockRestore() }
  })

  it('validates strings, escapes, whitespace and arbitrary JSON numbers without materializing values', () => {
    const text = ' \n{"a\\\"b":"\\b\\f\\n\\r\\t\\/\\\\\\\"\\u0061","中😀":-0.01e+400,"values":[true,false,null,0,-1,1E-2]}\t'
    const parse = vi.spyOn(JSON, 'parse').mockImplementation(() => { throw new Error('unexpected native parse') })
    try { expect(() => assertJSONResourceSource(text, genericJSONResource)).not.toThrow() }
    finally { parse.mockRestore() }
    expect(() => assertJSONResourceSource('{"x":"\\ud800"}', genericJSONResource)).toThrow(/surrogate/)
  })

  it('keeps unknown schema paths opaque and compiles shared recursive metadata only once', () => {
    const types = { Node: { next: { fieldID: 1, type: 'Model', typeName: 'Node' } } }
    const c = new JSONResourceCompiler(types), plan = c.model('Node')
    expect(c.model('Node')).toBe(plan)
    Object.defineProperty(types.Node.next, 'type', { get() { throw new Error('runtime metadata read') } })
    expect(() => assertJSONResourceSource('{"next":null,"unknown":' + generic(2000) + '}', plan)).not.toThrow()
  })

  it('keeps legacy fields without declared types opaque, including typed list wrappers', () => {
    const c = new JSONResourceCompiler({ Legacy: {
      unknown: { fieldID: 1 }, unknowns: { fieldID: 2, isList: true },
    } })
    const plan = c.model('Legacy'), deep = generic(2000)
    expect(() => assertJSONResourceSource('{"unknown":' + deep + ',"unknowns":[' + deep + ']}', plan)).not.toThrow()
    expect(() => assertJSONResourceSource('{"unknown":[0,]}', plan)).toThrow(SyntaxError)
  })

  it('agrees with native JSON grammar on deterministic mutations without constructing values in the validator', () => {
    const bases = ['null', 'true', 'false', '0', '-0.1e+2', '1e400', '"a\\\"b\\\\c\\u0061"',
      '[]', '{}', '[1,true,null,{"x":[0]}]', '{"a":[],"b":{"c":"text"}}']
    const alphabet = '[]{}:,0123456789eE+-."\\ abcdefnulrt\n\t'
    let seed = 0x531c45
    const random = (limit: number): number => {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
      return seed % limit
    }
    const opaqueFields = compiler().parameters([])
    for (let sample = 0; sample < 5000; sample++) {
      let text = bases[random(bases.length)]
      for (let i = 0, count = random(4); i < count; i++) {
        const index = random(text.length + 1), char = alphabet[random(alphabet.length)]
        text = random(2) ? text.slice(0, index) + char + text.slice(index) : text.slice(0, index) + text.slice(index + 1)
      }
      let native = true, source = true
      try { JSON.parse(text) } catch { native = false }
      try { assertJSONResourceSource(text, genericJSONResource) } catch { source = false }
      expect(source, `sample ${sample}: ${JSON.stringify(text)}`).toBe(native)
      let opaque = true
      try { assertJSONResourceSource(text, opaqueFields) } catch { opaque = false }
      expect(opaque, `opaque sample ${sample}: ${JSON.stringify(text)}`).toBe(native)
    }
  })
})
