import Foundation
import LuxoClient

@main enum Generate {
    static func main() async throws {
        let generator = try LuxoCodegen(
            endpoint: CommandLine.arguments[1], introspectionKey: "benchmark")
        try await generator.generate(outputDir: CommandLine.arguments[2])
    }
}
