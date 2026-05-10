import Foundation

/// Luxo API error with code and message.
/// Supports i18n message keys from server.
public struct LuxoError: LocalizedError, Sendable {
    public let code: Int
    public let message: String
    public let data: [String: Any]?

    public init(code: Int, message: String, data: [String: Any]? = nil) {
        self.code = code
        self.message = message
        self.data = data
    }

    public var errorDescription: String? { message }

    /// Parse error from JSON response.
    public static func from(json: [String: Any]) -> LuxoError {
        LuxoError(
            code: json["code"] as? Int ?? 0,
            message: json["message"] as? String ?? "Unknown error",
            data: json["data"] as? [String: Any]
        )
    }
}
