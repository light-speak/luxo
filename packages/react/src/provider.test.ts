import { renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { QueryClient } from './query-client'
import { QueryClientProvider, useQueryClient } from './provider'

describe('QueryClientProvider', () => {
  it('provides the configured client', () => {
    const client = new QueryClient()
    const wrapper = ({ children }: { children: ReactNode }) => {
      return createElement(QueryClientProvider, { client }, children)
    }
    const { result } = renderHook(() => useQueryClient(), { wrapper })
    expect(result.current).toBe(client)
  })
})
