import Foundation
import LuxoCompiler

// Usage: swift run LuxoAnalyze --source-dir Sources/ --output Sources/Generated/SelectHints.swift

let args = CommandLine.arguments
var sourceDir = "Sources/"
var outputFile = "Sources/Generated/SelectHints.swift"

var i = 1
while i < args.count {
    switch args[i] {
    case "--source-dir":
        i += 1
        sourceDir = args[i]
    case "--output":
        i += 1
        outputFile = args[i]
    default:
        break
    }
    i += 1
}

let analyzer = SelectAnalyzer()
let fm = FileManager.default

// Walk source directory and analyze all .swift files
if let enumerator = fm.enumerator(atPath: sourceDir) {
    while let file = enumerator.nextObject() as? String {
        guard file.hasSuffix(".swift"),
              !file.contains("SelectHints"),
              !file.contains(".build/") else { continue }
        let path = (sourceDir as NSString).appendingPathComponent(file)
        do {
            try analyzer.analyzeFile(at: path)
        } catch {
            print("[luxo] Warning: failed to parse \(path): \(error)")
        }
    }
}

let output = analyzer.generateHintsFile()
let outputDir = (outputFile as NSString).deletingLastPathComponent
try? fm.createDirectory(atPath: outputDir, withIntermediateDirectories: true)
try output.write(toFile: outputFile, atomically: true, encoding: .utf8)
print("[luxo] Generated select hints → \(outputFile)")
