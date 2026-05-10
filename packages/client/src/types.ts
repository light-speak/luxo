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
  enums?: Record<string, LuxoEnum>
  types?: Record<string, LuxoTypeDecl>
}

export interface LuxoModel {
  name: string
  fields: LuxoField[]
}

export interface LuxoField {
  id: number
  name: string
  type: string
  typeName?: string // original Luxo type (User, MemberRole, etc.)
  nullable?: boolean
  isList?: boolean
  list?: boolean // alias for isList
  relation?: boolean // true if relation field (not DB column)
}

export interface LuxoEnum {
  name: string
  values: string[]
}

export interface LuxoTypeDecl {
  name: string
  fields: LuxoField[]
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
