// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { QueryClient, type QueryFunctionContext } from './query-client'
import { useLuxoMutation } from './use-mutation'
import { useLuxoQuery } from './use-query'

describe('useLuxoMutation', () => {
  it('preserves int64 mutation inputs, results and invalidation keys', async () => {
    const maximum = 9223372036854775807n
    const queryClient = new QueryClient({ gcTime: Infinity })
    const matching = vi.fn()
    const adjacent = vi.fn()
    const unsubscribeMatching = queryClient.subscribe(['metric', maximum], matching)
    const unsubscribeAdjacent = queryClient.subscribe(['metric', maximum - 1n], adjacent)
    const mutationFn = vi.fn(async (id: bigint) => ({ id, duration: -9223372036854775808n }))
    const { result, unmount } = renderHook(() => useLuxoMutation(mutationFn, {
      queryClient,
      invalidateQueries: data => [['metric', data.id]],
    }))

    await act(async () => {
      expect(await result.current.mutate(maximum)).toEqual({ id: maximum, duration: -9223372036854775808n })
    })
    expect(mutationFn).toHaveBeenCalledWith(maximum)
    expect(result.current.data?.id).toBe(maximum)
    expect(matching).toHaveBeenCalledWith({ type: 'invalidated' })
    expect(adjacent).not.toHaveBeenCalled()
    unmount()
    unsubscribeMatching()
    unsubscribeAdjacent()
    queryClient.clear()
  })

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

  it.each(['static', 'derived'] as const)('keeps the starting project for %s invalidation after a project switch', async kind => {
    type Project = 'A' | 'B'
    const queryClient = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
    const keys = { A: ['tickets', 'A'], B: ['tickets', 'B'] } as const
    const statuses = { A: 'open', B: 'open' }
    const reads = { A: vi.fn(async () => statuses.A), B: vi.fn(async () => statuses.B) }
    const callbacks = { A: vi.fn(), B: vi.fn() }
    for (const project of ['A', 'B'] as const) queryClient.setQueryData(keys[project], statuses[project])

    let finishA!: (status: string) => void
    const writeA = new Promise<string>(resolve => { finishA = resolve })
    const closeTicket = vi.fn(async (project: Project, _id: number) => {
      if (project === 'A') return writeA
      statuses.B = 'closed'
      return statuses.B
    })
    const lists = renderHook(() => ({
      A: useLuxoQuery(reads.A, [], { queryClient, queryKey: keys.A }),
      B: useLuxoQuery(reads.B, [], { queryClient, queryKey: keys.B }),
    }))
    const { result, rerender } = renderHook(({ project }: { project: Project }) => useLuxoMutation(
      (id: number) => closeTicket(project, id),
      {
        queryClient,
        onSuccess: callbacks[project],
        invalidateQueries: kind === 'static' ? [keys[project]] : () => [keys[project]],
      },
    ), { initialProps: { project: 'A' } })

    let closing!: Promise<string>
    act(() => { closing = result.current.mutate(7) })
    expect(closeTicket).toHaveBeenCalledWith('A', 7)
    rerender({ project: 'B' })

    await act(async () => {
      statuses.A = 'closed'
      finishA(statuses.A)
      await expect(closing).resolves.toBe('closed')
    })
    expect({
      callbacks: [callbacks.A.mock.calls, callbacks.B.mock.calls],
      reads: [reads.A.mock.calls.length, reads.B.mock.calls.length],
      data: [lists.result.current.A.data, lists.result.current.B.data],
      cached: [queryClient.getQueryData(keys.A), queryClient.getQueryData(keys.B)],
    }).toEqual({ callbacks: [[['closed', 7]], []], reads: [1, 0], data: ['closed', 'open'], cached: ['closed', 'open'] })

    await act(async () => { await result.current.mutate(8) })
    expect(closeTicket).toHaveBeenCalledWith('B', 8)
    expect(callbacks.B).toHaveBeenCalledExactlyOnceWith('closed', 8)
    expect(reads.A).toHaveBeenCalledOnce()
    expect(reads.B).toHaveBeenCalledOnce()
    expect(queryClient.getQueryData(keys.B)).toBe('closed')
  })

  it.each(['throw', 'reject'] as const)('resolves a saved mutation and refreshes active queries when onSuccess can %s', async kind => {
    const queryClient = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
    const queryKey = ['tickets']
    let tickets = 1
    queryClient.setQueryData(queryKey, tickets)
    const queryFn = vi.fn(async () => tickets)
    const telemetryError = new Error('telemetry unavailable')
    const mutationFn = vi.fn(async () => ++tickets)
    const { result } = renderHook(() => ({
      list: useLuxoQuery(queryFn, [], { queryClient, queryKey }),
      mutation: useLuxoMutation(mutationFn, {
        queryClient,
        onSuccess: () => {
          if (kind === 'throw') throw telemetryError
          return Promise.reject(telemetryError)
        },
        invalidateQueries: [queryKey],
      }),
    }))

    await act(async () => {
      await expect(result.current.mutation.mutate(undefined)).resolves.toBe(2)
    })
    expect(mutationFn).toHaveBeenCalledOnce()
    expect(queryFn).toHaveBeenCalledOnce()
    expect(result.current.list.data).toBe(2)
    expect(queryClient.getQueryData(queryKey)).toBe(2)
    expect(result.current.mutation.data).toBe(2)
    expect(result.current.mutation.error).toBeNull()
    expect(result.current.mutation.postSuccessError).toBe(telemetryError)

    act(() => result.current.mutation.reset())
    expect(result.current.mutation.postSuccessError).toBeNull()
  })

  it('preserves both failures if the callback and deriving invalidation keys fail after a write', async () => {
    const callbackError = new Error('telemetry unavailable')
    const keyError = new Error('no key for response')
    const { result } = renderHook(() => useLuxoMutation(async () => 2, {
      onSuccess: () => { throw callbackError },
      invalidateQueries: () => { throw keyError },
    }))

    await act(async () => {
      await expect(result.current.mutate(undefined)).resolves.toBe(2)
    })
    expect(result.current.error).toBeNull()
    expect(result.current.postSuccessError).toBeInstanceOf(AggregateError)
    expect((result.current.postSuccessError as AggregateError).errors).toEqual([callbackError, keyError])
  })

  it('reports a rejected write as a mutation error without running success handling', async () => {
    const writeError = new Error('write failed')
    const onSuccess = vi.fn()
    const invalidateQueries = vi.fn(() => [['tickets']])
    const { result } = renderHook(() => useLuxoMutation(() => Promise.reject(writeError), {
      onSuccess, invalidateQueries,
    }))

    await act(async () => {
      await expect(result.current.mutate(undefined)).rejects.toBe(writeError)
    })
    expect(result.current.data).toBeNull()
    expect(result.current.error).toBe(writeError)
    expect(result.current.postSuccessError).toBeNull()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(invalidateQueries).not.toHaveBeenCalled()
  })

  it.each([
    { overlapping: false, disabled: false },
    { overlapping: true, disabled: false },
    { overlapping: false, disabled: true },
  ])('shares a new read after mutation success ($overlapping, $disabled)', async ({ overlapping, disabled }) => {
    const queryKey = ['tickets', 'list']
    const queryClient = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
    queryClient.setQueryData(queryKey, 'open')
    const reads: { signal: AbortSignal; resolve: (status: string) => void }[] = []
    const queryFn = vi.fn(({ signal }: QueryFunctionContext) => new Promise<string>(resolve => {
      reads.push({ signal, resolve })
    }))
    const first = renderHook(() => useLuxoQuery(queryFn, [], { queryClient, queryKey, enabled: !disabled }))
    const second = renderHook(() => useLuxoQuery(queryFn, [], { queryClient, queryKey }))
    const mutation = renderHook(() => useLuxoMutation(async () => 'closed', {
      queryClient,
      invalidateQueries: overlapping ? [queryKey, ['tickets'], queryKey] : [['tickets']],
    }))
    expect(first.result.current.data).toBe('open')
    expect(second.result.current.data).toBe('open')
    expect(queryFn).not.toHaveBeenCalled()

    let oldRead!: Promise<void>
    act(() => { oldRead = first.result.current.refetch() })
    expect(queryFn).toHaveBeenCalledTimes(1)

    await act(async () => { await mutation.result.current.mutate(undefined) })
    await act(async () => { reads[1]?.resolve('closed') })
    await act(async () => {
      reads[0].resolve('open')
      await oldRead
    })
    expect({
      signals: reads.map(read => read.signal.aborted),
      cached: queryClient.getQueryData(queryKey),
      first: first.result.current.data,
      second: second.result.current.data,
    }).toEqual({ signals: [true, false], cached: 'closed', first: 'closed', second: 'closed' })
  })

  it('stays loading until every concurrent mutation settles', async () => {
    let resolveFirst!: (value: string) => void
    let resolveSecond!: (value: string) => void
    const first = new Promise<string>(resolve => { resolveFirst = resolve })
    const second = new Promise<string>(resolve => { resolveSecond = resolve })
    const mutationFn = vi.fn<() => Promise<string>>().mockReturnValueOnce(first).mockReturnValueOnce(second)
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
