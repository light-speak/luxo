import XCTest
@testable import LuxoClient

final class ErrorTests: XCTestCase {
    func testConstructorFields() {
        let err = LuxoError(code: 404, message: "Not found")
        XCTAssertEqual(err.code, 404)
        XCTAssertEqual(err.message, "Not found")
        XCTAssertEqual(err.name, "Error")
        XCTAssertNil(err.traceId)
        XCTAssertNil(err.data)
    }

    func testConstructorWithAllFields() {
        let data: [String: Any] = ["field": "email"]
        let err = LuxoError(code: 422, message: "Validation failed", name: "ValidationError", traceId: "abc-123", data: data)
        XCTAssertEqual(err.code, 422)
        XCTAssertEqual(err.message, "Validation failed")
        XCTAssertEqual(err.name, "ValidationError")
        XCTAssertEqual(err.traceId, "abc-123")
        XCTAssertEqual(err.data?["field"] as? String, "email")
    }

    func testLocalizedDescription() {
        let err = LuxoError(code: 500, message: "Internal server error")
        XCTAssertEqual(err.errorDescription, "[Error] Internal server error")
    }

    func testLocalizedDescriptionWithTraceId() {
        let err = LuxoError(code: 500, message: "Internal server error", name: "ServerError", traceId: "trace-xyz")
        XCTAssertEqual(err.errorDescription, "[ServerError] Internal server error (trace: trace-xyz)")
    }

    func testErrorProtocolConformance() {
        let err: Error = LuxoError(code: 401, message: "Unauthorized", name: "Unauthorized")
        XCTAssertEqual(err.localizedDescription, "[Unauthorized] Unauthorized")
    }

    func testFromJSON() {
        let json: [String: Any] = [
            "code": 403,
            "message": "Forbidden",
            "name": "Forbidden",
            "traceId": "req-456",
            "data": ["reason": "insufficient_permissions"],
        ]
        let err = LuxoError.from(json: json)
        XCTAssertEqual(err.code, 403)
        XCTAssertEqual(err.message, "Forbidden")
        XCTAssertEqual(err.name, "Forbidden")
        XCTAssertEqual(err.traceId, "req-456")
        XCTAssertEqual(err.data?["reason"] as? String, "insufficient_permissions")
    }

    func testFromJSONWithErrorField() {
        let json: [String: Any] = [
            "code": 401,
            "message": "Token expired",
            "error": "Unauthorized",
        ]
        let err = LuxoError.from(json: json)
        XCTAssertEqual(err.name, "Unauthorized")
    }

    func testFromJSONMissingFields() {
        let json: [String: Any] = [:]
        let err = LuxoError.from(json: json)
        XCTAssertEqual(err.code, 0)
        XCTAssertEqual(err.message, "Unknown error")
        XCTAssertEqual(err.name, "Error")
        XCTAssertNil(err.traceId)
        XCTAssertNil(err.data)
    }

    func testFromJSONPartialFields() {
        let json: [String: Any] = ["code": 500]
        let err = LuxoError.from(json: json)
        XCTAssertEqual(err.code, 500)
        XCTAssertEqual(err.message, "Unknown error")
    }
}
