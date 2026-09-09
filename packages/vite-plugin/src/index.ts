import type { Plugin } from 'vite'
import { resolve } from 'node:path'
import { analyzeAndTransform } from './analyzer'
import { generateTypes } from './codegen'
import type { LuxoSchema } from '@luxojs/client'

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
  let generatedDir = resolve(outDir)

  return {
    name: 'luxo',

    configResolved(config) {
      generatedDir = resolve(config.root, outDir)
    },

    async buildStart() {
      // Load schema from endpoint or local file
      schema = await loadSchema(options)
      if (!schema) {
        console.warn('[luxo] No schema available — skipping codegen. Set endpoint + key or schemaFile option.')
        return
      }
      await generateTypes(schema, outDir)
    },

    transform(code: string, id: string) {
      const sourceID = id.split('?', 1)[0]!.replaceAll('\\', '/')
      if (!sourceID.endsWith('.ts') && !sourceID.endsWith('.tsx')) return null
      if (sourceID.includes('/node_modules/')) return null
      if (isInsideDirectory(sourceID, generatedDir)) return null

      if (!schema) return null

      // Analyze field access and inject $select
      const result = analyzeAndTransform(code, id, schema)
      if (!result) return null

      return { code: result, map: null }
    },
  }
}

function isInsideDirectory(file: string, directory: string): boolean {
  const normalizedDirectory = directory.replaceAll('\\', '/').replace(/\/$/, '')
  return file === normalizedDirectory || file.startsWith(`${normalizedDirectory}/`)
}

async function loadSchema(options: LuxoPluginOptions): Promise<LuxoSchema | null> {
  // Try endpoint first
  if (options.endpoint) {
    try {
      const url = `${options.endpoint}?$schema`
      const headers: Record<string, string> = {}
      if (options.introspectionKey) {
        headers['X-Introspection-Key'] = options.introspectionKey
      }
      const resp = await fetch(url, { headers })
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
