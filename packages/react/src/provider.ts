import { createContext, useContext, createElement, type ReactNode } from 'react'
import type { Transport } from '@luxojs/client'

const LuxoContext = createContext<Transport | null>(null)

/** Provide Luxo transport to the component tree */
export function LuxoProvider({ transport, children }: { transport: Transport; children: ReactNode }) {
  return createElement(LuxoContext.Provider, { value: transport }, children)
}

/** Get Luxo transport from context */
export function useLuxoClient(): Transport {
  const ctx = useContext(LuxoContext)
  if (!ctx) throw new Error('useLuxoClient must be used within <LuxoProvider>')
  return ctx
}
