import { createContext, useContext, createElement, useRef, type ReactNode } from 'react'
import type { Transport } from '@luxojs/client'
import { QueryClient } from './query-client'

const LuxoContext = createContext<Transport | null>(null)
const defaultQueryClient = new QueryClient()
const QueryClientContext = createContext<QueryClient>(defaultQueryClient)

/** Provide Luxo transport to the component tree */
export function LuxoProvider({
  transport,
  queryClient,
  children,
}: {
  transport: Transport
  queryClient?: QueryClient
  children: ReactNode
}) {
  const localClient = useRef<QueryClient | null>(null)
  if (localClient.current === null) localClient.current = new QueryClient()
  return createElement(
    QueryClientContext.Provider,
    { value: queryClient ?? localClient.current },
    createElement(LuxoContext.Provider, { value: transport }, children),
  )
}

/** Provide a QueryClient without requiring a Luxo transport context. */
export function QueryClientProvider({ client, children }: { client: QueryClient; children: ReactNode }) {
  return createElement(QueryClientContext.Provider, { value: client }, children)
}

/** Get Luxo transport from context */
export function useLuxoClient(): Transport {
  const ctx = useContext(LuxoContext)
  if (!ctx) throw new Error('useLuxoClient must be used within <LuxoProvider>')
  return ctx
}

/** Get the nearest QueryClient, or the browser-local default client. */
export function useQueryClient(): QueryClient {
  return useContext(QueryClientContext)
}
