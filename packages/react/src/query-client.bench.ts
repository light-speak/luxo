import { bench, describe } from 'vitest'
import { QueryClient } from './query-client'

describe('QueryClient hot path', () => {
  const client = new QueryClient({ staleTime: Infinity, gcTime: Infinity })
  const queryKey = ['project', 1, { range: '1h' }] as const
  client.setQueryData(queryKey, { requests: 42 })

  bench('read cached query data', () => {
    client.getQueryData(queryKey)
  })

  bench('fetch fresh cached query', async () => {
    await client.fetchQuery({ queryKey, queryFn: async () => ({ requests: 0 }) })
  })
})
