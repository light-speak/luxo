/** An unrecognized response member, retaining its enum identity and wire text. */
export class UnknownEnumValue<Name extends string = string> {
  readonly kind = 'unknown'

  constructor(readonly enumName: Name, readonly rawValue: string) {
    if (typeof enumName !== 'string' || typeof rawValue !== 'string') {
      throw new TypeError('unknown enum values require an enum name and string value')
    }
    Object.freeze(this)
  }
}

/** Only declared Enum paths unwrap these values; arbitrary objects are not enums. */
export function enumRawValue(value: unknown, enumName?: string): string {
  if (typeof value === 'string') return value
  if (value instanceof UnknownEnumValue && (!enumName || value.enumName === enumName)) return value.rawValue
  throw new TypeError(`expected ${enumName || 'enum'} string or matching UnknownEnumValue`)
}
