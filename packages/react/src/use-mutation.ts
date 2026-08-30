import { useState, useCallback, useEffect, useRef } from 'react'
import { type QueryClient, type QueryKey } from './query-client'
import { useQueryClient } from './provider'

export interface UseLuxoMutationResult<T, V> {
  data: T | null
  loading: boolean
  error: Error | null
  mutate: (variables: V) => Promise<T>
  reset: () => void
}

export interface UseLuxoMutationOptions<T, V> {
  queryClient?: QueryClient
  invalidateQueries?: readonly QueryKey[] | ((data: T, variables: V) => readonly QueryKey[])
  onSuccess?: (data: T, variables: V) => void | Promise<void>
}

/**
 * React hook for Luxo API mutations (create, update, delete, custom).
 *
 * @example
 * ```tsx
 * const { mutate: login, loading } = useLuxoMutation(
 *   (params: { email: string; password: string }) => client.login(params)
 * )
 * const result = await login({ email, password })
 * ```
 */
export function useLuxoMutation<T, V = void>(
  mutationFn: (variables: V) => Promise<T>,
  options: UseLuxoMutationOptions<T, V> = {},
): UseLuxoMutationResult<T, V> {
  const contextClient = useQueryClient()
  const queryClient = options.queryClient ?? contextClient
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const mountedRef = useRef(true)
  const pendingRef = useRef(0)
  const generationRef = useRef(0)
  const mutationFnRef = useRef(mutationFn)
  const optionsRef = useRef(options)
  mutationFnRef.current = mutationFn
  optionsRef.current = options

  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])

  const mutate = useCallback(async (variables: V): Promise<T> => {
    const generation = generationRef.current
    pendingRef.current++
    if (mountedRef.current) {
      setLoading(true)
      setError(null)
    }
    try {
      const result = await mutationFnRef.current(variables)
      const currentOptions = optionsRef.current
      if (currentOptions.onSuccess) await currentOptions.onSuccess(result, variables)
      const invalidations = typeof currentOptions.invalidateQueries === 'function'
        ? currentOptions.invalidateQueries(result, variables)
        : currentOptions.invalidateQueries
      for (const queryKey of invalidations ?? []) queryClient.invalidateQueries({ queryKey })
      if (mountedRef.current && generation === generationRef.current) setData(result)
      return result
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e))
      if (mountedRef.current && generation === generationRef.current) setError(err)
      throw err
    } finally {
      if (generation === generationRef.current) {
        pendingRef.current--
        if (mountedRef.current) setLoading(pendingRef.current > 0)
      }
    }
  }, [queryClient])

  const reset = useCallback(() => {
    generationRef.current++
    pendingRef.current = 0
    setData(null)
    setError(null)
    setLoading(false)
  }, [])

  return { data, loading, error, mutate, reset }
}
