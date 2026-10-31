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

/** Generate an SDK without starting Vite; return the loaded schema for field analysis. */
export async function generateSDK(options: LuxoPluginOptions = {}, projectRoot = process.cwd()): Promise<LuxoSchema> {
  const schema = await loadSchema(options, projectRoot)
  await generateTypes(schema, resolve(projectRoot, options.outDir || 'src/luxo'))
  return schema
}

/** Luxo Vite plugin — compile-time field tracking + type generation */
export function luxo(options: LuxoPluginOptions = {}): Plugin {
  let schema: LuxoSchema | null = null
  const outDir = options.outDir || 'src/luxo'
  let projectRoot = process.cwd()
  let generatedDir = resolve(projectRoot, outDir)

  return {
    name: 'luxo',

    configResolved(config) {
      projectRoot = config.root
      generatedDir = resolve(projectRoot, outDir)
    },

    async buildStart() {
      // Load schema from endpoint or local file
      schema = null
      schema = await generateSDK(options, projectRoot)
    },

    transform(code: string, id: string) {
      const sourceID = id.split('?', 1)[0]!.replaceAll('\\', '/')
      if (!/\.(?:[mc]?ts|tsx)$/.test(sourceID)) return null
      if (sourceID.includes('/node_modules/')) return null
      if (isInsideDirectory(sourceID, generatedDir)) return null

      if (!schema) return null

      // Analyze field access and inject $select
      const result = analyzeAndTransform(code, id, schema, true)
      if (!result) return null

      return { code: result, map: null }
    },
  }
}

function isInsideDirectory(file: string, directory: string): boolean {
  const normalizedDirectory = directory.replaceAll('\\', '/').replace(/\/$/, '')
  return file === normalizedDirectory || file.startsWith(`${normalizedDirectory}/`)
}

async function loadSchema(options: LuxoPluginOptions, projectRoot: string): Promise<LuxoSchema> {
  const failures: unknown[] = []
  // Try endpoint first
  if (options.endpoint) {
    try {
      return await fetchSchema(options.endpoint, options.introspectionKey)
    } catch (e) {
      failures.push(e)
    }
  }

  // Try local schema file
  if (options.schema) {
    const path = resolve(projectRoot, options.schema)
    try {
      const fs = await import('fs')
      const schema = decodeSchema(fs.readFileSync(path))
      if (failures.length > 0) console.warn('[luxo] Schema endpoint failed; using the explicitly configured local schema.')
      return schema
    } catch (e) {
      failures.push(new Error(`[luxo] Cannot read schema file ${path}`, { cause: e }))
    }
  }

  throw new AggregateError(failures, '[luxo] No schema available. Configure a working endpoint or schema file.')
}

async function fetchSchema(endpoint: string, key?: string): Promise<LuxoSchema> {
  const url = new URL(endpoint)
  url.searchParams.set('$schema', '')
  url.hash = ''
  const headers: Record<string, string> = {}
  if (key) headers['X-Introspection-Key'] = key
  const response = await fetch(url.toString(), { headers })
  if (!response.ok) throw new Error(`[luxo] Schema endpoint returned HTTP ${response.status}`)
  return decodeSchema(new Uint8Array(await response.arrayBuffer()))
}

/** Decode once, before generation; accept one document BOM, never replacement decoding. */
function decodeSchema(bytes: Uint8Array): LuxoSchema {
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  const value: unknown = JSON.parse(text)
  if (!isRecord(value) || !isRecord(value.models) || !isRecord(value.apis)) {
    throw new TypeError('[luxo] Expected a Schema object with models and apis maps')
  }
  return value as unknown as LuxoSchema
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export default luxo
