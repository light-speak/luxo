import type { HmrContext, ModuleNode, Plugin, ViteDevServer } from 'vite'
import { resolve } from 'node:path'
import MagicString from 'magic-string'
import { analyzeVerifiedEdits, type TextEdit } from './analyzer'
import { BuildOwner, CallIdentity } from './call-identity'
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
  const build = new PluginBuild(options)
  return {
    name: 'luxo', enforce: 'pre',
    configResolved: config => build.configure(config.root),
    configureServer: server => { build.server = server },
    buildStart: () => build.start(),
    transform: (code, id) => build.transform(code, id),
    watchChange: (id, change) => build.invalidate(id, change.event === 'delete'),
    handleHotUpdate: context => build.hotUpdate(context),
    buildEnd: error => error ? build.close() : undefined,
    closeBundle: () => build.close(),
  }
}

class PluginBuild {
  private schema: LuxoSchema | null = null
  private root = process.cwd()
  private generatedDir: string
  private identity?: CallIdentity
  private readonly lifecycle = new BuildOwner()
  private readonly callers = new Set<string>()
  server?: ViteDevServer

  constructor(private readonly options: LuxoPluginOptions) {
    this.generatedDir = resolve(this.root, options.outDir || 'src/luxo')
  }

  configure(root: string): void {
    this.root = root
    this.generatedDir = resolve(root, this.options.outDir || 'src/luxo')
  }

  async start(): Promise<void> {
    await this.lifecycle.run(async () => {
      // Load schema from endpoint or local file.
      this.schema = null
      this.callers.clear()
      await this.closeIdentity()
      this.schema = await generateSDK(this.options, this.root)
      this.identity = new CallIdentity(this.root, resolve(this.generatedDir, 'client.ts'))
    })
  }

  async transform(code: string, id: string): Promise<ReturnType<typeof renderMappedSource> | null> {
    const sourceID = id.split('?', 1)[0]!.replaceAll('\\', '/')
    if (!/\.(?:[mc]?ts|tsx)$/.test(sourceID) || sourceID.includes('/node_modules/')) return null
    if (isInsideDirectory(sourceID, this.generatedDir) || !this.schema || !this.identity) return null
    // Analyze field access and inject $select.
    const current = this.identity
    const edits = await analyzeVerifiedEdits(code, id, this.schema, calls => {
      this.callers.add(sourceID)
      return current.identify(sourceID, code, calls)
    })
    if (this.identity !== current) throw new Error(`[luxo] Schema changed during selection analysis: ${id}`)
    return edits ? renderMappedSource(code, sourceID, edits) : null
  }

  async invalidate(file: string, deleted: boolean): Promise<void> {
    await this.identity?.invalidate(file, deleted)
    if (deleted) this.callers.delete(file)
    // Type-only dependencies can change selections without changing emitted imports.
    for (const module of this.callerModules()) this.server!.moduleGraph.invalidateModule(module)
  }

  hotUpdate(context: HmrContext): ModuleNode[] {
    return [...new Set([...context.modules, ...this.callerModules()])]
  }

  private callerModules(): ModuleNode[] {
    if (!this.server) return []
    return [...this.callers].flatMap(file => [...this.server!.moduleGraph.getModulesByFile(file) ?? []])
  }

  async close(): Promise<void> {
    await this.lifecycle.run(async () => {
      this.schema = null
      this.callers.clear()
      this.server = undefined
      await this.closeIdentity()
    })
  }

  private async closeIdentity(): Promise<void> {
    const current = this.identity
    this.identity = undefined
    try {
      await current?.close()
    } catch (cause) {
      this.identity = current
      throw cause
    }
  }
}

function renderMappedSource(code: string, source: string, edits: readonly TextEdit[]) {
  const mapped = new MagicString(code)
  // Selection analysis only inserts text; preserve the string renderer's same-offset order.
  for (const edit of [...edits].sort((a, b) => b.start - a.start)) {
    mapped.prependLeft(edit.start, edit.replacement)
  }
  const map = mapped.generateMap({ source, hires: true })
  return { code: mapped.toString(), map: { ...map, sourcesContent: [code] } }
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
  const response = await fetch(url.toString(), { headers, redirect: 'error' })
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
