import { describe, expect, it } from 'vitest'
import { HTTPHeaders } from './http-headers'

describe('HTTP header snapshots', () => {
  it('shares immutable defaults without sharing later authentication updates', () => {
    const first = new HTTPHeaders(), second = new HTTPHeaders()
    expect(first.json).toBe(second.json)
    expect(first.binary).toBe(second.binary)
    expect(Object.isFrozen(first.json)).toBe(true)
    expect(Object.isFrozen(first.binary)).toBe(true)
    first.setToken('one')
    expect(second.json.authorization).toBeUndefined()
    expect(second.binary.authorization).toBeUndefined()
    expect(first.json.authorization).toBe('Bearer one')
    expect(first.binary.authorization).toBe('Bearer one')
  })

  it('normalizes custom headers once and keeps protocol metadata authoritative', () => {
    const custom = { 'X-Region': '  test  ', 'Content-TYPE': 'ignored', 'X-LUXO-MODE': 'ignored' }
    const headers = new HTTPHeaders(custom)
    custom['X-Region'] = 'changed'
    expect(headers.json).toEqual({ 'x-region': 'test', 'content-type': 'application/json', 'x-luxo-mode': 'json' })
    expect(headers.binary).toEqual({ 'x-region': 'test', 'content-type': 'application/x-luxo', 'x-luxo-mode': 'binary' })
  })

  it('preserves explicit authorization until token replacement or logout', () => {
    const headers = new HTTPHeaders({ AUTHORIZATION: 'Basic custom' }, '')
    const original = headers.json
    expect(original.authorization).toBe('Basic custom')
    for (const token of ['updated', null, 'again', '']) {
      headers.setToken(token)
      expect(headers.json.authorization).toBe(token ? `Bearer ${token}` : undefined)
      expect(headers.binary.authorization).toBe(token ? `Bearer ${token}` : undefined)
      expect(Object.isFrozen(headers.json)).toBe(true)
      expect(Object.isFrozen(headers.binary)).toBe(true)
    }
    expect(original.authorization).toBe('Basic custom')
    expect(new HTTPHeaders(undefined, 'initial').json.authorization).toBe('Bearer initial')
  })

  it('uses the platform header validation before dispatch', () => {
    expect(() => new HTTPHeaders({ 'bad header': 'value' })).toThrow(TypeError)
    expect(() => new HTTPHeaders({ 'X-Region': 'value\r\ninjected: value' })).toThrow(TypeError)
  })
})
