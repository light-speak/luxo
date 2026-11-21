import Foundation
@testable import LuxoClient

let selection = "id,whole,ratio,amount,moment,elapsed,token"

struct PrecisionCase: Decodable {
    let whole: String
    let ratio: String
    let ratioBits: String
    let amount: String
    let moment: String
    let storedMoment: String
    let elapsed: String
    let token: String
}

func check(_ condition: Bool) throws {
    if !condition { throw NSError(domain: "persistent precision", code: 1) }
}

func verify(_ row: PrecisionRecord, _ item: PrecisionCase, _ id: Int) throws {
    try check(row.id.requireValue() == id)
    try check(row.whole.requireValue() == Int(item.whole)!)
    try check(row.ratio.requireValue().bitPattern == UInt64(item.ratioBits, radix: 16)!)
    try check(row.amount.requireValue() == item.amount)
    try check(row.moment.requireValue() == item.storedMoment)
    try check(row.elapsed.requireValue() == Int64(item.elapsed)!)
    try check(row.token.requireValue().uuidString.lowercased() == item.token.lowercased())
}

@main enum PersistentPrecision {
    static func main() async throws {
        let environment = ProcessInfo.processInfo.environment
        let cases = try JSONDecoder().decode([PrecisionCase].self,
            from: Data(contentsOf: URL(fileURLWithPath: environment["LUXO_SQL_PRECISION_CORPUS"]!)))
        let pem = try String(contentsOfFile: CommandLine.arguments[2], encoding: .utf8)
        let base64 = pem.split(separator: "\n").filter { !$0.hasPrefix("---") }.joined()
        guard let certificate = Data(base64Encoded: base64) else { throw NSError(domain: "certificate", code: 1) }
        let session = URLSession(configuration: .default, delegate: PinnedTrust(certificate), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let transport = try URLSessionTransport(endpoint: CommandLine.arguments[1], session: session)
        let client = try LuxoGeneratedClient(transport: transport)
        if environment["LUXO_SQL_NULLABLE"] == "1" {
            try await verifyNullableStorage(client, transport)
            return
        }
        for modeIndex in 0..<2 {
            transport.setMode(modeIndex == 0 ? .json : .binary)
            for (index, item) in cases.enumerated() {
                let id = modeIndex * 10 + index + 1
                let row = try await client.storePrecision(id: id, whole: Int(item.whole)!, ratio: Double(item.ratio)!,
                    amount: item.amount, moment: item.moment, elapsed: Int64(item.elapsed)!, token: UUID(uuidString: item.token)!, select: selection)
                try verify(row, item, id)
                guard let stored = try await client.readPrecision(id: id, select: selection) else { throw NSError(domain: "missing", code: 1) }
                try verify(stored, item, id)
            }
            let rows = try await client.precisionRecords(select: selection)
            try check(rows.count == (modeIndex + 1) * cases.count)
            for row in rows {
                let id = try row.id.requireValue()
                try verify(row, cases[Int(id % 10) - 1], id)
            }
            var rejected = false
            do {
                _ = try await client.storePrecision(id: 999999, whole: 1, ratio: 1, amount: "1",
                    moment: cases[0].moment, elapsed: 1, token: UUID(uuidString: cases[0].token)!, select: selection)
            } catch is LuxoError { rejected = true }
            try check(rejected)
            try check(await client.readPrecision(id: 999999, select: selection) == nil)
            print("Swift \(modeIndex): six exact persistent records, list and rejected Duration passed")
        }
    }
}

let nullableCases: [(Int?, [Int]?)] = [
    (nil, nil), (0, []), (Int.max, [Int.min, 9007199254740993, Int.max]),
]
func verifyNullable(_ row: NullableRecord) throws {
    let expected = nullableCases[try row.id.requireValue() % 10 - 1]
    try check(row.number.requireValue() == expected.0)
    try check(row.items.requireValue() == expected.1)
}
func verifyNullableStorage(_ client: LuxoGeneratedClient, _ transport: URLSessionTransport) async throws {
    let fields = "id,number,items"
    for modeIndex in 0..<2 {
        transport.setMode(modeIndex == 0 ? .json : .binary)
        for (index, item) in nullableCases.enumerated() {
            let id = modeIndex * 10 + index + 1
            try verifyNullable(await client.storeNullableRecord(id: id, number: item.0, items: item.1, select: fields))
            guard let row = try await client.readNullableRecord(id: id, select: fields) else { throw NSError(domain: "missing", code: 1) }
            try check(row.id.requireValue() == id)
            try verifyNullable(row)
        }
        let rows = try await client.nullableRecords(select: fields)
        try check(rows.count == (modeIndex + 1) * nullableCases.count)
        for row in rows { try verifyNullable(row) }
        try check(await client.readNullableRecord(id: 999999, select: fields) == nil)
        print("Swift \(modeIndex): nullable zero, null, empty/list persistence passed")
    }
}
