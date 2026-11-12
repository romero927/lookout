package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source describes the file a record came from.
type Source struct {
	Path       string
	Project    string // directory name under projects/ (the dash-encoded cwd)
	SessionID  string // from the file name, or the parent session for subagents
	IsSubagent bool
}

// SourceFor derives a Source from a transcript path under root.
func SourceFor(root, path string) Source {
	s := Source{Path: path}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 0 {
		s.Project = parts[0]
	}
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	// <project>/<session>/subagents/agent-<id>.jsonl
	if len(parts) >= 4 && parts[len(parts)-2] == "subagents" {
		s.IsSubagent = true
		s.SessionID = parts[len(parts)-3]
	} else {
		s.SessionID = base
	}
	return s
}

// Tailer reads JSONL files incrementally, remembering how far it got in
// each. Only complete lines (ending in '\n') are consumed, so a line being
// written right now is picked up on the next read.
//
// Different files may be read concurrently; reads of the same file are
// serialised.
type Tailer struct {
	mu      sync.Mutex
	offsets map[string]int64
	files   map[string]os.FileInfo // what each path was when last read
	locks   map[string]*sync.Mutex
	health  Health
}

// NewTailer returns an empty Tailer.
func NewTailer() *Tailer {
	return &Tailer{offsets: make(map[string]int64), files: make(map[string]os.FileInfo), locks: make(map[string]*sync.Mutex)}
}

func (t *Tailer) fileLock(path string) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.locks[path]
	if l == nil {
		l = &sync.Mutex{}
		t.locks[path] = l
	}
	return l
}

// maxLine guards against runaway lines; longer lines are skipped as malformed.
const maxLine = 64 << 20

// Read parses any new complete lines in path and calls fn for each record.
// It returns the number of records delivered.
func (t *Tailer) Read(path string, src Source, fn func(Record, Source)) (int, error) {
	fl := t.fileLock(path)
	fl.Lock()
	defer fl.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	off := t.offsets[path]
	prev := t.files[path]
	t.files[path] = st
	t.mu.Unlock()
	if off > 0 && (st.Size() < off || (prev != nil && !os.SameFile(prev, st))) {
		// Truncated or replaced: start over. Usage dedupe makes re-reads
		// safe; the other consumers start the session over on KindReset.
		off = 0
		fn(Record{Kind: KindReset, SessionID: src.SessionID}, src)
		if !src.IsSubagent {
			t.rewindSubagents(path)
		}
	}
	if st.Size() == off {
		return 0, nil
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}

	r := bufio.NewReaderSize(f, 256<<10)
	n := 0
	var statuses []Status
	var (
		buf      []byte
		lineLen  int64
		skipping bool // current line exceeded maxLine; discard it
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			lineLen += int64(len(chunk))
			if !skipping {
				buf = append(buf, chunk...)
				if len(buf) > maxLine {
					skipping, buf = true, buf[:0]
				}
			}
			continue
		}
		if err != nil {
			// EOF with a partial line: leave it for next time.
			break
		}
		lineLen += int64(len(chunk))
		off += lineLen
		if skipping {
			statuses = append(statuses, StatusMalformed)
		} else {
			buf = append(buf, chunk...)
			rec, status := ParseLine(buf)
			statuses = append(statuses, status)
			if status != StatusBlank && status != StatusMalformed {
				fn(rec, src)
				n++
			}
		}
		buf, lineLen, skipping = buf[:0], 0, false
	}
	t.mu.Lock()
	t.offsets[path] = off
	for _, s := range statuses {
		t.health.observe(s, path, "")
	}
	t.mu.Unlock()
	return n, nil
}

// Offset returns how far path has been read (-1 if never).
// rewindSubagents makes a main transcript's subagent files read again from
// the start, since a reset drops what was learned from them too.
func (t *Tailer) rewindSubagents(mainPath string) {
	dir := strings.TrimSuffix(mainPath, ".jsonl") + string(filepath.Separator) + "subagents" + string(filepath.Separator)
	t.mu.Lock()
	defer t.mu.Unlock()
	for p := range t.offsets {
		if strings.HasPrefix(p, dir) {
			t.offsets[p] = 0
			delete(t.files, p)
		}
	}
}

func (t *Tailer) Offset(path string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	off, ok := t.offsets[path]
	if !ok {
		return -1
	}
	return off
}

// Forget drops the stored offset for path.
func (t *Tailer) Forget(path string) {
	t.mu.Lock()
	delete(t.offsets, path)
	t.mu.Unlock()
}

// Health returns a copy of the parse health counters.
func (t *Tailer) Health() Health {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := t.health
	h.recent = append([]Status(nil), t.health.recent...)
	return h
}

// Health tracks parse failures, overall and over the most recent lines.
type Health struct {
	Lines     int64
	Malformed int64
	Degraded  int64
	LastFile  string
	LastAt    time.Time
	recent    []Status // ring of the last recentWindow statuses
	next      int
}

const recentWindow = 500

func (h *Health) observe(s Status, path, _ string) {
	if s == StatusBlank {
		return
	}
	h.Lines++
	switch s {
	case StatusMalformed:
		h.Malformed++
	case StatusDegraded:
		h.Degraded++
	}
	if s == StatusMalformed || s == StatusDegraded {
		h.LastFile = path
		h.LastAt = time.Now()
	}
	if len(h.recent) < recentWindow {
		h.recent = append(h.recent, s)
	} else {
		h.recent[h.next] = s
		h.next = (h.next + 1) % recentWindow
	}
}

// Recent returns (failed, total) over the most recent lines.
func (h Health) Recent() (failed, total int) {
	for _, s := range h.recent {
		if s == StatusMalformed || s == StatusDegraded {
			failed++
		}
	}
	return failed, len(h.recent)
}

// Warning returns a user-facing warning when recent lines are failing to
// parse, or "" when parsing looks healthy. Isolated bad lines are tolerated.
func (h Health) Warning() string {
	failed, total := h.Recent()
	if total == 0 || failed == 0 {
		return ""
	}
	if failed < 5 && float64(failed)/float64(total) < 0.02 {
		return ""
	}
	return fmt.Sprintf("parser: %d of the last %d transcript lines failed to parse — the Claude Code transcript format may have changed; token figures may be incomplete", failed, total)
}

// IsJSONL reports whether path looks like a transcript file.
func IsJSONL(path string) bool { return strings.HasSuffix(path, ".jsonl") }

// ReadAll parses a whole file (used by the hook and by tests).
func ReadAll(path string, fn func(Record)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		rec, st := ParseLine(line)
		if st == StatusBlank || st == StatusMalformed {
			continue
		}
		fn(rec)
	}
	return nil
}
