package com.luxo.client

internal fun requireJSONContainerBudget(remaining: Int) {
    require(remaining > 0) { "JSON nesting exceeds 128 levels" }
}

/** Validate one complete value without materializing another JSON tree. */
internal fun requireJSONTokens(text: String, maxDepth: Int = Int.MAX_VALUE): String {
    var offset = 0
    while (offset < text.length && text[offset] in " \t\n\r") offset++
    require(offset < text.length) { "Incomplete JSON value" }
    if (text[offset] == '[' || text[offset] == '{') {
        JSONSyntax(text).validate(maxDepth)
    } else {
        offset = if (text[offset] == '"') jsonQuotedTokenEnd(text, offset) else jsonLiteralEnd(text, offset)
        while (offset < text.length && text[offset] in " \t\n\r") offset++
        require(offset == text.length) { "Trailing JSON content" }
    }
    return text
}

private class JSONSyntax(private val text: String) {
    private var offset = 0
    private var state = 'v'
    private var depth = 0
    private var parents = 0L
    private var overflow: StringBuilder? = null

    fun validate(maxDepth: Int) {
        while (offset < text.length) {
            val char = text[offset]
            if (char in " \t\n\r") { offset++; continue }
            when (state) {
                'v', 'a', 'b', 'o' -> value(char, maxDepth)
                'k', 'l' -> key(char)
                ':' -> { require(char == ':') { "Missing JSON colon" }; offset++; state = 'o' }
                ']', '}' -> separator(char)
                else -> throw IllegalArgumentException("Trailing JSON content")
            }
        }
        require(state == 'e' && depth == 0) { "Incomplete JSON value" }
    }

    private fun value(char: Char, maxDepth: Int) {
        if (state == 'a' && char == ']') { close(); return }
        val after = when (state) { 'a', 'b' -> ']'; 'o' -> '}'; else -> 'e' }
        when (char) {
            '[', '{' -> {
                push(after, maxDepth)
                state = if (char == '[') 'a' else 'k'
                offset++
            }
            '"' -> { offset = jsonQuotedTokenEnd(text, offset); state = after }
            else -> { offset = jsonLiteralEnd(text, offset); state = after }
        }
    }

    private fun key(char: Char) {
        if (state == 'k' && char == '}') { close(); return }
        require(char == '"') { "JSON object key must be quoted" }
        offset = jsonQuotedTokenEnd(text, offset)
        state = ':'
    }

    private fun separator(char: Char) {
        if (char == state) { close(); return }
        require(char == ',') { "Missing JSON separator" }
        state = if (state == ']') 'b' else 'l'
        offset++
    }

    // A parent needs one bit: array or object. The root is identified by depth.
    private fun push(after: Char, maxDepth: Int) {
        requireJSONContainerBudget(maxDepth - depth)
        if (depth < Long.SIZE_BITS) {
            if (after == '}') parents = parents or (1L shl depth)
            else parents = parents and (1L shl depth).inv()
        } else {
            val stack = overflow ?: StringBuilder().also { overflow = it }
            stack.append(after)
        }
        depth++
    }

    private fun close() {
        depth--
        state = when {
            depth == 0 -> 'e'
            depth < Long.SIZE_BITS -> if (parents and (1L shl depth) == 0L) ']' else '}'
            else -> {
                val stack = requireNotNull(overflow)
                val parent = stack[stack.lastIndex]
                stack.setLength(stack.length - 1)
                parent
            }
        }
        offset++
    }
}

internal fun jsonQuotedTokenEnd(text: String, start: Int): Int {
    var offset = start + 1
    var quote = text.indexOf('"', offset)
    require(quote >= 0) { "Unterminated JSON string" }
    while (offset < quote) {
        val char = text[offset++]
        require(char >= ' ') { "Unescaped JSON control character" }
        if (char == '\\') {
            offset = jsonEscapeEnd(text, offset)
            if (offset > quote) {
                quote = text.indexOf('"', offset)
                require(quote >= 0) { "Unterminated JSON string" }
            }
        }
    }
    return quote + 1
}

private fun jsonEscapeEnd(text: String, start: Int): Int {
    require(start < text.length) { "Incomplete JSON escape" }
    if (text[start] != 'u') {
        require(text[start] in "\"\\/bfnrt") { "Invalid JSON escape" }
        return start + 1
    }
    require(text.length - start >= 5) { "Incomplete JSON Unicode escape" }
    for (index in start + 1..start + 4) {
        val char = text[index]
        require(char in '0'..'9' || char in 'a'..'f' || char in 'A'..'F') { "Invalid JSON Unicode escape" }
    }
    return start + 5
}

internal fun jsonLiteralEnd(text: String, start: Int): Int {
    if (text.startsWith("null", start) || text.startsWith("true", start)) return start + 4
    if (text.startsWith("false", start)) return start + 5
    var offset = start
    if (text[offset] == '-') offset++
    require(offset < text.length) { "Invalid JSON number" }
    offset = if (text[offset] == '0') offset + 1 else jsonDigitsEnd(text, offset)
    if (offset < text.length && text[offset] == '.') offset = jsonDigitsEnd(text, offset + 1)
    if (offset < text.length && (text[offset] == 'e' || text[offset] == 'E')) {
        offset++
        if (offset < text.length && (text[offset] == '+' || text[offset] == '-')) offset++
        offset = jsonDigitsEnd(text, offset)
    }
    return offset
}

private fun jsonDigitsEnd(text: String, start: Int): Int {
    var offset = start
    while (offset < text.length && text[offset] in '0'..'9') offset++
    require(offset > start) { "Invalid JSON number" }
    return offset
}
