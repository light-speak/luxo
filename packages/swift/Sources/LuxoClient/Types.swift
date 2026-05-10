import Foundation

// MARK: - Pagination

/// Offset-based page response.
public struct Page<T: Decodable>: Decodable {
    public let items: [T]
    public let total: Int
    public let page: Int
    public let pageSize: Int
}

/// Cursor-based page response.
public struct Cursor<T: Decodable>: Decodable {
    public let items: [T]
    public let nextCursor: String?
    public let hasMore: Bool
}

// MARK: - Schema (for codegen / introspection)

public struct LuxoSchema: Codable, Sendable {
    public let models: [String: LuxoModel]
    public let apis: [String: LuxoAPI]
    public let enums: [String: LuxoEnum]?
    public let types: [String: LuxoTypeDecl]?
}

public struct LuxoEnum: Codable, Sendable {
    public let name: String
    public let values: [String]
}

public struct LuxoTypeDecl: Codable, Sendable {
    public let name: String
    public let fields: [LuxoField]
}

public struct LuxoModel: Codable, Sendable {
    public let name: String
    public let fields: [LuxoField]
}

public struct LuxoField: Codable, Sendable {
    public let id: Int
    public let name: String
    public let type: String
    public let typeName: String?
    public let nullable: Bool?
    public let isList: Bool?
    public let relation: Bool?
}

public struct LuxoAPI: Codable, Sendable {
    public let id: Int
    public let name: String
    public let module: String
    public let returnType: String?
    public let returnList: Bool?
    public let paginated: Bool?
    public let params: [LuxoParam]?
}

public struct LuxoParam: Codable, Sendable {
    public let name: String
    public let type: String
    public let required: Bool?
}
