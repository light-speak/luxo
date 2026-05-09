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
}
