import type { Plugin } from 'vite'
import { analyzeAndTransform } from './analyzer'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxo/client'

export interface LuxoPluginOptions {
  /** Luvia endpoint URL for schema introspection */
  endpoint?: string
  /** Introspection key */
  introspectionKey?: string
  /** Path to local schema file (alternative to endpoint) */
  schema?: string
  /** Output directory for generated types (default: src/luxo) */
  outDir?: string
}

/** Luxo Vite plugin — compile-time field tracking + type generation */
export function luxo(options: LuxoPluginOptions = {}): Plugin {
  let schema: LuxoSchema | null = null
  const outDir = options.outDir || 'src/luxo'

  return {
    name: 'luxo',

    async buildStart() {
      // Load schema from endpoint or local file
      schema = await loadSchema(options)
      if (schema) {
        // Generate TypeScript types + client from schema
        await generateTypes(schema, outDir)
      }
    },

    transform(code: string, id: string) {
      // Only process .ts and .tsx files
      if (!id.endsWith('.ts') && !id.endsWith('.tsx')) return null
      if (id.includes('node_modules')) return null
      if (id.includes('/luxo/')) return null // skip generated files

      if (!schema) return null

      // Analyze field access and inject $select
      const result = analyzeAndTransform(code, id, schema)
      if (!result) return null

      return { code: result, map: null }
    },
  }
}

async function loadSchema(options: LuxoPluginOptions): Promise<LuxoSchema | null> {
  // Try endpoint first
  if (options.endpoint) {
    try {
      let url = `${options.endpoint}?$schema`
      if (options.introspectionKey) {
        url += `&key=${options.introspectionKey}`
      }
      const resp = await fetch(url)
      if (resp.ok) {
        return await resp.json() as LuxoSchema
      }
      console.warn(`[luxo] Failed to fetch schema: ${resp.status}`)
    } catch (e) {
      console.warn(`[luxo] Failed to connect to ${options.endpoint}:`, e)
    }
  }

  // Try local schema file
  if (options.schema) {
    try {
      const fs = await import('fs')
      const data = fs.readFileSync(options.schema, 'utf-8')
      return JSON.parse(data) as LuxoSchema
    } catch (e) {
      console.warn(`[luxo] Failed to read schema file:`, e)
    }
  }

  console.warn('[luxo] No schema available. Set endpoint or schema option.')
  return null
}

export default luxo
