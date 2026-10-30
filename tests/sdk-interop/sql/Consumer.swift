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
        try narrow(await client.products(selectionPlan: LuxoGeneratedClient.prepareSelection_products("name")))
        try narrow(await client.selectedProducts(selectionPlan: LuxoGeneratedClient.prepareSelection_selectedProducts("name")))
        let full = try await client.selectedProducts(select: "id,name,price,description")
        try check(full.count == 128)
        for row in full {
            let price = try row.price.requireValue()
            try check(row.id.requireValue() == 9007199254740992 + price)
            try check(row.name.requireValue() == "item-\(price)")
            try check(row.description.requireValue() == String(repeating: "x", count: 4096))
        }
        let plan = try LuxoGeneratedClient.prepareSelection_listProducts("name")
        let first = try await client.listProducts(pageSize: 64, selectionPlan: plan)
        let second = try await client.listProducts(page: 2, pageSize: 64, selectionPlan: plan)
        try check(first.total == 128 && second.total == 128)
        try check(first.page == 1 && second.page == 2 && first.pageSize == 64 && second.pageSize == 64)
        try narrow(first.items, count: 64)
        try narrow(second.items, count: 64)
        try check(Set((first.items + second.items).map { try $0.name.requireValue() }).count == 128)
        print("Swift: five generated H2 calls, 128 rows, exact Int64 and pagination passed")
    }
}
