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
      usage: 'output',
      fields: [{ id: 1, name: 'id', type: 'Int' }],
    },
  },
  enums: {},
  types: {
    MetricPoint: {
      name: 'MetricPoint',
      usage: 'output',
      fields: [
        { id: 1, name: 'totalCount', type: 'Int' },
        { id: 2, name: 'labels', type: 'String', isList: true },
      ],
    },
    MetricTimeSeries: {
      name: 'MetricTimeSeries',
      usage: 'output',
      fields: [
        { id: 1, name: 'apiName', type: 'String' },
        { id: 2, name: 'points', type: 'Model', typeName: 'MetricPoint', isList: true, relation: true },
      ],
    },
		CreateNodeInput: {
			name: 'CreateNodeInput',
			usage: 'input',
			fields: [{ id: 1, name: 'name', type: 'String' }],
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
				{ id: 4, name: 'input', type: 'JSON', typeName: 'CreateNodeInput' },
      ],
    },
    searchNodes: {
      id: 3,
      name: 'searchNodes',
      module: 'project',
      returnType: 'Node',
      returnList: true,
      paginated: true,
    },
    createSnapshot: {
      id: 4,
      name: 'createSnapshot',
      module: 'project',
      returnType: 'Node',
    },
  },
}

beforeEach(() => outputs.clear())

