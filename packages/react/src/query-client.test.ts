import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient, type QueryFunctionContext } from './query-client'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('QueryClient', () => {
  it('deduplicates concurrent requests with the same structural key', async () => {
    const client = new QueryClient()
    const result = deferred<string>()
    const queryFn = vi.fn(() => result.promise)
    const first = client.fetchQuery({ queryKey: ['user', { id: 1, role: 'owner' }], queryFn })
    const second = client.fetchQuery({ queryKey: ['user', { role: 'owner', id: 1 }], queryFn })

    expect(first).toBe(second)
    expect(queryFn).toHaveBeenCalledOnce()
    result.resolve('ready')
    await expect(first).resolves.toBe('ready')
    await expect(second).resolves.toBe('ready')
  })

  it('does not reuse completed data with the zero-cache defaults', async () => {
    const client = new QueryClient()
    const queryFn = vi.fn().mockResolvedValueOnce(1).mockResolvedValueOnce(2)

    await expect(client.fetchQuery({ queryKey: ['counter'], queryFn })).resolves.toBe(1)
    await expect(client.fetchQuery({ queryKey: ['counter'], queryFn })).resolves.toBe(2)
    expect(queryFn).toHaveBeenCalledTimes(2)
  })

  it('reuses fresh data and garbage-collects it after gcTime', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(1000)
    const client = new QueryClient()
    const queryFn = vi.fn().mockResolvedValue({ id: 1 })
    const options = { queryKey: ['user', 1], queryFn, staleTime: 1000, gcTime: 2000 }

    await client.fetchQuery(options)
    vi.setSystemTime(1500)
    await client.fetchQuery(options)
    expect(queryFn).toHaveBeenCalledOnce()
    expect(client.getQueryData(['user', 1])).toEqual({ id: 1 })

    await vi.advanceTimersByTimeAsync(2000)
    expect(client.getQueryData(['user', 1])).toBeUndefined()
  })

  it('invalidates exact keys and structural prefixes', async () => {
    const client = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
    const userOne = vi.fn().mockResolvedValueOnce('one').mockResolvedValueOnce('one-new')
    const userTwo = vi.fn().mockResolvedValue('two')
    await client.fetchQuery({ queryKey: ['user', 1], queryFn: userOne })
    await client.fetchQuery({ queryKey: ['user', 2], queryFn: userTwo })

    client.invalidateQueries({ queryKey: ['user', 1], exact: true })
    await expect(client.fetchQuery({ queryKey: ['user', 1], queryFn: userOne })).resolves.toBe('one-new')
    await client.fetchQuery({ queryKey: ['user', 2], queryFn: userTwo })
    expect(userOne).toHaveBeenCalledTimes(2)
    expect(userTwo).toHaveBeenCalledOnce()

    client.invalidateQueries({ queryKey: ['user'] })
    await client.fetchQuery({ queryKey: ['user', 2], queryFn: userTwo })
    expect(userTwo).toHaveBeenCalledTimes(2)
  })

  it('updates cached data and notifies subscribers', () => {
    const client = new QueryClient({ gcTime: Infinity })
    const listener = vi.fn()
    const unsubscribe = client.subscribe(['project', 7], listener)

    client.setQueryData(['project', 7], { name: 'Luxo' })
    expect(client.getQueryData(['project', 7])).toEqual({ name: 'Luxo' })
    expect(listener).toHaveBeenCalledWith({ type: 'updated' })

    client.setQueryData<{ name: string }>(['project', 7], current => ({ name: `${current?.name} Studio` }))
    expect(client.getQueryData(['project', 7])).toEqual({ name: 'Luxo Studio' })
    unsubscribe()
  })

  it('aborts matching requests and prevents stale completion from replacing new data', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    const oldResult = deferred<string>()
    const newResult = deferred<string>()
    let oldContext: QueryFunctionContext | undefined
    const oldQuery = client.fetchQuery({
      queryKey: ['status'],
      queryFn: (context) => {
        oldContext = context
        return oldResult.promise
      },
    })

    client.cancelQueries({ queryKey: ['status'], exact: true })
    expect(oldContext?.signal.aborted).toBe(true)
    const newQuery = client.fetchQuery({ queryKey: ['status'], queryFn: () => newResult.promise })
    newResult.resolve('new')
    await expect(newQuery).resolves.toBe('new')
    oldResult.resolve('old')
    await expect(oldQuery).resolves.toBe('old')
    expect(client.getQueryData(['status'])).toBe('new')
  })

  it('retries after a rejected request instead of caching the error', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    const queryFn = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce('online')

    await expect(client.fetchQuery({ queryKey: ['health'], queryFn })).rejects.toThrow('offline')
    await expect(client.fetchQuery({ queryKey: ['health'], queryFn })).resolves.toBe('online')
    expect(queryFn).toHaveBeenCalledTimes(2)
  })

  it('rejects unsafe keys and invalid cache durations', () => {
    const client = new QueryClient()
    expect(() => client.getQueryData(['bad', () => true])).toThrow('unsupported query key value')
    expect(() => new QueryClient({ gcTime: -1 })).toThrow('gcTime must be zero or greater')
    expect(() => client.fetchQuery({
      queryKey: ['bad-duration'],
      queryFn: async () => true,
      staleTime: Number.NaN,
    })).toThrow('staleTime must be zero or greater')
  })

  it('swallows prefetch failures while retaining the error state', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    await expect(client.prefetchQuery({
      queryKey: ['prefetch'],
      queryFn: async () => { throw new Error('offline') },
    })).resolves.toBeUndefined()
    expect(client.getQueryState(['prefetch']).error?.message).toBe('offline')
  })

  it('keeps active subscriptions attached when cached data is removed', async () => {
    const client = new QueryClient({ gcTime: Infinity })
    const listener = vi.fn()
    client.subscribe(['active'], listener)
    client.setQueryData(['active'], 'old')
    listener.mockClear()

    client.removeQueries({ queryKey: ['active'], exact: true })
    expect(client.getQueryData(['active'])).toBeUndefined()
    await client.fetchQuery({ queryKey: ['active'], queryFn: async () => 'new' })

    expect(listener).toHaveBeenCalledWith({ type: 'removed' })
    expect(listener).toHaveBeenCalledWith({ type: 'updated' })
    expect(client.getQueryData(['active'])).toBe('new')
  })
})
