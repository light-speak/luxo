// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "LuxoClient",
    platforms: [.iOS(.v15), .macOS(.v13)],
    products: [
        .library(name: "LuxoClient", targets: ["LuxoClient"]),
    ],
    targets: [
        .target(name: "LuxoClient"),
        .testTarget(name: "LuxoClientTests", dependencies: ["LuxoClient"]),
    ]
)
