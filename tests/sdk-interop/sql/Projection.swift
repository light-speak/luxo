import Foundation
import Security
@testable import LuxoClient

final class PinnedTrust: NSObject, URLSessionDelegate {
    let certificate: Data
    init(_ certificate: Data) { self.certificate = certificate }
    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust,
              let certificate = SecCertificateCreateWithData(nil, certificate as CFData),
              SecTrustSetAnchorCertificates(trust, [certificate] as CFArray) == errSecSuccess,
              SecTrustSetAnchorCertificatesOnly(trust, true) == errSecSuccess,
              SecTrustEvaluateWithError(trust, nil) else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(trust: trust))
    }
}

func check(_ value: Bool) throws {
    if !value { throw NSError(domain: "fixture", code: 1) }
}
func narrow(_ rows: [Product], count: Int = 128) throws {
    try check(rows.count == count)
    try check(Set(rows.map { try $0.name.requireValue() }).count == count)
    for row in rows {
        try check(row.name.requireValue().hasPrefix("item-"))
        guard case .unselected = row.id, case .unselected = row.price,
              case .unselected = row.description else { throw NSError(domain: "unselected", code: 1) }
    }
}
@main enum Probe {
    static func main() async throws {
        let pem = try String(contentsOfFile: CommandLine.arguments[2], encoding: .utf8)
        let base64 = pem.split(separator: "\n").filter { !$0.hasPrefix("---") }.joined()
        guard let certificate = Data(base64Encoded: base64) else { throw NSError(domain: "certificate", code: 1) }
        let session = URLSession(configuration: .default, delegate: PinnedTrust(certificate), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let transport = try URLSessionTransport(endpoint: CommandLine.arguments[1], session: session)
        transport.setMode(.binary)
        let client = try LuxoGeneratedClient(transport: transport)
        try await measure(client)
    }

    @MainActor static func measure(_ client: LuxoGeneratedClient) async throws {
        let full = try LuxoGeneratedClient.prepareSelection_products("name")
        let selected = try LuxoGeneratedClient.prepareSelection_selectedProducts("name")
        for _ in 0..<20 {
            try verify(await client.products(selectionPlan: full))
            try verify(await client.selectedProducts(selectionPlan: selected))
        }
        var output = "batch,path,iteration,microseconds\n"
        for batch in 0..<3 {
            for iteration in 0..<50 {
                let order = (batch + iteration) % 2 == 0 ? [0, 1] : [1, 0]
                for index in order {
                    let start = DispatchTime.now().uptimeNanoseconds
                    let rows: [Product]
                    if index == 0 { rows = try await client.products(selectionPlan: full) }
                    else { rows = try await client.selectedProducts(selectionPlan: selected) }
                    let elapsed = Double(DispatchTime.now().uptimeNanoseconds - start) / 1000
                    try verify(rows)
                    let name = index == 0 ? "fullSQL" : "selectedSQL"
                    output += "\(batch),\(name),\(iteration),\(elapsed)\n"
                }
            }
        }
        guard let root = ProcessInfo.processInfo.environment["LUXO_SQL_SCRATCH"] else {
            throw NSError(domain: "evidence", code: 1)
        }
        try output.write(toFile: root + "/projection-swift.csv", atomically: false, encoding: .utf8)
        print("Swift projection comparison: 340 verified calls; no allocation claim")
    }
}

private let expectedNames = (1...128).map { "item-\($0)" }.sorted()
private func verify(_ rows: [Product]) throws {
    try narrow(rows)
    try check(rows.map { try $0.name.requireValue() }.sorted() == expectedNames)
}
