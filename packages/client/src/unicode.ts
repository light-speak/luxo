/** Reject isolated UTF-16 code units before a native UTF-8 encoder can replace them. */
export function assertUnicode(text: string): void {
  if (!text.isWellFormed()) throw new TypeError('String must contain complete Unicode characters')
}

/** Check Unicode only; the native JSON parser remains responsible for JSON syntax. */
export function assertJSONUnicode(text: string): void {
  assertUnicode(text)
  for (let slash = text.indexOf('\\'); slash >= 0; slash = text.indexOf('\\', slash)) {
    if (text.charCodeAt(slash + 1) !== 117) {
      slash += 2
      continue
    }
    const kind = surrogateKind(text, slash + 2)
    if (kind === 2) throw new TypeError('JSON contains an isolated Unicode surrogate')
    if (kind === 1) {
      if (text.charCodeAt(slash + 6) !== 92 || text.charCodeAt(slash + 7) !== 117
        || surrogateKind(text, slash + 8) !== 2) {
        throw new TypeError('JSON contains an isolated Unicode surrogate')
      }
      slash += 12
    } else slash += 6
  }
}

// For valid \uXXXX syntax, the leading two hex digits determine its surrogate
// range. Invalid/truncated hex is rejected by JSON.parse, not repaired here.
function surrogateKind(text: string, offset: number): number {
  if ((text.charCodeAt(offset) | 32) !== 100) return 0
  const second = text.charCodeAt(offset + 1) | 32
  if (second >= 56 && second <= 57 || second >= 97 && second <= 98) return 1
  return second >= 99 && second <= 102 ? 2 : 0
}
