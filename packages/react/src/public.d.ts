import type { Transport } from '@luxojs/client'
import type { ReactElement, ReactNode } from 'react'

export type QueryKey = readonly unknown[]

export interface QueryFunctionContext {
  signal: AbortSignal
}

export type QueryFunction<T> = (context: QueryFunctionContext) => Promise<T>

export interface QueryClientDefaults {
  staleTime?: number
  gcTime?: number
}

export interface FetchQueryOptions<T> {
  queryKey: QueryKey
  queryFn: QueryFunction<T>
  staleTime?: number
  gcTime?: number
  force?: boolean
}

export interface QueryFilters {
  queryKey?: QueryKey
  exact?: boolean
}

export interface QueryEvent {
  type: 'updated' | 'error' | 'invalidated' | 'cancelled' | 'removed'
}

export interface QueryState<T> {
  data: T | undefined
  error: Error | undefined
  hasData: boolean
  isFetching: boolean
  isInvalidated: boolean
}

export type QueryUpdater<T> = T | ((current: T | undefined) => T)

export class QueryClient {
  constructor(defaults?: QueryClientDefaults)
  fetchQuery<T>(options: FetchQueryOptions<T>): Promise<T>
  prefetchQuery<T>(options: FetchQueryOptions<T>): Promise<void>
  getQueryData<T>(queryKey: QueryKey): T | undefined
  getQueryState<T>(queryKey: QueryKey): QueryState<T>
  setQueryData<T>(queryKey: QueryKey, updater: QueryUpdater<T>): T
  subscribe(queryKey: QueryKey, listener: (event: QueryEvent) => void): () => void
  invalidateQueries(filters?: QueryFilters): void
  cancelQueries(filters?: QueryFilters): void
  removeQueries(filters?: QueryFilters): void
  clear(): void
}

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

export function useLuxoQuery<T>(
  queryFn: QueryFunction<T>,
  deps?: unknown[],
  options?: UseLuxoQueryOptions,
): UseLuxoQueryResult<T>
export function useLuxoMutation<T, V = void>(
  mutationFn: (variables: V) => Promise<T>,
  options?: UseLuxoMutationOptions<T, V>,
): UseLuxoMutationResult<T, V>
export function LuxoProvider(props: {
  transport: Transport
  queryClient?: QueryClient
  children: ReactNode
}): ReactElement
export function QueryClientProvider(props: { client: QueryClient; children: ReactNode }): ReactElement
export function useLuxoClient(): Transport
export function useQueryClient(): QueryClient
