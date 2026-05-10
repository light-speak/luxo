import Foundation

// MARK: - Transport Protocol

/// Transport protocol — all transports implement this.
public protocol Transport: Sendable {
    func call(_ api: String, params: [String: Any]?) async throws -> Any
    func setToken(_ token: String)
    func setMode(_ mode: TransportMode)
    func setSchema(_ schema: [String: APISchema])
}

public enum TransportMode: String, Sendable {
    case json
    case binary
}

/// API schema metadata for binary encoding.
public struct APISchema: Sendable {
    public let id: Int
    public let params: [ParamSchema]?

    public struct ParamSchema: Sendable {
        public let fieldID: Int
        public let name: String
        public let type: String
    }
}

// MARK: - URLSession Transport (HTTP/2)

/// HTTP/2 transport using URLSession. Single connection, multiplexed requests.
/// Supports both JSON and binary (Luxo codec) modes.
public final class URLSessionTransport: Transport, @unchecked Sendable {
    private let endpoint: URL
    private let session: URLSession
    private var headers: [String: String] = [:]
    private var mode: TransportMode = .json
    private var schema: [String: APISchema] = [:]

    public init(endpoint: String, token: String? = nil) {
        self.endpoint = URL(string: endpoint)!
        // HTTP/2 multiplexing via shared URLSession
        let config = URLSessionConfiguration.default
        config.httpAdditionalHeaders = ["Content-Type": "application/json"]
        self.session = URLSession(configuration: config)
        if let token = token {
            headers["Authorization"] = "Bearer \(token)"
        }
    }

    public func setToken(_ token: String) {
        headers["Authorization"] = "Bearer \(token)"
    }

    public func setMode(_ mode: TransportMode) {
        self.mode = mode
    }

    public func setSchema(_ schema: [String: APISchema]) {
        self.schema = schema
    }

    public func call(_ api: String, params: [String: Any]? = nil) async throws -> Any {
        switch mode {
        case .json:
            return try await jsonCall(api, params: params)
        case .binary:
            return try await binaryCall(api, params: params)
        }
    }

    private func jsonCall(_ api: String, params: [String: Any]?) async throws -> Any {
        var body: [String: Any] = ["$api": api]
        if let params = params {
            for (k, v) in params { body[k] = v }
        }

        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        for (k, v) in headers { request.setValue(v, forHTTPHeaderField: k) }
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await session.data(for: request)

        guard let httpResponse = response as? HTTPURLResponse else {
            throw LuxoError(code: 0, message: "Invalid response")
        }

        guard httpResponse.statusCode == 200 else {
            let errorBody = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
            throw LuxoError(
                code: errorBody?["code"] as? Int ?? httpResponse.statusCode,
                message: errorBody?["message"] as? String ?? "HTTP \(httpResponse.statusCode)"
            )
        }

        return try JSONSerialization.jsonObject(with: data)
    }

    private func binaryCall(_ api: String, params: [String: Any]?) async throws -> Any {
        guard let apiSchema = schema[api] else {
            // Fallback to JSON if no schema
            return try await jsonCall(api, params: params)
        }

        var encoder = Encoder()
        encoder.writeVarint(UInt64(apiSchema.id))
        // Encode params using binary codec
        if let params = params, let paramSchemas = apiSchema.params {
            for ps in paramSchemas {
                if let value = params[ps.name] {
                    encoder.writeField(ps.fieldID, value: value, type: ps.type)
                }
            }
        }
        encoder.writeEnd()

        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/x-luxo", forHTTPHeaderField: "Content-Type")
        request.setValue("binary", forHTTPHeaderField: "X-Luxo-Mode")
        for (k, v) in headers { request.setValue(v, forHTTPHeaderField: k) }
        request.httpBody = encoder.data

        let (data, response) = try await session.data(for: request)

        guard let httpResponse = response as? HTTPURLResponse, httpResponse.statusCode == 200 else {
            throw LuxoError(code: 0, message: "Binary call failed")
        }

        return data // Return raw bytes for binary decoding
    }
}

// MARK: - WebSocket Transport

/// WebSocket transport for streaming (subscriptions) and bidirectional communication.
public final class WebSocketTransport: @unchecked Sendable {
    private let url: URL
    private var task: URLSessionWebSocketTask?
    private let session: URLSession
    private var handlers: [String: (Any) -> Void] = [:]
    private let lock = NSLock()

    public init(url: String) {
        self.url = URL(string: url)!
        self.session = URLSession(configuration: .default)
    }

    public func connect() {
        task = session.webSocketTask(with: url)
        task?.resume()
        receiveLoop()
    }

    public func subscribe(_ api: String, params: [String: Any]? = nil, handler: @escaping (Any) -> Void) {
        lock.lock()
        handlers[api] = handler
        lock.unlock()
        let msg: [String: Any] = [
            "type": "subscribe",
            "api": api,
            "params": params ?? [:],
        ]
        if let data = try? JSONSerialization.data(withJSONObject: msg) {
            task?.send(.data(data)) { _ in }
        }
    }

    public func close() {
        task?.cancel(with: .normalClosure, reason: nil)
        task = nil
        lock.lock()
        handlers.removeAll()
        lock.unlock()
    }

    private func receiveLoop() {
        task?.receive { [weak self] result in
            switch result {
            case .success(let message):
                switch message {
                case .data(let data):
                    if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                       let api = json["api"] as? String,
                       let payload = json["data"] {
                        self?.lock.lock()
                        let handler = self?.handlers[api]
                        self?.lock.unlock()
                        handler?(payload)
                    }
                case .string(let text):
                    if let data = text.data(using: .utf8),
                       let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                       let api = json["api"] as? String,
                       let payload = json["data"] {
                        self?.lock.lock()
                        let handler = self?.handlers[api]
                        self?.lock.unlock()
                        handler?(payload)
                    }
                @unknown default:
                    break
                }
                self?.receiveLoop()
            case .failure:
                break
            }
        }
    }
}
