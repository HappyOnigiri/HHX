package toolresponse

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, raw string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSucceeded(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `[]`, `"x"`,
		`{"stdout": "", "stderr": " * [new branch]      feature -> feature"}`,
		`{"exit_code": 0, "stderr": "To github.com:owner/repo.git"}`,
		`{"exit_code": true, "code": false}`,
		`{"exitCode": 1.0}`,
		`{"code": "1"}`,
		`{"success": true, "interrupted": false}`,
		`{"exit_code": -0}`,
	} {
		if !Succeeded(decode(t, raw)) {
			t.Errorf("%s must be a success", raw)
		}
	}
	for _, raw := range []string{
		`{"exitCode": 128}`,
		`{"returnCode": -1}`,
		`{"return_code": 2}`,
		`{"code": 99999999999999999999999}`,
		`{"success": false}`,
		`{"interrupted": true}`,
		`{"stderr": "fatal: repository not found"}`,
		`{"aggregated_output": "error: failed to push some refs"}`,
		`{"stdout": "Everything up to date"}`,
		`{"output": "EVERYTHING UP-TO-DATE"}`,
		`{"stderr": ["Permission denied"]}`,
		`{"stdout": {"x": "! [rejected]"}}`,
	} {
		if Succeeded(decode(t, raw)) {
			t.Errorf("%s must be a failure", raw)
		}
	}
}

func TestText(t *testing.T) {
	for raw, want := range map[string]string{
		`{"stdout": "a", "stderr": "b", "output": "c", "aggregated_output": "d"}`: "a b c d",
		`{"stderr": "b"}`: " b  ",
		`{"stdout": 0, "stderr": false, "output": []}`:                               "   ",
		`{"stdout": 12, "stderr": 1.5, "output": true, "aggregated_output": null}`:   "12 1.5 True ",
		`{"stdout": [1, "a", null, {"k": "v's"}]}`:                                   `[1, 'a', None, {'k': "v's"}]   `,
		`{"stdout": {"b": 1, "a": "\n\u0001é　😀"}}`:                                   `{'a': '\n\x01é\u3000😀', 'b': 1}   `,
		`{"stdout": 1e20, "stderr": 1e-5, "output": 100.0, "aggregated_output": -0}`: "1e+20 1e-05 100.0 ",
		`[]`: "",
	} {
		if got := Text(decode(t, raw)); got != want {
			t.Errorf("Text(%s)=%q, want %q", raw, got, want)
		}
	}
}

func TestPyStr(t *testing.T) {
	for raw, want := range map[string]string{
		`"it's \"x\""`: `it's "x"`,
		`["it's"]`:     `["it's"]`,
		`["a\"b'"]`:    `['a"b\'']`,
		`[" \\ \t"]`:   `[' \\ \t']`,
		`-12`:          "-12",
		`0.1`:          "0.1",
		`1e400`:        "1e400",
		`{}`:           "{}",
	} {
		if got := PyStr(decode(t, raw)); got != want {
			t.Errorf("PyStr(%s)=%q, want %q", raw, got, want)
		}
	}
	if got := PyStr(struct{}{}); got != "" {
		t.Errorf("unknown types: %q", got)
	}
	if !Truthy(struct{}{}) || Truthy(decode(t, `""`)) || Truthy(decode(t, `0.0`)) || !Truthy(decode(t, `[0]`)) {
		t.Error("Truthy does not follow Python")
	}
}
