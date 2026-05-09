import Foundation

/// Tree node for nested field selection.
/// Root → { user → { name, email }, comments → { content, user → { id } } }
///
/// Generates $select strings: "name,email,comments{content,user{id}}"
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
