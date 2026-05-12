import XCTest
@testable import LuxoClient

final class TransportTests: XCTestCase {
    // MARK: - TransportMode

    func testTransportModeRawValues() {
        XCTAssertEqual(TransportMode.json.rawValue, "json")
        XCTAssertEqual(TransportMode.binary.rawValue, "binary")
    }

    // MARK: - APISchema

    func testAPISchemaConstruction() {
        let param = APISchema.ParamSchema(fieldID: 1, name: "id", type: "Int")
        let schema = APISchema(id: 42, params: [param])
        XCTAssertEqual(schema.id, 42)
        XCTAssertEqual(schema.params?.count, 1)
        XCTAssertEqual(schema.params?[0].fieldID, 1)
        XCTAssertEqual(schema.params?[0].name, "id")
        XCTAssertEqual(schema.params?[0].type, "Int")
    }

    func testAPISchemaNilParams() {
        let schema = APISchema(id: 1, params: nil)
        XCTAssertEqual(schema.id, 1)
        XCTAssertNil(schema.params)
    }

    // MARK: - URLSessionTransport construction

    func testURLSessionTransportSetToken() {
        let transport = URLSessionTransport(endpoint: "http://localhost:4000/api")
        transport.setToken("test-token-123")
        // No crash, method callable
    }

    func testURLSessionTransportSetMode() {
        let transport = URLSessionTransport(endpoint: "http://localhost:4000/api")
        transport.setMode(.binary)
        transport.setMode(.json)
        // No crash, modes set correctly
    }

    func testURLSessionTransportSetSchema() {
        let transport = URLSessionTransport(endpoint: "http://localhost:4000/api")
        let schema: [String: APISchema] = [
            "getUser": APISchema(id: 1, params: [
                APISchema.ParamSchema(fieldID: 1, name: "id", type: "Int"),
            ]),
        ]
        transport.setSchema(schema)
        // No crash, schema set correctly
    }

    func testURLSessionTransportWithToken() {
        let transport = URLSessionTransport(endpoint: "http://localhost:4000/api", token: "init-token")
        // No crash, initializer with token works
        transport.setToken("new-token")
    }
}
