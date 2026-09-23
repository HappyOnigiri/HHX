package pycompat

import "testing"

// 期待値は Python 3.14 の os.path で作った。
func TestExpanduser(t *testing.T) {
	t.Setenv("HOME", "/Users/alice/")
	for _, testCase := range []struct{ input, want string }{
		{"~", "/Users/alice"},
		{"~/a", "/Users/alice/a"},
		{"~alice-no-such-user/x", "~alice-no-such-user/x"},
		{"a~", "a~"},
		{"~/", "/Users/alice/"},
		{"/x", "/x"},
	} {
		if got := Expanduser(testCase.input); got != testCase.want {
			t.Errorf("Expanduser(%q)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
	t.Setenv("HOME", "/")
	for _, testCase := range []struct{ input, want string }{
		{"~", "/"},
		{"~/a", "/a"},
	} {
		if got := Expanduser(testCase.input); got != testCase.want {
			t.Errorf("HOME=/: Expanduser(%q)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestExpandvars(t *testing.T) {
	t.Setenv("HHX_X", "val")
	for _, testCase := range []struct{ input, want string }{
		{"$HHX_X", "val"},
		{"${HHX_X}", "val"},
		{"$HHX_X/y", "val/y"},
		{"a$HHX_Xb", "a$HHX_Xb"},
		{"${HHX_X", "${HHX_X"},
		{"$HHX_MISSING", "$HHX_MISSING"},
		{"${}", "${}"},
		{"$", "$"},
		{"$$HHX_X", "$val"},
		{"${HHX_X}${HHX_X}", "valval"},
		{"x$1", "x$1"},
	} {
		if got := Expandvars(testCase.input); got != testCase.want {
			t.Errorf("Expandvars(%q)=%q, want %q", testCase.input, got, testCase.want)
		}
	}
}

func TestRStrip(t *testing.T) {
	if got := RStrip(" a \u3000\n"); got != " a" {
		t.Errorf("RStrip=%q", got)
	}
}
