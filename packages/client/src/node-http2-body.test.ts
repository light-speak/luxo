import { expect, it } from 'vitest'
import { HTTP2Body } from './node-http2-body'

it('owns exact bytes across chunk growth and does not retain caller buffers', () => {
  const body = new HTTP2Body(10)
  const first = Uint8Array.of(1, 2)
  body.append(first)
  first.fill(9)
  body.append(Uint8Array.of(3))
  body.append(Uint8Array.of(4, 5))
  expect(new Uint8Array(body.finish())).toEqual(Uint8Array.of(1, 2, 3, 4, 5))
})

it('validates declared length, the hard limit and empty bodies', () => {
  expect(new HTTP2Body(10, 0).finish().byteLength).toBe(0)
  expect(new HTTP2Body(10).finish().byteLength).toBe(0)
  expect(() => new HTTP2Body(1).append(Uint8Array.of(1, 2))).toThrow()
  expect(() => new HTTP2Body(10, 1).append(Uint8Array.of(1, 2))).toThrow()
  const body = new HTTP2Body(10, 2)
  body.append(new Uint8Array(0))
  body.append(Uint8Array.of(1))
  expect(() => body.finish()).toThrow()
  body.append(Uint8Array.of(2))
  expect(new Uint8Array(body.finish())).toEqual(Uint8Array.of(1, 2))
})
