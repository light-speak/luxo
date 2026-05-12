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
/// Supports timeout configuration and 401 auto-refresh.
public final class URLSessionTransport: Transport, @unchecked Sendable {
    private let endpoint: URL
    private let session: URLSession
    private var headers: [String: String] = [:]
    private var mode: TransportMode = .json
    private var schema: [String: APISchema] = [:]
    private let timeout: TimeInterval

    /// Callback invoked on 401 response. Return a new token to retry, or nil to propagate error.
    public var onTokenExpired: (() async -> String?)?

    public init(endpoint: String, token: String? = nil, timeout: TimeInterval = 30) {
        self.endpoint = URL(string: endpoint)!
        self.timeout = timeout
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
        do {
            return try await doCall(api, params: params)
        } catch let error as LuxoError where error.code == 401 {
            // Attempt token refresh on 401
            if let refresh = onTokenExpired, let newToken = await refresh() {
                setToken(newToken)
                return try await doCall(api, params: params)
            }
            throw error
        }
    }

    private func doCall(_ api: String, params: [String: Any]?) async throws -> Any {
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
        request.timeoutInterval = timeout
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
                message: errorBody?["message"] as? String ?? "HTTP \(httpResponse.statusCode)",
                name: errorBody?["name"] as? String ?? "Error",
                traceId: errorBody?["traceId"] as? String
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
        request.timeoutInterval = timeout
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
/// Supports exponential backoff auto-reconnect on disconnect.
public final class WebSocketTransport: @unchecked Sendable {
    private let url: URL
    private var task: URLSessionWebSocketTask?
    private let session: URLSession
    private var handlers: [String: (Any) -> Void] = [:]
    private let lock = NSLock()

    // Auto-reconnect state
    private var shouldReconnect: Bool = true
    private var reconnectAttempt: Int = 0
    private let maxBackoff: TimeInterval = 30.0
    private let baseBackoff: TimeInterval = 1.0

    public init(url: String) {
        self.url = URL(string: url)!
        self.session = URLSession(configuration: .default)
    }

    public func connect() {
        shouldReconnect = true
        reconnectAttempt = 0
        openConnection()
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
        shouldReconnect = false
        task?.cancel(with: .normalClosure, reason: nil)
        task = nil
        lock.lock()
        handlers.removeAll()
        lock.unlock()
    }

    // MARK: - Internal

    private func openConnection() {
        task = session.webSocketTask(with: url)
        task?.resume()
        receiveLoop()
    }

    private func receiveLoop() {
        task?.receive { [weak self] result in
            guard let self = self else { return }
            switch result {
            case .success(let message):
                // Reset backoff on successful receive
                self.reconnectAttempt = 0
                self.handleMessage(message)
                self.receiveLoop()
            case .failure:
                self.scheduleReconnect()
            }
        }
    }

    private func handleMessage(_ message: URLSessionWebSocketTask.Message) {
        switch message {
        case .data(let data):
            if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let api = json["api"] as? String,
               let payload = json["data"] {
                lock.lock()
                let handler = handlers[api]
                lock.unlock()
                handler?(payload)
            }
        case .string(let text):
            if let data = text.data(using: .utf8),
               let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let api = json["api"] as? String,
               let payload = json["data"] {
                lock.lock()
                let handler = handlers[api]
                lock.unlock()
                handler?(payload)
            }
        @unknown default:
            break
        }
    }

    /// Exponential backoff: 1s, 2s, 4s, 8s... up to 30s max.
    private func scheduleReconnect() {
        guard shouldReconnect else { return }
        let delay = min(baseBackoff * pow(2.0, Double(reconnectAttempt)), maxBackoff)
        reconnectAttempt += 1
        DispatchQueue.global().asyncAfter(deadline: .now() + delay) { [weak self] in
            guard let self = self, self.shouldReconnect else { return }
            self.openConnection()
        }
    }
}
