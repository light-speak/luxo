import Foundation
import Security
@testable import LuxoClient

private let articleViews = ["article-1:alice", "article-2:alice", "article-3:bob",
                            "article-4:-", "article-5:zero", "article-6:-"]

private func same(_ actual: [String], _ expected: [String]) throws {
    if actual != expected { throw NSError(domain: "relationship-values", code: 1) }
}

private func verify(_ value: Bool) throws {
    if !value { throw NSError(domain: "relationship-page", code: 1) }
}

private func verifyRelations(_ client: LuxoGeneratedClient) async throws {
    let rows = try await client.articleRows()
    try same(rows.map { "\(try $0.title.requireValue()):\(try $0.author.requireValue()?.name.requireValue() ?? "-")" }, articleViews)
    let people = try await client.peopleRows()
    try same(people.map { person in
        let titles = try person.articles.requireValue().map { try $0.title.requireValue() }.sorted().joined(separator: "|")
        return "\(try person.name.requireValue()):\(titles)"
    }, ["zero:article-5", "alice:article-1|article-2", "bob:article-3", "empty:"])
    for number in 1...4 {
        let page = try await client.articlePage(page: number, pageSize: 2)
        try verify(page.total == 6 && page.page == number && page.pageSize == 2)
        var pageViews: [String] = []
        for row in page.items {
            pageViews.append("\(try row.title.requireValue()):\(try row.author.requireValue()?.name.requireValue() ?? "-")")
        }
        try same(pageViews, Array(articleViews.dropFirst(Int(number - 1) * 2).prefix(2)))
    }
    let boxed = try await client.boxedArticlePage(page: 2, pageSize: 2)
    try verify(boxed.total == 6 && boxed.page == 2 && boxed.pageSize == 2)
    try same(boxed.items.map { "\(try $0.title.requireValue()):\(try $0.author.requireValue()?.name.requireValue() ?? "-")" },
             Array(articleViews.dropFirst(2).prefix(2)))
    let explicit = try await client.articlePage(pageSize: 2, select: "title")
    try verify(explicit.total == 6 && explicit.page == 1 && explicit.pageSize == 2)
    try same(explicit.items.map { try $0.title.requireValue() }, ["article-1", "article-2"])
    let names = try await client.peopleRows(select: "name")
    try same(names.map { try $0.name.requireValue() }, ["zero", "alice", "bob", "empty"])
}

@main enum RelationshipSQL {
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
            try await verifyRelations(client)
        }
        print("Swift: eighteen inferred/explicit relationship and pagination calls passed")
    }
}
