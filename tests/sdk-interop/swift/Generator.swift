import Foundation
import LuxoClient

@main
struct LuxoInteropGenerator {
    static func main() async {
        do {
            try await run()
        } catch {
            FileHandle.standardError.write(Data("Swift interoperability failed: \(error)\n".utf8))
            exit(1)
        }
    }

    private static func run() async throws {
        let environment = ProcessInfo.processInfo.environment
        let endpoint = try required("LUXO_INTEROP_ENDPOINT", in: environment)
        let scratch = URL(fileURLWithPath: try required("LUXO_INTEROP_SCRATCH", in: environment))
        let sdkPath = try required("LUXO_SWIFT_PATH", in: environment)
        let consumer = scratch.appendingPathComponent("swift-consumer", isDirectory: true)
        let sources = consumer.appendingPathComponent("Sources/GoConsumer", isDirectory: true)
        let generator = try LuxoCodegen(
            endpoint: environment["LUXO_INTEROP_SCHEMA_ENDPOINT"] ?? endpoint,
            introspectionKey: try required("LUXO_INTROSPECTION_KEY", in: environment))
        try await generator.generate(outputDir: sources.path)
        guard let fixture = Bundle.module.url(forResource: "Consumer.swift", withExtension: "txt") else {
            throw failure("missing generated-consumer fixture")
        }
        try FileManager.default.copyItem(at: fixture, to: sources.appendingPathComponent("Main.swift"))
        try manifest(sdkPath: sdkPath).write(
            to: consumer.appendingPathComponent("Package.swift"), atomically: true, encoding: .utf8)
        let lock = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .appendingPathComponent("Package.resolved")
        try FileManager.default.copyItem(at: lock, to: consumer.appendingPathComponent("Package.resolved"))
        try runSwift([
            "run", "--disable-automatic-resolution", "--package-path", consumer.path,
            "--scratch-path", consumer.appendingPathComponent(".build").path,
            "-Xswiftc", "-warnings-as-errors", "-Xswiftc", "-strict-concurrency=complete",
            "-Xswiftc", "-enable-testing", "GoConsumer",
        ])
    }

    private static func manifest(sdkPath: String) -> String {
        """
        // swift-tools-version:6.3
        import PackageDescription
        let package = Package(
            name: "GoConsumer", platforms: [.macOS(.v13)],
            dependencies: [.package(name: "LuxoSDK", path: \(String(reflecting: sdkPath)))],
            targets: [.executableTarget(name: "GoConsumer", dependencies: [.product(name: "LuxoClient", package: "LuxoSDK")], exclude: ["LuxoSelection.json"])]
        )
        """
    }

    private static func runSwift(_ arguments: [String]) throws {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = ["swift"] + arguments
        process.standardOutput = FileHandle.standardOutput
        process.standardError = FileHandle.standardError
        try process.run()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw failure("Swift consumer exited \(process.terminationStatus)")
        }
    }

    private static func required(_ name: String, in environment: [String: String]) throws -> String {
        guard let value = environment[name], !value.isEmpty else {
            throw failure("missing \(name); use the Go -run owner")
        }
        return value
    }

    private static func failure(_ message: String) -> LuxoError {
        LuxoError(code: 0, message: message, name: "InteropFailure")
    }
}