describe('generateTypes', () => {
  it('generates optional parameters and binary array metadata', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    const apiSchema = outputs.get('generated/schema.ts') ?? ''
		expect(client).toContain('apiName: string | null')
    expect(client).toContain('tags?: string[]')
		expect(client).toContain('input: CreateNodeInput')
		expect(client).toMatch(/import type \{[^\n]*CreateNodeInput[^\n]*\} from '\.\/types'/)
    expect(client).toContain('TransportOptions, CallOptions, Page, Filter, Sorter')
    expect(client).toContain('$filters?: Filter[]; $sorters?: Sorter[]')
    expect(client).toContain('}, options?: CallOptions): Promise<MetricTimeSeries[]>')
    expect(client).toContain('params?: { page?: number; pageSize?: number; $select?: string; $filters?: Filter[]; $sorters?: Sorter[] }, options?: CallOptions')
    expect(client).toContain('async searchNodes(params?: { page?: number; pageSize?: number; $select?: string; $filters?: Filter[]; $sorters?: Sorter[] }')
    expect(client).toContain('async createSnapshot(params?: { $select?: string }, options?: CallOptions)')
    expect(client).not.toContain('async createSnapshot(input:')
    expect(client).toContain('projectId: number; apiName: string | null; tags?: string[]; input: CreateNodeInput; $select?: string')
    expect(client).toContain("this.transport.call('getMetricTimeSeries', params, options)")
    expect(apiSchema).toContain("name: 'tags', type: 'String', isList: true")
		expect(apiSchema).toContain("name: 'apiName', type: 'String', nullable: true")
		expect(apiSchema).toContain("'MetricTimeSeries': { 'apiName': { fieldID: 1 }, 'points': { fieldID: 2, typeName: 'MetricPoint' } }")
		expect(apiSchema).toContain("fields: LUXO_SELECTION_TYPES['MetricTimeSeries'], types: LUXO_SELECTION_TYPES")
	})

	it('keeps absent and explicit null distinct for nullable patch params', async () => {
		const patchSchema: LuxoSchema = {
			models: schema.models,
			apis: {
				updateNode: {
					id: 3,
					name: 'updateNode',
					module: 'node',
					returnType: 'Node',
					params: [
						{ id: 1, name: 'id', type: 'Int' },
						{ id: 2, name: 'nickname', type: 'String', nullable: true, hasDefault: true },
					],
				},
			},
		}
		await generateTypes(patchSchema, 'generated')
		const client = outputs.get('generated/client.ts') ?? ''
		expect(client).toContain('nickname?: string | null')
	})

  it('decodes nested list columns from blob cells', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain('let _points: Uint8Array[] | undefined')
    expect(types).toContain('points: _points ? decodeColumnarMetricPoint(_points[i]) : undefined')
  })

  it('decodes scalar list fields in row mode', async () => {
    await generateTypes(schema, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain("case 2: obj['labels'] = dec.readStringArray(); break")
  })

  it('does not fabricate a value for an unselected required enum field', async () => {
    await generateTypes({
      models: {
        User: {
          name: 'User',
          fields: [{ id: 1, name: 'role', type: 'Enum', typeName: 'Role' }],
        },
      },
      enums: {
        Role: { name: 'Role', values: ['ADMIN', 'VIEWER'] },
      },
      apis: {},
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    expect(types).toContain('role: Selected<Role>')
    expect(types).toContain("Object.prototype.hasOwnProperty.call(data, 'role')")
    expect(types).not.toContain("'' as Role")
  })

  it('does not import an unused row decoder for list returns', async () => {
    await generateTypes(schema, 'generated')

    const client = outputs.get('generated/client.ts') ?? ''
    expect(client).toContain('decodeColumnarMetricTimeSeries')
    expect(client).not.toMatch(/import \{[^\n]*decodeMetricTimeSeries[, }]/)
  })

  it('only imports runtime decode helpers used by the generated schema', async () => {
    await generateTypes({
      models: {
        Node: { name: 'Node', fields: [{ id: 1, name: 'id', type: 'Int' }] },
      },
      apis: {
        listNodes: {
          id: 1,
          name: 'listNodes',
          module: 'node',
          returnType: 'Node',
          returnList: true,
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain("import { Decoder, ColumnarDecoder } from '@luxojs/client'")
    expect(types).not.toContain('decodeBase64')
    expect(types).not.toContain('decodeJSONValue')
    expect(types).not.toContain('decodeScalarArray')
    expect(types).not.toContain('unixSecondsToISO')
    expect(client).toContain("import { FetchTransport, WsTransport } from '@luxojs/client'")
    expect(client).not.toContain('Decoder')
    expect(client).not.toContain('decodeBase64')
    expect(client).not.toContain('decodeJSONValue')
    expect(client).not.toContain('unixSecondsToISO')
  })

  it('keeps JSON and binary model decoding type-equivalent', async () => {
    await generateTypes({
      models: {
        Payload: {
          name: 'Payload',
          fields: [
            { id: 1, name: 'blob', type: 'Bytes' },
            { id: 2, name: 'metadata', type: 'JSON' },
          ],
        },
      },
      apis: {
        listPayloads: {
          id: 3,
          name: 'listPayloads',
          module: 'file',
          returnType: 'Payload',
          returnList: true,
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain('blob: Selected<Uint8Array>')
    expect(types).toContain('metadata: Selected<unknown>')
    expect(types).toContain("Object.prototype.hasOwnProperty.call(data, 'blob')")
    expect(types).not.toContain('new Uint8Array(0)')
    expect(types).toContain('decodeJSONValue(_metadata![i]!)')
    expect(client).toContain('decodeJSONPayload')
  })

  it('separates strict input DTOs from field-selected output models', async () => {
    await generateTypes({
      models: {},
      types: {
        Profile: {
          name: 'Profile',
          usage: 'unused',
          fields: [
            { id: 1, name: 'name', type: 'String' },
            { id: 2, name: 'bio', type: 'String', nullable: true },
          ],
        },
      },
      apis: {
        updateProfile: {
          id: 1,
          name: 'updateProfile',
          module: 'profile',
          returnType: 'Profile',
          params: [{ id: 1, name: 'profile', type: 'Model', typeName: 'Profile' }],
        },
      },
    }, 'generated')

    const types = outputs.get('generated/types.ts') ?? ''
    const client = outputs.get('generated/client.ts') ?? ''
    expect(types).toContain('export interface Profile {')
    expect(types).toContain('name: Selected<string>')
    expect(types).toContain('bio: Selected<string | null>')
    expect(types).toContain('export interface ProfileInput {')
    expect(types).toContain('name: string')
    expect(types).toContain('bio: string | null')
    expect(client).toContain('profile: ProfileInput')
    expect(client).toContain('Promise<Profile>')
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
    expect(client).toContain('subscribeLiveAlerts(params: { projectId: number; $select?: string }, onData: (data: Alert) => void): Promise<() => void>')
    expect(client).toContain("this.transport.subscribe('liveAlerts', params ?? {}")
    expect(client).not.toContain('async liveAlerts(')
  })
})
