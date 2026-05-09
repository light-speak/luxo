// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "LuxoClient",
    platforms: [.iOS(.v15), .macOS(.v13)],
    products: [
        .library(name: "LuxoClient", targets: ["LuxoClient"]),
        .library(name: "LuxoCompiler", targets: ["LuxoCompiler"]),
        .executable(name: "LuxoAnalyze", targets: ["LuxoAnalyze"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-syntax.git", from: "510.0.0"),
    ],
    targets: [
        .target(name: "LuxoClient"),
        .target(
            name: "LuxoCompiler",
            dependencies: [
                .product(name: "SwiftSyntax", package: "swift-syntax"),
                .product(name: "SwiftParser", package: "swift-syntax"),
            ]
        ),
        .executableTarget(
            name: "LuxoAnalyze",
            dependencies: ["LuxoCompiler"]
        ),
        .testTarget(name: "LuxoClientTests", dependencies: ["LuxoClient"]),
    ]
)
