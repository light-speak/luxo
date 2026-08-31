import { useState, useEffect, useCallback, useRef } from 'react'
import { hashQueryKey, type QueryClient, type QueryFunction, type QueryKey } from './query-client'
import { useQueryClient } from './provider'

export interface UseLuxoQueryResult<T> {
  data: T | null
  loading: boolean
  error: Error | null
  refetch: () => Promise<void>
}

export interface UseLuxoQueryOptions {
  queryKey?: QueryKey
  queryClient?: QueryClient
  enabled?: boolean
  staleTime?: number
  gcTime?: number
}

let nextHookID = 0

function dependenciesEqual(previous: readonly unknown[], next: readonly unknown[]): boolean {
  return previous.length === next.length && previous.every((value, index) => Object.is(value, next[index]))
}

/**
 * React hook for Luxo API queries.
 * Works with @luxo/vite-plugin for compile-time $select injection.
 *
 * @example
 * ```tsx
 * const { data: user, loading } = useLuxoQuery(() => client.getUser(1), [1])
 * return <div>{user?.name}</div>
 * ```
 */
export function useLuxoQuery<T>(
  queryFn: QueryFunction<T>,
  deps: unknown[] = [],
  options: UseLuxoQueryOptions = {},
): UseLuxoQueryResult<T> {
  const contextClient = useQueryClient()
  const queryClient = options.queryClient ?? contextClient
  const enabled = options.enabled ?? true
  const hookID = useRef(0)
  if (hookID.current === 0) hookID.current = ++nextHookID
  const dependencyState = useRef({ values: deps as readonly unknown[], generation: 0 })
  if (!dependenciesEqual(dependencyState.current.values, deps)) {
    dependencyState.current = { values: deps, generation: dependencyState.current.generation + 1 }
  }
  const queryKey = options.queryKey ?? ['@luxo/hook', hookID.current, dependencyState.current.generation]
  const queryHash = hashQueryKey(queryKey)
  const initialState = queryClient.getQueryState<T>(queryKey)
  const [data, setData] = useState<T | null>(initialState.hasData ? initialState.data ?? null : null)
  const [loading, setLoading] = useState(enabled && !initialState.hasData)
  const [error, setError] = useState<Error | null>(initialState.error ?? null)
  const mountedRef = useRef(false)
  const versionRef = useRef(0)
  const queryFnRef = useRef(queryFn)
  const queryKeyRef = useRef(queryKey)
  queryFnRef.current = queryFn
  queryKeyRef.current = queryKey

  const execute = useCallback(async (force = false) => {
    const version = ++versionRef.current
    setLoading(true)
    setError(null)
    try {
      const result = await queryClient.fetchQuery({
        queryKey: queryKeyRef.current,
        queryFn: context => queryFnRef.current(context),
        staleTime: options.staleTime,
        gcTime: options.gcTime,
        force,
      })
      // Only update if still mounted and this is the latest request
      if (mountedRef.current && version === versionRef.current) {
        setData(result)
      }
    } catch (e) {
      if (mountedRef.current && version === versionRef.current) {
        setError(e instanceof Error ? e : new Error(String(e)))
      }
    } finally {
      if (mountedRef.current && version === versionRef.current) {
        setLoading(false)
      }
    }
  }, [queryClient, queryHash, options.staleTime, options.gcTime])

  useEffect(() => {
    mountedRef.current = true
    const syncState = () => {
      const state = queryClient.getQueryState<T>(queryKeyRef.current)
      setData(state.hasData ? state.data ?? null : null)
      setError(state.error ?? null)
      setLoading(state.isFetching)
    }
    const unsubscribe = queryClient.subscribe(queryKeyRef.current, event => {
      if (!mountedRef.current) return
      if (event.type === 'invalidated' && enabled) {
        void execute()
        return
      }
      if (
        event.type === 'updated'
        || event.type === 'error'
        || event.type === 'cancelled'
        || event.type === 'removed'
      ) syncState()
    })
    if (enabled) {
      void execute()
    } else {
      syncState()
      setLoading(false)
    }
    return () => {
      mountedRef.current = false
      versionRef.current++
      unsubscribe()
    }
  }, [enabled, execute, queryClient, queryHash])

  const refetch = useCallback(() => execute(true), [execute])
  return { data, loading, error, refetch }
}
