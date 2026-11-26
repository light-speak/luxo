package sqllex

import "testing"

func TestSkipComment(t *testing.T) {
	for _, sample := range []struct {
		input, comment string
	}{
		{"/", ""}, {"/ 4", ""}, {"-4", ""},
		{"-- remainder", "-- remainder"}, {"-- first\rsecond", "-- first\r"},
		{"/* unfinished", "/* unfinished"}, {"/* outer /* inner */ end */rest", "/* outer /* inner */ end */"},
	} {
		end, found := SkipComment(sample.input, 0)
		if end != len(sample.comment) || found != (sample.comment != "") {
			t.Errorf("SkipComment(%q) = %d, %t, want %q", sample.input, end, found, sample.comment)
		}
	}
}

func TestSkipLiteral(t *testing.T) {
	for _, sample := range []struct {
		input, prefix, literal string
	}{
		{"name", "", ""}, {"$tag+value", "", ""}, {"$tag$unfinished", "", "$tag$unfinished"},
		{"$tag$closed$tag$ + 1", "", "$tag$closed$tag$"}, {"name$tag$more", "name", ""},
		{"'unfinished", "", "'unfinished"}, {"'Kit ''A''' + 1", "", "'Kit ''A'''"},
		{"E'a'", "E", "'a'"}, {"E'a' 'b'", "E", "'a'"}, {"E'a' - $1", "E", "'a'"},
		{"E'a'  -- note\r\n 'b\\'c' + $1", "E", "'a'  -- note\r\n 'b\\'c'"},
		{"identifierE'\\' + $1", "identifierE", "'\\'"},
	} {
		end, found := SkipLiteral(sample.input, len(sample.prefix))
		if end != len(sample.prefix)+len(sample.literal) || found != (sample.literal != "") {
			t.Errorf("SkipLiteral(%q) = %d, %t, want %q", sample.input, end, found, sample.literal)
		}
	}
}
