import { useState, useCallback, useEffect, useRef } from 'react'
import { type QueryClient, type QueryKey } from './query-client'
import { useQueryClient } from './provider'

export interface UseLuxoMutationResult<T, V> {
  data: T | null
  loading: boolean
  error: Error | null
  postSuccessError: Error | null
  mutate: (variables: V) => Promise<T>
  reset: () => void
}

export interface UseLuxoMutationOptions<T, V> {
  queryClient?: QueryClient
  invalidateQueries?: readonly QueryKey[] | ((data: T, variables: V) => readonly QueryKey[])
  onSuccess?: (data: T, variables: V) => void | Promise<void>
}

function normalizeMutationError(error: unknown): Error {
  try {
    return error instanceof Error ? error : new Error(String(error), { cause: error })
  } catch {
    return new Error('Unknown mutation error', { cause: error })
  }
}

function invalidateMutationQueries<T, V>(
  queryClient: QueryClient, options: UseLuxoMutationOptions<T, V>, result: T, variables: V,
): void {
  const invalidations = typeof options.invalidateQueries === 'function'
    ? options.invalidateQueries(result, variables)
    : options.invalidateQueries
  if (invalidations?.length === 1) {
    queryClient.invalidateQueries({ queryKey: invalidations[0] })
  } else if (invalidations?.length) {
    queryClient.invalidateQueries(invalidations.map(queryKey => ({ queryKey })))
  }
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
  const [postSuccessError, setPostSuccessError] = useState<Error | null>(null)
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
    const mutationOptions = optionsRef.current
    const generation = generationRef.current
    pendingRef.current++
    if (mountedRef.current) {
      setLoading(true)
      setError(null)
      setPostSuccessError(null)
    }
    try {
      let result: T
      try {
        result = await mutationFnRef.current(variables)
      } catch (failure) {
        const err = normalizeMutationError(failure)
        if (mountedRef.current && generation === generationRef.current) setError(err)
        throw err
      }

      let followupError: Error | undefined
      try {
        if (mutationOptions.onSuccess) await mutationOptions.onSuccess(result, variables)
      } catch (failure) {
        followupError = normalizeMutationError(failure)
      }
      try {
        invalidateMutationQueries(queryClient, mutationOptions, result, variables)
      } catch (failure) {
        const err = normalizeMutationError(failure)
        followupError = followupError
          ? new AggregateError([followupError, err], 'Mutation success handling failed')
          : err
      }
      if (mountedRef.current && generation === generationRef.current) {
        setData(result)
        if (followupError) setPostSuccessError(followupError)
      }
      return result
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
    setPostSuccessError(null)
    setLoading(false)
  }, [])

  return { data, loading, error, postSuccessError, mutate, reset }
}
