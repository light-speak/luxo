import { useState, useEffect, useCallback, useRef } from 'react'

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
 * const { data: user, loading } = useLuxoQuery(() => client.getUser(1), [1])
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
  const mountedRef = useRef(true)
  const versionRef = useRef(0)

  const execute = useCallback(async () => {
    const version = ++versionRef.current
    setLoading(true)
    setError(null)
    try {
      const result = await queryFn()
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  useEffect(() => {
    mountedRef.current = true
    execute()
    return () => {
      mountedRef.current = false
    }
  }, [execute])

  return { data, loading, error, refetch: execute }
}
