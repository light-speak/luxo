import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'

const outputs = vi.hoisted(() => new Map<string, string>())

vi.mock('fs', () => ({
  existsSync: () => true,
  mkdirSync: vi.fn(),
  writeFileSync: (path: string, data: string) => outputs.set(path, data),
}))

import { generateTypes } from './codegen'

const schema: LuxoSchema = {
  models: {
    Node: {
      name: 'Node',
      fields: [{ id: 1, name: 'id', type: 'Int' }],
    },
  },
  enums: {},
  types: {
    MetricPoint: {
      name: 'MetricPoint',
      fields: [
        { id: 1, name: 'totalCount', type: 'Int' },
        { id: 2, name: 'labels', type: 'String', isList: true },
      ],
    },
    MetricTimeSeries: {
      name: 'MetricTimeSeries',
      fields: [
        { id: 1, name: 'apiName', type: 'String' },
        { id: 2, name: 'points', type: 'Model', typeName: 'MetricPoint', isList: true, relation: true },
      ],
    },
  },
  apis: {
    listNodes: {
      id: 2,
      name: 'listNodes',
      module: 'project',
      returnType: 'Node',
      returnList: true,
      paginated: true,
    },
    getMetricTimeSeries: {
      id: 1,
      name: 'getMetricTimeSeries',
      module: 'monitoring',
      returnType: 'MetricTimeSeries',
      returnList: true,
      params: [
        { id: 1, name: 'projectId', type: 'Int' },
        { id: 2, name: 'apiName', type: 'String', nullable: true },
        { id: 3, name: 'tags', type: 'String', isList: true, hasDefault: true },
      ],
    },
  },
}

beforeEach(() => outputs.clear())

describe('generateTypes', () => {
  it('generates optional parameters and binary array metadata', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    const apiSchema = outputs.get('generated/schema.ts') ?? ''
    expect(client).toContain('apiName?: string')
    expect(client).toContain('tags?: string')
    expect(client).toContain('TransportOptions, CallOptions, Page, Filter, Sorter')
    expect(client).toContain('$filters?: Filter[]; $sorters?: Sorter[]')
    expect(client).toContain('}, options?: CallOptions): Promise<MetricTimeSeries[]>')
    expect(client).toContain('params?: { page?: number; pageSize?: number; $select?: string; $filters?: Filter[]; $sorters?: Sorter[] }, options?: CallOptions')
    expect(client).toContain("this.transport.call('getMetricTimeSeries', params, options)")
    expect(apiSchema).toContain("name: 'tags', type: 'String', isList: true")
    expect(apiSchema).toContain("fields: { 'apiName': 1, 'points': 2 }")
  })

  it('decodes nested list columns from blob cells', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain('let _points: Uint8Array[] | undefined')
    expect(types).toContain('points: _points ? decodeColumnarMetricPoint(_points[i]) : []')
  })

  it('decodes scalar list fields in row mode', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain("case 2: obj['labels'] = dec.readStringArray(); break")
  })

  it('does not import an unused row decoder for list returns', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    expect(client).toContain('decodeColumnarMetricTimeSeries')
    expect(client).not.toMatch(/import \{[^\n]*decodeMetricTimeSeries[, }]/)
  })
})

describe('stream client generation', () => {
  it('generates callback subscriptions instead of one-shot calls', async () => {
    await generateTypes({
      models: {
        Alert: { name: 'Alert', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        liveAlerts: {
          id: 9,
          name: 'liveAlerts',
          module: 'alert',
          returnType: 'Alert',
          stream: true,
          params: [{ id: 1, name: 'projectId', type: 'Int' }],
        },
      },
    }, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    expect(client).toContain('subscribeLiveAlerts(params: { projectId: number }, onData: (data: Alert) => void): Promise<() => void>')
    expect(client).toContain("this.transport.subscribe('liveAlerts', params")
    expect(client).not.toContain('async liveAlerts(')
  })
})
