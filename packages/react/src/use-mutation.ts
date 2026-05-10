import { useState, useCallback } from 'react'

export interface UseLuxoMutationResult<T, V> {
  data: T | null
  loading: boolean
  error: Error | null
  mutate: (variables: V) => Promise<T>
  reset: () => void
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
): UseLuxoMutationResult<T, V> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const mutate = useCallback(async (variables: V): Promise<T> => {
    setLoading(true)
    setError(null)
    try {
      const result = await mutationFn(variables)
      setData(result)
      return result
    } catch (e) {
      const err = e instanceof Error ? e : new Error(String(e))
      setError(err)
      throw err
    } finally {
      setLoading(false)
    }
  }, [mutationFn])

  const reset = useCallback(() => {
    setData(null)
    setError(null)
    setLoading(false)
  }, [])

  return { data, loading, error, mutate, reset }
}
