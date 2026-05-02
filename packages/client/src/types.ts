/** Paginated list response */
export interface Page<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
}

/** Luxo schema definition (from introspection) */
export interface LuxoSchema {
  models: Record<string, LuxoModel>
  apis: Record<string, LuxoAPI>
}

export interface LuxoModel {
  name: string
  fields: LuxoField[]
}

export interface LuxoField {
  id: number
  name: string
  type: string
  nullable?: boolean
  isList?: boolean
}

export interface LuxoAPI {
  id: number
  name: string
  module: string
  returnType?: string
  returnList?: boolean
  paginated?: boolean
  params?: LuxoParam[]
}

export interface LuxoParam {
  id: number
  name: string
  type: string
}
