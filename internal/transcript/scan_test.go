package transcript

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStrPrefix(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
		ok   bool
	}{
		{`"plain"`, -1, "plain", true},
		{`"a\nb\t\"q\"\\ \/"`, -1, "a\nb\t\"q\"\\ /", true},
		{`"é😀"`, -1, "é😀", true},
		{`"\ud83d"`, -1, "�", true},
		{`"héllo"`, 2, "h", true}, // don't split é
		{`"aéb"`, 2, "a", true},
		{`"abc"`, 0, "", true},
		{`"bad \x escape"`, -1, "", false},
		{`not a string`, -1, "", false},
		{`"unterminated`, -1, "", false},
	}
	for _, tt := range tests {
		got, ok := strPrefix([]byte(tt.in), tt.max)
		if got != tt.want || ok != tt.ok {
			t.Errorf("strPrefix(%s, %d) = %q, %v; want %q, %v", tt.in, tt.max, got, ok, tt.want, tt.ok)
		}
	}
}

func TestEachField(t *testing.T) {
	tests := []struct {
		in   string
		keys string
		ok   bool
	}{
		{`{}`, "", true},
		{` { "a" : 1 , "b":[1,{"x":"}"}], "c":"q\"}" } `, "a,b,c", true},
		{`{"a":1,}`, "a", false},
		{`{"a" 1}`, "", false},
		{`{"a":1} trailing`, "a", false},
		{`{"a":[1,2}`, "", false},
		{`[1]`, "", false},
	}
	for _, tt := range tests {
		var keys []string
		ok := eachField([]byte(tt.in), func(k, _ []byte) { keys = append(keys, string(k)) })
		if ok != tt.ok || (ok && strings.Join(keys, ",") != tt.keys) {
			t.Errorf("eachField(%s) = %v %v", tt.in, keys, ok)
		}
	}
}

// The hand-written decoder must agree with encoding/json on valid strings.
func FuzzStrMatchesEncodingJSON(f *testing.F) {
	for _, s := range []string{"", "plain", "a\nb", "é😀", "\x00\x1f", `"\`, " "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			return
		}
		b, _ := json.Marshal(s)
		got, ok := str(b)
		if !ok || got != s {
			t.Fatalf("str(%s) = %q, %v; want %q", b, got, ok, s)
		}
	})
}
