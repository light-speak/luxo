import Foundation
import Security
@testable import LuxoClient

private func verify(_ value: Bool) throws {
    if !value { throw NSError(domain: "inferred-sql", code: 1) }
}

private func verifyNames(_ values: [String]) throws {
    try verify(values.sorted() == (1...128).map { "item-\($0)" }.sorted())
}

private func selectedSites(_ client: LuxoGeneratedClient) async throws {
    let mapped = try await client.selectedProducts()
    try verifyNames(mapped.map { row in
        try verify(row.price.requireValue() > 0)
        return try row.name.requireValue()
    })
    let first = try await client.selectedProducts()
    try verify(first[0].price.requireValue() == 1 && first[0].name.requireValue() == "item-1")
    let source = try await client.selectedProducts()
    let alias = source
    try verifyNames(alias.map { row in
        try verify(row.price.requireValue() > 0)
        return try row.name.requireValue()
    })
    let indexed = try await client.selectedProducts()
    try verify(indexed[0].name.requireValue() == "item-1")
    let last = try await client.selectedProducts()
    try verify(last[127].name.requireValue() == "item-128")
}

private func businessSites(_ client: LuxoGeneratedClient) async throws {
    // Preserve each static call's local field accesses; opaque model helpers
    // would require explicit selections instead of proving automatic inference.
    let priced = try await client.pricedProducts()
    try verifyNames(priced.map { try $0.name.requireValue() })
    let privateRows = try await client.privateProducts()
    try verifyNames(privateRows.map { try $0.name.requireValue() })
    let called = try await client.calledProducts()
    try verifyNames(called.map { try $0.name.requireValue() })
    let calledPrivate = try await client.calledPrivateProducts()
    try verifyNames(calledPrivate.map { try $0.name.requireValue() })
    let nested = try await client.nestedProducts()
    try verifyNames(nested.map { try $0.name.requireValue() })
    let own = try await client.ownProducts()
    try verifyNames(own.map { try $0.name.requireValue() })
    let ownPrivate = try await client.ownPrivateProducts()
    try verifyNames(ownPrivate.map { try $0.name.requireValue() })
    let combined = try await client.combinedProducts()
    try verifyNames(combined.map { try $0.name.requireValue() })
    let forwarded = try await client.forwardedProducts()
    try verifyNames(forwarded.map { try $0.name.requireValue() })
    try verifyNames(await client.scalarCalledProducts())
    let aliasedOwn = try await client.aliasedOwnProducts()
    try verifyNames(aliasedOwn.map { try $0.name.requireValue() })
}

@main enum InferredSQL {
    static func main() async throws {
        let pem = try String(contentsOfFile: CommandLine.arguments[2], encoding: .utf8)
        let base64 = pem.split(separator: "\n").filter { !$0.hasPrefix("---") }.joined()
        guard let certificate = Data(base64Encoded: base64) else {
            throw NSError(domain: "certificate", code: 1)
        }
        let session = URLSession(configuration: .default, delegate: PinnedTrust(certificate), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let transport = try URLSessionTransport(endpoint: CommandLine.arguments[1], session: session)
        let client = try LuxoGeneratedClient(transport: transport)
        for mode in [TransportMode.json, .binary] {
            transport.setMode(mode)
            try await selectedSites(client)
            try await businessSites(client)
        }
        print("Swift inferred macOS: thirty-two JSON/Binary H2 calls passed")
    }
}
