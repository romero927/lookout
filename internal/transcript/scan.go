package transcript

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

// A minimal JSON walker. Transcript lines can be megabytes (tool output,
// file contents, thinking), and Lookout needs only a few small fields, so
// instead of decoding whole lines this skips over values it doesn't need.
// It checks structure (balanced brackets, terminated strings) but is not a
// full validator; values Lookout actually uses are decoded strictly.

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func skipSpace(b []byte, i int) int {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	return i
}

// skipString returns the index just past the string starting at b[i]=='"', or -1.
func skipString(b []byte, i int) int {
	i++
	for {
		j := bytes.IndexByte(b[i:], '"')
		if j < 0 {
			return -1
		}
		q := i + j
		// Count the backslashes before the quote; an odd number escapes it.
		bs := 0
		for k := q - 1; k >= i && b[k] == '\\'; k-- {
			bs++
		}
		if bs%2 == 0 {
			return q + 1
		}
		i = q + 1
	}
}

// skipValue returns the index just past the value starting at b[i], or -1.
func skipValue(b []byte, i int) int {
	i = skipSpace(b, i)
	if i >= len(b) {
		return -1
	}
	switch c := b[i]; c {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				i = skipString(b, i)
				if i < 0 {
					return -1
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
				if depth < 0 {
					return -1
				}
			}
			i++
		}
		return -1
	default:
		// number, true, false, null
		start := i
		for i < len(b) && !isSpace(b[i]) && b[i] != ',' && b[i] != '}' && b[i] != ']' {
			i++
		}
		if i == start {
			return -1
		}
		return i
	}
}

// eachField calls fn for every key/value of the object obj. key is the raw
// key without quotes. It returns false if obj isn't a well-formed object.
func eachField(obj []byte, fn func(key, val []byte)) bool {
	i := skipSpace(obj, 0)
	if i >= len(obj) || obj[i] != '{' {
		return false
	}
	i = skipSpace(obj, i+1)
	if i < len(obj) && obj[i] == '}' {
		return skipSpace(obj, i+1) == len(obj)
	}
	for {
		if i >= len(obj) || obj[i] != '"' {
			return false
		}
		ke := skipString(obj, i)
		if ke < 0 {
			return false
		}
		key := obj[i+1 : ke-1]
		i = skipSpace(obj, ke)
		if i >= len(obj) || obj[i] != ':' {
			return false
		}
		vs := skipSpace(obj, i+1)
		ve := skipValue(obj, vs)
		if ve < 0 {
			return false
		}
		fn(key, obj[vs:ve])
		i = skipSpace(obj, ve)
		if i >= len(obj) {
			return false
		}
		if obj[i] == '}' {
			return skipSpace(obj, i+1) == len(obj)
		}
		if obj[i] != ',' {
			return false
		}
		i = skipSpace(obj, i+1)
	}
}

// eachElem calls fn for every element of the array arr. It stops quietly
// at the first structural problem; non-arrays yield nothing.
func eachElem(arr []byte, fn func(val []byte)) {
	i := skipSpace(arr, 0)
	if i >= len(arr) || arr[i] != '[' {
		return
	}
	i = skipSpace(arr, i+1)
	if i < len(arr) && arr[i] == ']' {
		return
	}
	for {
		ve := skipValue(arr, i)
		if ve < 0 {
			return
		}
		fn(arr[i:ve])
		i = skipSpace(arr, ve)
		if i >= len(arr) || arr[i] != ',' {
			return
		}
		i = skipSpace(arr, i+1)
	}
}

// str decodes a JSON string value in full. ok is false if v isn't a string.
func str(v []byte) (string, bool) { return strPrefix(v, -1) }

// strPrefix decodes at most max bytes of a JSON string (max < 0: all),
// without decoding the rest. The result never ends mid-rune.
func strPrefix(v []byte, max int) (string, bool) {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	body := v[1 : len(v)-1]
	if bytes.IndexByte(body, '\\') < 0 {
		return string(cutRune(body, max)), true
	}
	hint := len(body)
	if max >= 0 && max < hint {
		hint = max + utf8.UTFMax
	}
	out := make([]byte, 0, hint)
	for i := 0; i < len(body) && (max < 0 || len(out) < max); {
		c := body[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(body) {
			return "", false
		}
		switch body[i+1] {
		case '"', '\\', '/':
			out = append(out, body[i+1])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, n := decodeU(body[i:])
			if n == 0 {
				return "", false
			}
			out = utf8.AppendRune(out, r)
			i += n
			continue
		default:
			return "", false
		}
		i += 2
	}
	return string(cutRune(out, max)), true
}

// cutRune shortens b to at most n bytes (n < 0: no limit) without
// splitting a UTF-8 sequence.
func cutRune(b []byte, n int) []byte {
	if n < 0 || len(b) <= n {
		return b
	}
	for n > 0 && b[n]&0xC0 == 0x80 {
		n--
	}
	return b[:n]
}

// decodeU decodes \uXXXX (and a following low surrogate). It returns the
// rune and the number of bytes consumed, or n=0 if malformed.
func decodeU(b []byte) (rune, int) {
	if len(b) < 6 {
		return 0, 0
	}
	v, err := strconv.ParseUint(string(b[2:6]), 16, 32)
	if err != nil {
		return 0, 0
	}
	r := rune(v)
	if r >= 0xD800 && r < 0xDC00 && len(b) >= 12 && b[6] == '\\' && b[7] == 'u' {
		if lo, err := strconv.ParseUint(string(b[8:12]), 16, 32); err == nil && lo >= 0xDC00 && lo < 0xE000 {
			return (r-0xD800)<<10 + (rune(lo) - 0xDC00) + 0x10000, 12
		}
	}
	if r >= 0xD800 && r < 0xE000 {
		return utf8.RuneError, 6
	}
	return r, 6
}

// boolean decodes true/false.
func boolean(v []byte) (val, ok bool) {
	switch string(v) {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return false, true
	}
	return false, false
}

// integer decodes a number, a numeric string, or null; anything else is 0.
func integer(v []byte) int64 {
	s := string(v)
	if len(s) >= 2 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}
