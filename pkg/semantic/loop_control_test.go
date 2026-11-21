package semantic

import "testing"

func TestLoopControlRequiresReachableLoopScope(t *testing.T) {
	for _, keyword := range []string{"break", "continue"} {
		for _, sample := range []struct{ body, message string }{
			{keyword, "loop scope"},
			{"when { true -> { " + keyword + " } else -> {} }", "loop scope"},
			{"for item in 0..1 { async { " + keyword + " } }", "loop scope"},
			{"for item in 0..1 { transaction { " + keyword + " } }", "loop scope"},
			{"for item in 0..1 { [1].forEach { value -> " + keyword + " } }", "loop scope"},
			{"for item in 0..1 { val result = when { true -> { " + keyword + " } else -> 1 } }", "when branch"},
		} {
			t.Run(keyword+"/"+sample.body, func(t *testing.T) {
				result := analyze(t, "api probe(): Int { "+sample.body+" return 1 }")
				expectError(t, result, sample.message)
			})
		}
	}
}

func TestLoopControlRetainsStatementWhenScope(t *testing.T) {
	for _, body := range []string{
		"for item in 0..4 { when { item == 2 -> { break } else -> { continue } } }",
		"for item in 0..4 { for inner in 0..4 { when { inner == 2 -> { break } else -> { continue } } } }",
		"async { for item in 0..4 { when { item == 2 -> { break } else -> { continue } } } }",
		"transaction { for item in 0..4 { when { item == 2 -> { break } else -> { continue } } } }",
		"[1].forEach { value -> for item in 0..4 { when { item == 2 -> { break } else -> { continue } } } }",
	} {
		t.Run(body, func(t *testing.T) {
			expectNoErrors(t, analyze(t, "api probe(): Int { "+body+" return 1 }"))
		})
	}
}

func TestConditionalLoopRequiresBoolean(t *testing.T) {
	for _, sample := range []struct{ param, condition string }{
		{"", "1"}, {"", "\"text\""}, {"", "[true]"}, {"flag: Boolean?", "flag"},
	} {
		t.Run(sample.condition, func(t *testing.T) {
			result := analyze(t, "api probe("+sample.param+"): Int { for "+sample.condition+" {} return 1 }")
			expectError(t, result, "for condition requires non-null Boolean")
		})
	}
}

func TestNestedWhenCannotHideCollectionLoopEscape(t *testing.T) {
	for _, keyword := range []string{"break", "continue"} {
		result := analyze(t, "api probe(): Int { for item in 0..1 { [1].forEach { value -> when { true -> { "+keyword+" } else -> {} } } } return 1 }")
		expectError(t, result, "loop scope")
	}
}
