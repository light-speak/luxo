import { describe, expect, it } from 'vitest'
import { UnknownEnumValue, enumRawValue } from './enum'
import { Encoder } from './codec'
import { encodeParam } from './transport'
import { JSONParamValidator } from './json-params'

describe('unknown enum wire values', () => {
  it('keeps a frozen, explicitly tagged raw value without wrapping known strings', () => {
    const value = new UnknownEnumValue('State', 'FUTURE')
    expect(value).toMatchObject({ kind: 'unknown', enumName: 'State', rawValue: 'FUTURE' })
    expect(Object.isFrozen(value)).toBe(true)
    expect(enumRawValue(value, 'State')).toBe('FUTURE')
    expect(enumRawValue('OPEN', 'State')).toBe('OPEN')
    expect(() => enumRawValue(value, 'Role')).toThrow()
    for (const invalid of [null, 7, {}, { rawValue: 'FUTURE' }]) {
      expect(() => enumRawValue(invalid, 'State')).toThrow()
    }
  })

  it('encodes known and unknown enum values as strings in Binary scalars and lists', () => {
    for (const list of [false, true]) {
      const actual = new Encoder(), expected = new Encoder()
      const value = new UnknownEnumValue('State', 'FUTURE')
      encodeParam(actual, { fieldID: 1, type: 'Enum', typeName: 'State', isList: list }, list ? ['OPEN', value] : value)
      encodeParam(expected, { fieldID: 1, type: 'Enum', typeName: 'State', isList: list }, list ? ['OPEN', 'FUTURE'] : 'FUTURE')
      expect(actual.bytes()).toEqual(expected.bytes())
    }
  })

  it('preserves Binary structured arena sizes for unknown enum text', () => {
    const types = { Input: {
      state: { fieldID: 1, type: 'Enum', typeName: 'State' },
      states: { fieldID: 2, type: 'Enum', typeName: 'State', isList: true },
      child: { fieldID: 3, type: 'Model', typeName: 'Input', nullable: true },
    } }
    const metadata = { fieldID: 1, type: 'Model', typeName: 'Input' }
    for (const raw of ['FUTURE', '未来', '']) {
      const actual = new Encoder(), expected = new Encoder()
      const value = new UnknownEnumValue('State', raw)
      const child = { state: value, states: ['OPEN', value], child: null }
      encodeParam(actual, metadata, { ...child, child }, types)
      const rawChild = { state: raw, states: ['OPEN', raw], child: null }
      encodeParam(expected, metadata, { ...rawChild, child: rawChild }, types)
      expect(actual.bytes()).toEqual(expected.bytes())
    }
  })

  it('projects only declared JSON enum fields and shares unchanged known values', () => {
    const validator = new JSONParamValidator()
    validator.setSchema({ echo: { id: 1, params: [
      { name: 'input', fieldID: 1, type: 'Model', typeName: 'Input' },
    ], types: { Input: {
      state: { fieldID: 1, type: 'Enum', typeName: 'State', nullable: true },
      states: { fieldID: 2, type: 'Enum', typeName: 'State', isList: true },
      json: { fieldID: 3, type: 'JSON' },
    } } } })
    const value = new UnknownEnumValue('State', 'FUTURE')
    const json = { rawValue: 'not an enum' }
    const input = Object.freeze({ state: value, states: Object.freeze(['OPEN', value]), json })
    expect(validator.prepare('echo', { input })).toEqual({ input: { state: 'FUTURE', states: ['OPEN', 'FUTURE'], json } })
    expect(input.state).toBe(value)
    const known = { input: { state: 'OPEN', states: ['OPEN'], json } }
    expect(validator.prepare('echo', known)).toBe(known)
    const empty = { input: { state: null, states: [] } }
    expect(validator.prepare('echo', empty)).toBe(empty)
    expect(() => validator.prepare('echo', { input: { state: new UnknownEnumValue('Role', 'FUTURE') } })).toThrow()
  })
})
