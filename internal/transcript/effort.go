package transcript

import (
	"bytes"
	"io"
	"os"
)

// effortTail is how much of the end of a transcript LastEffort reads.
const effortTail = 512 << 10

// LastEffort returns the /effort level of the latest main-chain response in
// the transcript at path, or "" when none is recorded in its last 512 KB.
// It reads only the end of the file, so it is cheap enough for the status
// line.
func LastEffort(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(0, info.Size()-effortTail)
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	if off > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	for len(buf) > 0 {
		i := bytes.LastIndexByte(bytes.TrimRight(buf, "\n"), '\n')
		line := buf[i+1:]
		buf = buf[:max(0, i)]
		if !bytes.Contains(line, []byte(`"effort"`)) {
			continue
		}
		if r, _ := ParseLine(bytes.TrimSpace(line)); r.Kind == KindAssistant && !r.Sidechain && r.Effort != "" {
			return r.Effort
		}
		if i < 0 {
			break
		}
	}
	return ""
}
