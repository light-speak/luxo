// swift-tools-version:6.3
import Foundation
import PackageDescription

let sdkPath = ProcessInfo.processInfo.environment["LUXO_SWIFT_PATH"] ?? "../../../../luxo-swift"
let package = Package(
    name: "LuxoInteropGenerator",
    platforms: [.macOS(.v13)],
    dependencies: [.package(name: "LuxoSDK", path: sdkPath)],
    targets: [
        .executableTarget(
            name: "LuxoInteropGenerator",
            dependencies: [.product(name: "LuxoClient", package: "LuxoSDK")],
            path: ".",
            exclude: ["run.sh"],
            sources: ["Generator.swift"],
            resources: [.copy("Consumer.swift.txt")]
        )
    ]
)
