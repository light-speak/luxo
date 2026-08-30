import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { QueryClient } from './query-client'
import { useLuxoMutation } from './use-mutation'

describe('useLuxoMutation', () => {
  it('never deduplicates mutations and invalidates configured queries after success', async () => {
    const queryClient = new QueryClient({ gcTime: Infinity })
    const listener = vi.fn()
    queryClient.subscribe(['projects'], listener)
    const mutationFn = vi.fn(async (name: string) => ({ name }))
    const { result } = renderHook(() => useLuxoMutation(mutationFn, {
      queryClient,
      invalidateQueries: [['projects']],
    }))

    await act(async () => {
      await Promise.all([result.current.mutate('one'), result.current.mutate('two')])
    })

    expect(mutationFn).toHaveBeenCalledTimes(2)
    expect(listener).toHaveBeenCalledWith({ type: 'invalidated' })
    await waitFor(() => expect(result.current.loading).toBe(false))
  })

  it('runs onSuccess before invalidating derived query keys', async () => {
    const queryClient = new QueryClient({ gcTime: Infinity })
    const events: string[] = []
    queryClient.subscribe(['project', 9], event => events.push(event.type))
    const { result } = renderHook(() => useLuxoMutation(async (id: number) => ({ id }), {
      queryClient,
      onSuccess: async () => { events.push('success') },
      invalidateQueries: (data) => [['project', data.id]],
    }))

    await act(async () => { await result.current.mutate(9) })
    expect(events).toEqual(['success', 'invalidated'])
  })

  it('stays loading until every concurrent mutation settles', async () => {
    let resolveFirst!: (value: string) => void
    let resolveSecond!: (value: string) => void
    const first = new Promise<string>(resolve => { resolveFirst = resolve })
    const second = new Promise<string>(resolve => { resolveSecond = resolve })
    const mutationFn = vi.fn().mockReturnValueOnce(first).mockReturnValueOnce(second)
    const { result } = renderHook(() => useLuxoMutation(mutationFn))

    let firstCall!: Promise<string>
    let secondCall!: Promise<string>
    act(() => {
      firstCall = result.current.mutate(undefined)
      secondCall = result.current.mutate(undefined)
    })
    resolveFirst('one')
    await act(async () => { await firstCall })
    expect(result.current.loading).toBe(true)
    resolveSecond('two')
    await act(async () => { await secondCall })
    expect(result.current.loading).toBe(false)
  })

  it('reset prevents an older mutation from restoring cleared state', async () => {
    let resolve!: (value: string) => void
    const pending = new Promise<string>(done => { resolve = done })
    const { result } = renderHook(() => useLuxoMutation(() => pending))
    let call!: Promise<string>
    act(() => { call = result.current.mutate(undefined) })
    act(() => result.current.reset())

    resolve('late')
    await act(async () => { await call })
    expect(result.current.data).toBeNull()
    expect(result.current.error).toBeNull()
    expect(result.current.loading).toBe(false)
  })
})
