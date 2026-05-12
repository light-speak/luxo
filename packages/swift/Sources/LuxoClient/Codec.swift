import Foundation

// MARK: - Encoder

/// Binary encoder for Luxo protocol. Zero-copy, varint-based.
public struct Encoder {
    public private(set) var data = Data()

    public init() {}

    public mutating func writeVarint(_ value: UInt64) {
        var v = value
        while v > 0x7F {
            data.append(UInt8(v & 0x7F) | 0x80)
            v >>= 7
        }
        data.append(UInt8(v))
    }

    public mutating func writeSvarint(_ value: Int64) {
        // ZigZag encoding: (n << 1) ^ (n >> 63)
        let encoded = UInt64(bitPattern: (value << 1) ^ (value >> 63))
        writeVarint(encoded)
    }

    public mutating func writeFixed64(_ value: Double) {
        var v = value.bitPattern
        data.append(contentsOf: withUnsafeBytes(of: &v) { Array($0) })
    }

    public mutating func writeBool(_ value: Bool) {
        data.append(value ? 1 : 0)
    }

    public mutating func writeString(_ value: String) {
        let bytes = Array(value.utf8)
        writeVarint(UInt64(bytes.count))
        data.append(contentsOf: bytes)
    }

    public mutating func writeBytes(_ value: Data) {
        writeVarint(UInt64(value.count))
        data.append(value)
    }

    public mutating func writeEnd() {
        data.append(0x00)
    }

    public mutating func writeField(_ fieldID: Int, value: Any, type: String) {
        writeVarint(UInt64(fieldID))
        switch type {
        case "Int":
            writeSvarint(value as? Int64 ?? Int64(value as? Int ?? 0))
        case "Float":
            writeFixed64(value as? Double ?? 0)
        case "Boolean":
            writeBool(value as? Bool ?? false)
        case "String":
            writeString(value as? String ?? "")
        default:
            // Complex type — skip for now
            break
        }
    }
}

// MARK: - Decoder

/// Binary decoder for Luxo protocol.
public struct Decoder {
    private let data: Data
    private var offset: Int = 0

    public init(_ data: Data) {
        self.data = data
    }

    public var isAtEnd: Bool { offset >= data.count }

    public mutating func readVarint() -> UInt64 {
        var result: UInt64 = 0
        var shift: UInt64 = 0
        while offset < data.count {
            let byte = data[offset]
            offset += 1
            result |= UInt64(byte & 0x7F) << shift
            if byte & 0x80 == 0 { break }
            shift += 7
        }
        return result
    }

    public mutating func readSvarint() -> Int64 {
        let n = readVarint()
        // ZigZag decode: (n >> 1) ^ -(n & 1)
        return Int64(bitPattern: (n >> 1) ^ (UInt64(bitPattern: -Int64(n & 1))))
    }

    public mutating func readFixed64() -> Double {
        guard offset + 8 <= data.count else { return 0 }
        let bits = data[offset..<offset+8].withUnsafeBytes { $0.loadUnaligned(as: UInt64.self) }
        offset += 8
        return Double(bitPattern: bits)
    }

    public mutating func readBool() -> Bool {
        guard offset < data.count else { return false }
        let v = data[offset]
        offset += 1
        return v != 0
    }

    public mutating func readString() -> String {
        let len = Int(readVarint())
        guard offset + len <= data.count else { return "" }
        let str = String(data: data[offset..<offset+len], encoding: .utf8) ?? ""
        offset += len
        return str
    }

    public mutating func readBytes() -> Data {
        let len = Int(readVarint())
        guard offset + len <= data.count else { return Data() }
        let bytes = data[offset..<offset+len]
        offset += len
        return Data(bytes)
    }

    /// Read next field ID. Returns 0 for end marker.
    public mutating func nextField() -> Int {
        guard !isAtEnd else { return 0 }
        let id = Int(readVarint())
        return id
    }

    // MARK: - Nullable Readers

    /// Read nullable flag byte. Returns true if value is present (0x01).
    private mutating func readNullFlag() -> Bool {
        guard offset < data.count else { return false }
        let flag = data[offset]
        offset += 1
        return flag != 0x00
    }

    /// Read a nullable Int64 (null flag + zigzag varint).
    public mutating func readIntPtr() -> Int64? {
        if !readNullFlag() { return nil }
        return readSvarint()
    }

    /// Read a nullable Double (null flag + fixed64).
    public mutating func readFloatPtr() -> Double? {
        if !readNullFlag() { return nil }
        return readFixed64()
    }

    /// Read a nullable String (null flag + length-prefixed UTF-8).
    public mutating func readStringPtr() -> String? {
        if !readNullFlag() { return nil }
        return readString()
    }

    /// Read a nullable Bool (null flag + bool byte).
    public mutating func readBoolPtr() -> Bool? {
        if !readNullFlag() { return nil }
        return readBool()
    }

    // MARK: - Array Reader

    /// Read an array of items using a decoder closure.
    /// Format: varint count, then count items decoded by the closure.
    public mutating func readArray<T>(_ decode: (inout Decoder) -> T) -> [T] {
        let count = Int(readVarint())
        var items: [T] = []
        items.reserveCapacity(count)
        for _ in 0..<count {
            items.append(decode(&self))
        }
        return items
    }
}

// MARK: - FieldMask

/// Set a field as selected in a bitmask.
public func fieldMaskSet(_ mask: inout [UInt8], fieldID: Int) {
    let byteIndex = fieldID / 8
    let bitIndex = fieldID % 8
    while mask.count <= byteIndex {
        mask.append(0)
    }
    mask[byteIndex] |= (1 << bitIndex)
}

/// Check if a field is selected in a bitmask.
public func fieldMaskHas(_ mask: [UInt8], fieldID: Int) -> Bool {
    let byteIndex = fieldID / 8
    let bitIndex = fieldID % 8
    guard byteIndex < mask.count else { return false }
    return mask[byteIndex] & (1 << bitIndex) != 0
}
