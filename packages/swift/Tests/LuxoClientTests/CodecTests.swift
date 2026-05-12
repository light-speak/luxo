import XCTest
@testable import LuxoClient

final class CodecTests: XCTestCase {
    func testVarintRoundTrip() {
        var encoder = Encoder()
        encoder.writeVarint(300)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), 300)
    }

    func testSvarintRoundTrip() {
        var encoder = Encoder()
        encoder.writeSvarint(-42)
        encoder.writeSvarint(42)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readSvarint(), -42)
        XCTAssertEqual(decoder.readSvarint(), 42)
    }

    func testStringRoundTrip() {
        var encoder = Encoder()
        encoder.writeString("hello 世界")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readString(), "hello 世界")
    }

    func testBoolRoundTrip() {
        var encoder = Encoder()
        encoder.writeBool(true)
        encoder.writeBool(false)
        var decoder = Decoder(encoder.data)
        XCTAssertTrue(decoder.readBool())
        XCTAssertFalse(decoder.readBool())
    }

    func testFixed64RoundTrip() {
        var encoder = Encoder()
        encoder.writeFixed64(3.14)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readFixed64(), 3.14, accuracy: 0.001)
    }

    func testFieldMask() {
        var mask: [UInt8] = []
        fieldMaskSet(&mask, fieldID: 0)
        fieldMaskSet(&mask, fieldID: 5)
        fieldMaskSet(&mask, fieldID: 15)

        XCTAssertTrue(fieldMaskHas(mask, fieldID: 0))
        XCTAssertTrue(fieldMaskHas(mask, fieldID: 5))
        XCTAssertTrue(fieldMaskHas(mask, fieldID: 15))
        XCTAssertFalse(fieldMaskHas(mask, fieldID: 1))
        XCTAssertFalse(fieldMaskHas(mask, fieldID: 16))
    }

    // MARK: - Edge Cases

    func testNegativeSvarints() {
        var encoder = Encoder()
        encoder.writeSvarint(-1)
        encoder.writeSvarint(Int64.min)
        encoder.writeSvarint(Int64.max)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readSvarint(), -1)
        XCTAssertEqual(decoder.readSvarint(), Int64.min)
        XCTAssertEqual(decoder.readSvarint(), Int64.max)
    }

    func testEmptyString() {
        var encoder = Encoder()
        encoder.writeString("")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readString(), "")
    }

    func testLargeVarint() {
        var encoder = Encoder()
        encoder.writeVarint(UInt64.max)
        encoder.writeVarint(0)
        encoder.writeVarint(UInt64.max / 2)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), UInt64.max)
        XCTAssertEqual(decoder.readVarint(), 0)
        XCTAssertEqual(decoder.readVarint(), UInt64.max / 2)
    }

    func testFixed64SpecialValues() {
        var encoder = Encoder()
        encoder.writeFixed64(0.0)
        encoder.writeFixed64(-0.0)
        encoder.writeFixed64(Double.infinity)
        encoder.writeFixed64(-Double.infinity)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readFixed64(), 0.0)
        XCTAssertEqual(decoder.readFixed64(), -0.0)
        XCTAssertEqual(decoder.readFixed64(), Double.infinity)
        XCTAssertEqual(decoder.readFixed64(), -Double.infinity)
    }

    func testBytesRoundTrip() {
        var encoder = Encoder()
        let testData = Data([0x01, 0x02, 0xFF, 0x00, 0xAB])
        encoder.writeBytes(testData)
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readBytes(), testData)
    }

    func testEmptyBytes() {
        var encoder = Encoder()
        encoder.writeBytes(Data())
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readBytes(), Data())
    }

    func testWriteEnd() {
        var encoder = Encoder()
        encoder.writeEnd()
        XCTAssertEqual(encoder.data, Data([0x00]))
    }

    func testDecoderIsAtEnd() {
        var encoder = Encoder()
        encoder.writeBool(true)
        var decoder = Decoder(encoder.data)
        XCTAssertFalse(decoder.isAtEnd)
        _ = decoder.readBool()
        XCTAssertTrue(decoder.isAtEnd)
    }

    func testDecoderEmptyData() {
        var decoder = Decoder(Data())
        XCTAssertTrue(decoder.isAtEnd)
        // Reading from empty should return defaults
        XCTAssertEqual(decoder.readVarint(), 0)
        XCTAssertFalse(decoder.readBool())
        XCTAssertEqual(decoder.readFixed64(), 0)
        XCTAssertEqual(decoder.readString(), "")
        XCTAssertEqual(decoder.readBytes(), Data())
    }

    func testNextField() {
        var encoder = Encoder()
        encoder.writeVarint(5) // field ID 5
        encoder.writeEnd()     // field ID 0 = end
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.nextField(), 5)
        XCTAssertEqual(decoder.nextField(), 0)
    }

    func testWriteFieldInt() {
        var encoder = Encoder()
        encoder.writeField(1, value: 99 as Int, type: "Int")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), 1) // field ID
        XCTAssertEqual(decoder.readSvarint(), 99)
    }

    func testWriteFieldString() {
        var encoder = Encoder()
        encoder.writeField(2, value: "test", type: "String")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), 2) // field ID
        XCTAssertEqual(decoder.readString(), "test")
    }

    func testWriteFieldBool() {
        var encoder = Encoder()
        encoder.writeField(3, value: true, type: "Boolean")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), 3) // field ID
        XCTAssertTrue(decoder.readBool())
    }

    func testWriteFieldFloat() {
        var encoder = Encoder()
        encoder.writeField(4, value: 2.718, type: "Float")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readVarint(), 4) // field ID
        XCTAssertEqual(decoder.readFixed64(), 2.718, accuracy: 0.0001)
    }

    func testFieldMaskOutOfRange() {
        let mask: [UInt8] = [0xFF] // only byte 0 set
        XCTAssertFalse(fieldMaskHas(mask, fieldID: 8))
        XCTAssertFalse(fieldMaskHas(mask, fieldID: 100))
    }

    func testFieldMaskLargeFieldID() {
        var mask: [UInt8] = []
        fieldMaskSet(&mask, fieldID: 63)
        XCTAssertTrue(fieldMaskHas(mask, fieldID: 63))
        XCTAssertFalse(fieldMaskHas(mask, fieldID: 62))
        XCTAssertEqual(mask.count, 8) // 63/8 = 7, need index 7 => 8 bytes
    }

    // MARK: - ColumnarDecoder Tests

    func testColumnarDecode2Records() {
        var enc = Encoder()
        // count = 2
        enc.writeVarint(2)
        // Column 1: fieldID=1, int values [42, -7]
        enc.writeVarint(1)
        enc.writeSvarint(42)
        enc.writeSvarint(-7)
        // Column 2: fieldID=2, string values ["hello", "world"]
        enc.writeVarint(2)
        enc.writeString("hello")
        enc.writeString("world")
        // Column 3: fieldID=3, float values [3.14, 2.718]
        enc.writeVarint(3)
        enc.writeFixed64(3.14)
        enc.writeFixed64(2.718)
        // End marker
        enc.writeEnd()

        var dec = ColumnarDecoder(data: enc.data)
        XCTAssertEqual(dec.count, 2)

        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.fieldID, 1)
        let ints = dec.readColumnInt()
        XCTAssertEqual(ints, [42, -7])

        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.fieldID, 2)
        let strings = dec.readColumnString()
        XCTAssertEqual(strings, ["hello", "world"])

        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.fieldID, 3)
        let floats = dec.readColumnFloat()
        XCTAssertEqual(floats[0], 3.14, accuracy: 0.0001)
        XCTAssertEqual(floats[1], 2.718, accuracy: 0.0001)

        XCTAssertFalse(dec.nextColumn())
    }

    func testColumnarEmptyList() {
        var enc = Encoder()
        enc.writeVarint(0) // count=0
        enc.writeEnd()     // end marker

        var dec = ColumnarDecoder(data: enc.data)
        XCTAssertEqual(dec.count, 0)
        XCTAssertFalse(dec.nextColumn())
    }

    func testColumnarNullableColumns() {
        var data = Data()
        // count = 3
        appendVarint(&data, 3)
        // Column 1: fieldID=1, nullable int [null, 99, null]
        appendVarint(&data, 1)
        data.append(0x00) // null
        data.append(0x01); appendSvarint(&data, 99) // present
        data.append(0x00) // null
        // Column 2: fieldID=2, nullable string [null, "hi", ""]
        appendVarint(&data, 2)
        data.append(0x00) // null
        data.append(0x01); appendString(&data, "hi") // present
        data.append(0x01); appendString(&data, "")   // present empty
        // End marker
        data.append(0x00)

        var dec = ColumnarDecoder(data: data)
        XCTAssertEqual(dec.count, 3)

        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.fieldID, 1)
        let nullInts = dec.readColumnIntPtr()
        XCTAssertNil(nullInts[0])
        XCTAssertEqual(nullInts[1], 99)
        XCTAssertNil(nullInts[2])

        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.fieldID, 2)
        let nullStrings = dec.readColumnStringPtr()
        XCTAssertNil(nullStrings[0])
        XCTAssertEqual(nullStrings[1], "hi")
        XCTAssertEqual(nullStrings[2], "")

        XCTAssertFalse(dec.nextColumn())
    }

    func testColumnarBoolColumn() {
        var enc = Encoder()
        enc.writeVarint(3) // count=3
        enc.writeVarint(1) // fieldID=1
        enc.writeVarint(1) // true
        enc.writeVarint(0) // false
        enc.writeVarint(1) // true
        enc.writeEnd()

        var dec = ColumnarDecoder(data: enc.data)
        XCTAssertEqual(dec.count, 3)
        XCTAssertTrue(dec.nextColumn())
        XCTAssertEqual(dec.readColumnBool(), [true, false, true])
        XCTAssertFalse(dec.nextColumn())
    }

    func testColumnarOffsetAndReadSvarint() {
        var enc = Encoder()
        enc.writeVarint(0) // count=0
        enc.writeEnd()     // end marker
        enc.writeSvarint(-42) // pagination metadata

        var dec = ColumnarDecoder(data: enc.data)
        XCTAssertFalse(dec.nextColumn())
        XCTAssertEqual(dec.readSvarint(), -42)
    }

    // MARK: - Columnar Test Helpers

    private func appendVarint(_ data: inout Data, _ value: UInt64) {
        var v = value
        while v > 0x7F {
            data.append(UInt8(v & 0x7F) | 0x80)
            v >>= 7
        }
        data.append(UInt8(v))
    }

    private func appendSvarint(_ data: inout Data, _ value: Int64) {
        let encoded = UInt64(bitPattern: (value << 1) ^ (value >> 63))
        appendVarint(&data, encoded)
    }

    private func appendString(_ data: inout Data, _ value: String) {
        let bytes = Array(value.utf8)
        appendVarint(&data, UInt64(bytes.count))
        data.append(contentsOf: bytes)
    }

    func testMultipleStrings() {
        var encoder = Encoder()
        encoder.writeString("alpha")
        encoder.writeString("beta")
        encoder.writeString("gamma")
        var decoder = Decoder(encoder.data)
        XCTAssertEqual(decoder.readString(), "alpha")
        XCTAssertEqual(decoder.readString(), "beta")
        XCTAssertEqual(decoder.readString(), "gamma")
    }
}
