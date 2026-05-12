import Foundation

/// Luxo API error with code and message.
/// Supports i18n message keys from server.
public struct LuxoError: LocalizedError, Sendable {
    public let code: Int
    public let name: String
    public let message: String
    public let traceId: String?
    public let data: [String: Any]?

    public init(code: Int, message: String, name: String = "Error", traceId: String? = nil, data: [String: Any]? = nil) {
        self.code = code
        self.message = message
        self.name = name
        self.traceId = traceId
        self.data = data
    }

    public var errorDescription: String? {
        if let traceId = traceId {
            return "[\(name)] \(message) (trace: \(traceId))"
        }
        return "[\(name)] \(message)"
    }

    /// Parse error from JSON response.
    public static func from(json: [String: Any]) -> LuxoError {
        LuxoError(
            code: json["code"] as? Int ?? 0,
            message: json["message"] as? String ?? "Unknown error",
            name: json["name"] as? String ?? json["error"] as? String ?? "Error",
            traceId: json["traceId"] as? String,
            data: json["data"] as? [String: Any]
        )
    }
}
