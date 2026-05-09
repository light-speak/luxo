import Foundation

/// Compile-time field access analyzer for Swift.
///
/// Swift doesn't have a Vite/KCP-style build plugin ecosystem,
/// so we use a source-level analyzer that runs as a build phase script:
///
/// ```bash
/// swift run LuxoAnalyze --source-dir Sources/ --output Sources/Generated/SelectHints.swift
/// ```
///
/// Tracks nested field access patterns:
///   post.user.name          → "user{name}"
///   post.comments[0].content → "comments{content}"
///   post.comments.forEach { $0.user.id } → "comments{user{id}}"
///
/// Uses SwiftSyntax AST for accurate parsing.
///
/// NOTE: For production use, integrate with swift-syntax package.
/// This is the standalone text-level analyzer (fallback).
public final class SelectAnalyzer {

    /// API name → field tree
    private var trees: [String: FieldNode] = [:]

    /// Variable name → API name
    private var varToAPI: [String: String] = [:]

    /// Variable name → parent chain
    private var varToParent: [String: (parentVar: String, field: String)] = [:]

    public static let maxNestingDepth = 5

    public init() {}

    /// Analyze a Swift source file.
    public func analyzeFile(_ source: String) {
        // Phase 1: find API call assignments
        // Pattern: let/var user = await client.getUser(1)
        let callPattern = try! NSRegularExpression(
            pattern: #"(?:let|var)\s+(\w+)\s*=\s*(?:try\s+)?(?:await\s+)?\w+\.(\w+)\s*\("#
        )
        let matches = callPattern.matches(in: source, range: NSRange(source.startIndex..., in: source))
        for match in matches {
            let varName = String(source[Range(match.range(at: 1), in: source)!])
            let apiName = String(source[Range(match.range(at: 2), in: source)!])
            varToAPI[varName] = apiName
            trees[apiName] = trees[apiName] ?? FieldNode.root()
        }

        // Phase 2: find nested property chains
        for (varName, apiName) in varToAPI {
            let chainPattern = try! NSRegularExpression(
                pattern: #"\b\#(NSRegularExpression.escapedPattern(for: varName))((?:\??\.\w+|\[\w+\])+)"#
            )
            let chainMatches = chainPattern.matches(in: source, range: NSRange(source.startIndex..., in: source))
            for match in chainMatches {
                let chainStr = String(source[Range(match.range(at: 1), in: source)!])
                let segments = chainStr
                    .components(separatedBy: CharacterSet(charactersIn: ".?[]"))
                    .filter { !$0.isEmpty && Int($0) == nil }

                guard !segments.isEmpty, let tree = trees[apiName] else { continue }
                var node = tree
                for seg in segments {
                    node = node.addChild(seg)
                }
            }
        }

        // Phase 3: track closure params ($0, named params in forEach/map)
        let closurePattern = try! NSRegularExpression(
            pattern: #"(\w+)\.(\w+)\.(?:forEach|map|filter|compactMap)\s*\{\s*(?:(\w+)\s+in\s+)?"#
        )
        let closureMatches = closurePattern.matches(in: source, range: NSRange(source.startIndex..., in: source))
        for match in closureMatches {
            let parentVar = String(source[Range(match.range(at: 1), in: source)!])
            let fieldName = String(source[Range(match.range(at: 2), in: source)!])
            let paramName: String
            if match.range(at: 3).location != NSNotFound {
                paramName = String(source[Range(match.range(at: 3), in: source)!])
            } else {
                paramName = "$0"
            }

            guard varToAPI[parentVar] != nil else { continue }
            varToParent[paramName] = (parentVar: parentVar, field: fieldName)

            // Find field accesses on the closure param
            let escapedParam = paramName == "$0" ? "\\$0" : NSRegularExpression.escapedPattern(for: paramName)
            let paramChainPattern = try! NSRegularExpression(
                pattern: "\(escapedParam)((?:\\??\\.\\w+)+)"
            )
            let paramMatches = paramChainPattern.matches(in: source, range: NSRange(source.startIndex..., in: source))
            for pm in paramMatches {
                let chainStr = String(source[Range(pm.range(at: 1), in: source)!])
                let segments = chainStr.components(separatedBy: CharacterSet(charactersIn: ".?"))
                    .filter { !$0.isEmpty }

                guard let apiName = varToAPI[parentVar], let tree = trees[apiName] else { continue }
                var node = tree.addChild(fieldName)
                for seg in segments {
                    node = node.addChild(seg)
                }
            }
        }
    }

    /// Build select hints: API name → $select string.
    public func buildHints() -> [String: String] {
        var result: [String: String] = [:]
        for (api, tree) in trees {
            let depth = tree.maxDepth()
            if depth > Self.maxNestingDepth {
                print("[luxo] Warning: \(api) has \(depth)-level nested field selection (max recommended: \(Self.maxNestingDepth)). Consider restructuring your query.")
            }
            let selectStr = tree.toSelectString()
            if !selectStr.isEmpty {
                result[api] = selectStr
            }
        }
        return result
    }

    /// Generate SelectHints.swift source code.
    public func generateHintsFile() -> String {
        let hints = buildHints()
        var out = "// GENERATED BY LuxoAnalyze. DO NOT EDIT.\n\n"
        out += "import Foundation\n\n"
        out += "public enum SelectHints {\n"
        out += "    public static let hints: [String: String] = [\n"
        for (api, select) in hints.sorted(by: { $0.key < $1.key }) {
            out += "        \"\(api)\": \"\(select)\",\n"
        }
        out += "    ]\n"
        out += "}\n"
        return out
    }
}

// MARK: - FieldNode

/// Tree node for nested field selection.
public final class FieldNode {
    public let name: String
    public private(set) var children: [String: FieldNode] = [:]

    private init(_ name: String) { self.name = name }

    public static func root() -> FieldNode { FieldNode("") }

    @discardableResult
    public func addChild(_ fieldName: String) -> FieldNode {
        if let existing = children[fieldName] { return existing }
        let child = FieldNode(fieldName)
        children[fieldName] = child
        return child
    }

    public func maxDepth() -> Int {
        if children.isEmpty { return 0 }
        return children.values.map { $0.maxDepth() }.max()! + 1
    }

    public func toSelectString() -> String {
        if children.isEmpty { return "" }
        return children.values.map { child in
            let nested = child.toSelectString()
            return nested.isEmpty ? child.name : "\(child.name){\(nested)}"
        }.joined(separator: ",")
    }
}
