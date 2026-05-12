import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useLuxoQuery } from './use-query'

describe('useLuxoQuery', () => {
  it('starts in loading state', () => {
    const queryFn = vi.fn(() => new Promise<string>(() => {})) // never resolves
    const { result } = renderHook(() => useLuxoQuery(queryFn, []))

    expect(result.current.loading).toBe(true)
    expect(result.current.data).toBeNull()
    expect(result.current.error).toBeNull()
  })

  it('transitions from loading to data state', async () => {
    const queryFn = vi.fn(() => Promise.resolve({ id: 1, name: 'Alice' }))
    const { result } = renderHook(() => useLuxoQuery(queryFn, []))

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.data).toEqual({ id: 1, name: 'Alice' })
    expect(result.current.error).toBeNull()
  })

  it('handles error state', async () => {
    const queryFn = vi.fn(() => Promise.reject(new Error('fetch failed')))
    const { result } = renderHook(() => useLuxoQuery(queryFn, []))

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.data).toBeNull()
    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error!.message).toBe('fetch failed')
  })

  it('handles non-Error rejection', async () => {
    const queryFn = vi.fn(() => Promise.reject('string error'))
    const { result } = renderHook(() => useLuxoQuery(queryFn, []))

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })

    expect(result.current.error).toBeInstanceOf(Error)
    expect(result.current.error!.message).toBe('string error')
  })

  it('refetch re-executes query', async () => {
    let callCount = 0
    const queryFn = vi.fn(() => Promise.resolve({ count: ++callCount }))
    const { result } = renderHook(() => useLuxoQuery(queryFn, []))

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })
    expect(result.current.data).toEqual({ count: 1 })

    await act(async () => {
      await result.current.refetch()
    })

    expect(result.current.data).toEqual({ count: 2 })
    expect(queryFn).toHaveBeenCalledTimes(2)
  })

  it('re-fetches when deps change', async () => {
    let dep = 1
    const queryFn = vi.fn(() => Promise.resolve({ dep }))

    const { result, rerender } = renderHook(
      ({ d }) => useLuxoQuery(() => queryFn(), [d]),
      { initialProps: { d: 1 } },
    )

    await waitFor(() => {
      expect(result.current.loading).toBe(false)
    })
    expect(queryFn).toHaveBeenCalledTimes(1)

    dep = 2
    rerender({ d: 2 })

    await waitFor(() => {
      expect(queryFn).toHaveBeenCalledTimes(2)
    })
  })
})
