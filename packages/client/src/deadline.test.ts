import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { scheduleDeadline } from './deadline'

function deadlineHost() {
  let elapsed = 0
  vi.spyOn(performance, 'now').mockImplementation(() => elapsed)
  const timers = vi.spyOn(globalThis, 'setTimeout')
  return {
    at: (value: number) => { elapsed = value },
    latest: () => {
      const callback = timers.mock.calls.at(-1)?.[0]
      if (typeof callback !== 'function') throw new Error('missing timer callback')
      return callback
    },
    delays: () => timers.mock.calls.map(args => args[1]),
  }
}

describe('monotonic deadline timer', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => { vi.clearAllTimers(); vi.restoreAllMocks(); vi.useRealTimers() })

  it('rechecks repeated early deliveries and expires exactly at a fractional budget', () => {
    const host = deadlineHost(), expire = vi.fn()
    scheduleDeadline(1.9, expire)
    expect(host.delays()).toEqual([2])
    for (const elapsed of [0, 1, 1.899]) {
      host.at(elapsed)
      host.latest()()
      expect(expire).not.toHaveBeenCalled()
      expect(vi.getTimerCount()).toBe(1)
    }
    host.at(1.9)
    host.latest()()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each([0, -1, -Number.MAX_VALUE, NaN, Infinity, -Infinity])('keeps invalid/nonpositive %s expiry asynchronous', timeout => {
    const host = deadlineHost(), expire = vi.fn()
    scheduleDeadline(timeout, expire)
    expect(expire).not.toHaveBeenCalled()
    host.latest()()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('bounds enormous finite deadlines without overflow or an early expiry', () => {
    const host = deadlineHost(), expire = vi.fn()
    const cancel = scheduleDeadline(Number.MAX_VALUE, expire)
    host.at(1e12)
    host.latest()()
    expect(host.delays()).toEqual([0x7fffffff, 0x7fffffff])
    expect(expire).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
    cancel()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('preserves the fractional remainder after the largest host delay', () => {
    const host = deadlineHost(), expire = vi.fn()
    scheduleDeadline(0x7fffffff + 0.5, expire)
    host.at(0x7fffffff)
    host.latest()()
    expect(host.delays()).toEqual([0x7fffffff, 1])
    expect(expire).not.toHaveBeenCalled()
    host.at(0x7fffffff + 0.5)
    host.latest()()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores a queued callback after idempotent cancellation', () => {
    const host = deadlineHost(), expire = vi.fn()
    const cancel = scheduleDeadline(10, expire), stale = host.latest()
    cancel()
    cancel()
    host.at(20)
    stale()
    expect(expire).not.toHaveBeenCalled()
    expect(host.delays()).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores a queued callback after expiry even when the action cancels itself', () => {
    const host = deadlineHost()
    let cancel: () => void
    const expire = vi.fn(() => cancel())
    cancel = scheduleDeadline(10, expire)
    const stale = host.latest()
    host.at(10)
    stale()
    stale()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not rearm when host cancellation reenters an early callback and cancels the deadline', () => {
    const host = deadlineHost(), expire = vi.fn()
    const cancel = scheduleDeadline(10, expire), stale = host.latest()
    const clear = globalThis.clearTimeout.bind(globalThis)
    vi.spyOn(globalThis, 'clearTimeout').mockImplementation(timer => { clear(timer); stale(); cancel() })
    host.at(1)
    stale()
    expect(expire).not.toHaveBeenCalled()
    expect(host.delays()).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('claims expiry before host cancellation can reenter its callback', () => {
    const host = deadlineHost(), expire = vi.fn()
    scheduleDeadline(10, expire)
    const stale = host.latest(), clear = globalThis.clearTimeout.bind(globalThis)
    vi.spyOn(globalThis, 'clearTimeout').mockImplementation(timer => { clear(timer); stale() })
    host.at(10)
    stale()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('propagates action exceptions after detaching timer state', () => {
    const host = deadlineHost(), error = new Error('application failure')
    const expire = vi.fn(() => { throw error })
    const cancel = scheduleDeadline(10, expire), stale = host.latest()
    host.at(10)
    expect(stale).toThrow(error)
    expect(stale).not.toThrow()
    cancel()
    expect(expire).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps concurrent deadlines independent', () => {
    const host = deadlineHost(), first = vi.fn(), second = vi.fn()
    const cancel = scheduleDeadline(4, first), early = host.latest()
    scheduleDeadline(8, second)
    const later = host.latest()
    host.at(4)
    early()
    later()
    cancel()
    expect(first).toHaveBeenCalledOnce()
    expect(second).not.toHaveBeenCalled()
    host.at(8)
    host.latest()()
    expect(second).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('uses monotonic time when the wall clock changes', () => {
    const host = deadlineHost(), expire = vi.fn()
    scheduleDeadline(10, expire)
    vi.setSystemTime(new Date('2000-01-01'))
    host.at(9)
    host.latest()()
    expect(expire).not.toHaveBeenCalled()
    vi.setSystemTime(new Date('2100-01-01'))
    host.at(10)
    host.latest()()
    expect(expire).toHaveBeenCalledOnce()
  })
})
