import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { createElement, StrictMode, type ReactNode } from 'react'
import { QueryClient } from './query-client'
import { useLuxoQuery } from './use-query'

describe('useLuxoQuery', () => {
  it('deduplicates React StrictMode effect replay', async () => {
    const queryFn = vi.fn(() => Promise.resolve('ready'))
    const wrapper = ({ children }: { children: ReactNode }) => createElement(StrictMode, null, children)
    const { result } = renderHook(() => useLuxoQuery(queryFn, []), { wrapper })

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.data).toBe('ready')
    expect(queryFn).toHaveBeenCalledOnce()
  })

  it('deduplicates explicit query keys across hook consumers', async () => {
    const client = new QueryClient()
    const queryFn = vi.fn(() => Promise.resolve('shared'))
    const { result } = renderHook(() => {
      const first = useLuxoQuery(queryFn, [], { queryKey: ['shared'], queryClient: client })
      const second = useLuxoQuery(queryFn, [], { queryKey: ['shared'], queryClient: client })
      return { first, second }
    })

    await waitFor(() => expect(result.current.second.loading).toBe(false))
    expect(result.current.first.data).toBe('shared')
    expect(result.current.second.data).toBe('shared')
    expect(queryFn).toHaveBeenCalledOnce()
  })

  it('refetches active queries after prefix invalidation', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    const queryFn = vi.fn().mockResolvedValueOnce('old').mockResolvedValueOnce('new')
    const { result } = renderHook(() => useLuxoQuery(queryFn, [], {
      queryKey: ['project', 1],
      queryClient: client,
      staleTime: Infinity,
    }))
    await waitFor(() => expect(result.current.data).toBe('old'))

    act(() => client.invalidateQueries({ queryKey: ['project'] }))
    await waitFor(() => expect(result.current.data).toBe('new'))
    expect(queryFn).toHaveBeenCalledTimes(2)
  })

  it('does not execute disabled queries', () => {
    const queryFn = vi.fn(() => Promise.resolve('never'))
    const { result } = renderHook(() => useLuxoQuery(queryFn, [], { enabled: false }))

    expect(result.current.loading).toBe(false)
    expect(queryFn).not.toHaveBeenCalled()
  })

  it('aborts an unobserved request after a real unmount', async () => {
    let signal: AbortSignal | undefined
    const { unmount } = renderHook(() => useLuxoQuery(context => {
      signal = context.signal
      return new Promise<string>(() => {})
    }, []))

    expect(signal?.aborted).toBe(false)
    unmount()
    await act(async () => { await Promise.resolve() })
    expect(signal?.aborted).toBe(true)
  })

  it('leaves loading state after external cancellation', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    const { result } = renderHook(() => useLuxoQuery(
      () => new Promise<string>(() => {}),
      [],
      { queryKey: ['cancelled'], queryClient: client },
    ))
    expect(result.current.loading).toBe(true)

    act(() => client.cancelQueries({ queryKey: ['cancelled'], exact: true }))
    await waitFor(() => expect(result.current.loading).toBe(false))
  })

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
