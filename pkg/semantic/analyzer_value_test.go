package semantic

import "testing"

func TestStaticNullWhenResolvesTypePatterns(t *testing.T) {
	for _, subject := range []string{"null", "await { null }"} {
		t.Run(subject, func(t *testing.T) {
			base := "fn probe(): Int { return when (" + subject + ") { is "
			end := " -> 1 null -> 2 else -> 3 } }"
			expectNoErrors(t, analyze(t, "model Entry @noTime { id: Int @id }\n"+base+"Entry"+end))
			expectError(t, analyze(t, base+"MissingEntry"+end), "MissingEntry")
		})
	}
}

func TestWhenRejectsIncompatibleGenericBlocks(t *testing.T) {
	for _, branches := range []string{
		`return when { flag -> { left } else -> { right } }`,
		`return when { flag -> { val item = left item } else -> { val item = right item } }`,
	} {
		expectError(t, analyze(t, `fn choose(flag: Boolean, left: Channel<Int>, right: Channel<String>): Channel<Int> { `+branches+` }`), "compatible types")
	}
}

func TestWhenBlocksPreserveDatabaseContext(t *testing.T) {
	for _, body := range []string{
		`when { flag -> { Entry.find(id: 1) } else -> {} } return 0`,
		`return when { flag -> { Entry.find(id: 1).id } else -> { 0 } }`,
		`val row = when { flag -> { Entry.find(id: 1) } else -> { Entry.find(id: 2) } } return row.id`,
		`when { flag -> { when { true -> { Entry.find(id: 1) } else -> {} } } else -> {} } return 0`,
		`val channel: Channel<Int> = Channel(1) channel <- 1 return when { <-channel -> { key -> Entry.find(id: key).id } else -> 0 }`,
		`transaction { when { flag -> { Entry.find(id: 1) } else -> {} } } return 0`,
	} {
		t.Run(body, func(t *testing.T) {
			source := "model Entry @noTime { id: Int @id }\napi probe(flag: Boolean): Int { " + body + " }"
			expectNoErrors(t, analyze(t, source))
		})
	}
}

func TestWhenAndTransactionRetainCollectionQueryRestriction(t *testing.T) {
	for _, body := range []string{
		`values.forEach { value -> when { flag -> { Entry.find(id: value) } else -> {} } }`,
		`values.forEach { value -> transaction { Entry.find(id: value) } }`,
		`values.forEach { value -> transaction { when { flag -> { Entry.find(id: value) } else -> {} } } }`,
		`values.forEach { value -> when { flag -> { transaction { Entry.find(id: value) } } else -> {} } }`,
	} {
		t.Run(body, func(t *testing.T) {
			source := "model Entry @noTime { id: Int @id }\napi probe(flag: Boolean, values: [Int]): Int { " + body + " return 0 }"
			expectError(t, analyze(t, source), "inside collection lambda")
		})
	}
}

func TestValueControlFlowRequiresResult(t *testing.T) {
	for _, source := range []string{
		`fn broken(flag: Boolean): Int { return when { flag -> 1 else -> { val x = 2 } } }`,
		`fn broken(flag: Boolean): Int { return when { flag -> {} else -> 2 } }`,
		`fn broken(flag: Boolean): Int { return when { flag -> { if flag { return 1 } } else -> 2 } }`,
		`fn broken(items: [Int]): [Int] { return for item in items { val x = item } }`,
		`fn broken(items: [Int]): [Int] { return for item in items {} }`,
		`fn broken(items: [Int]): [Int] { return for item in items { when { item > 0 -> item else -> { val x = 2 } } } }`,
		`fn broken(): Int { val ch: Channel<Int> = Channel(1) return when { <-ch -> { value -> val x = value } else -> 0 } }`,
		`fn broken(flag: Boolean): Int { val ch: Channel<Int> = Channel(1) return when { flag -> ch.close() else -> 0 } }`,
		`fn broken(flag: Boolean): Int { val ch: Channel<Int> = Channel(1) return when { flag -> { ch.close() } else -> 0 } }`,
		`fn broken(items: [Int]): [Int] { val ch: Channel<Int> = Channel(1) return for item in items { ch.close() } }`,
		`fn broken(flag: Boolean): Int { return when { flag -> { var value = 0 value = 1 } else -> 2 } }`,
		`api broken(flag: Boolean): Int { when { flag -> 1 else -> { val x = 2 } } }`,
		`fn broken(flag: Boolean): Int { val x = when { flag -> 1 else -> { val y = 2 } } return x }`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, source), "must produce a value") })
	}
}

func TestValueControlFlowPreservesResultsAndStatements(t *testing.T) {
	for _, source := range []string{
		`error Failed { code: 400 message: "failed" } fn good(flag: Boolean): Int { return when { flag -> { throw Failed } else -> 2 } }`,
		`fn good(flag: Boolean): Int { return when { flag -> { val x = 1 x } else -> { return 2 } } }`,
		`fn good(flag: Boolean): Int { return when { flag -> { return 1 val x = 2 } else -> 2 } }`,
		`fn good(items: [Int]): [Int] { return for item in items { val x = item x + 1 } }`,
		`fn good(items: [Int]): Int? { return for item in items { if item > 0 { yield item } } }`,
		`fn good(items: [Int]): Int { for item in items { val x = item } return 1 }`,
		`fn good(flag: Boolean): Int { when { flag -> { val x = 1 } else -> { val x = 2 } } return 1 }`,
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, source)) })
	}
}

func TestWhenBlockResultsHaveConcreteTypes(t *testing.T) {
	expectError(t, analyze(t, `fn broken(flag: Boolean): Int { return when { flag -> { "bad" } else -> 2 } }`), "compatible types")
	expectError(t, analyze(t, `fn broken(flag: Boolean): Int { return when { flag -> { if flag { return "bad" } 1 } else -> 2 } }`), "return expects")
}
