import { useState, useEffect, useCallback } from 'react'

export interface UseLuxoQueryResult<T> {
  data: T | null
  loading: boolean
  error: Error | null
  refetch: () => Promise<void>
}

/**
 * React hook for Luxo API queries.
 * Works with @luxo/vite-plugin for compile-time $select injection.
 *
 * @example
 * ```tsx
 * const { data: user, loading } = useLuxoQuery(() => client.getUser(1))
 * // With vite plugin: automatically injects $select based on field usage
 * return <div>{user?.name}</div>
 * ```
 */
export function useLuxoQuery<T>(
  queryFn: () => Promise<T>,
  deps: unknown[] = [],
): UseLuxoQueryResult<T> {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<Error | null>(null)

  const execute = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const result = await queryFn()
      setData(result)
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setLoading(false)
    }
  }, deps)

  useEffect(() => {
    execute()
  }, [execute])

  return { data, loading, error, refetch: execute }
}
