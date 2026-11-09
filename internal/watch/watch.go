// Package watch follows Claude Code's transcript directory and feeds new
// records to consumers. fsnotify isn't recursive, so every directory under
// the root is watched individually; a slow poll backs it up in case an
// event is missed.
package watch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/romero927/lookout/internal/transcript"
)

// Handler receives each parsed record.
type Handler func(transcript.Record, transcript.Source)

// Watcher tails every transcript under Root.
type Watcher struct {
	Root     string
	MaxAge   time.Duration // skip files not modified within MaxAge on the first scan (0 = all)
	Handle   Handler
	OnChange func() // called (debounced) after new records arrive
	Poll     time.Duration

	tailer  *transcript.Tailer
	mu      sync.Mutex
	known   map[string]bool
	skipped map[string]time.Time // too old on the first scan; read only if modified later
}

// New creates a Watcher.
func New(root string, maxAge time.Duration, h Handler, onChange func()) *Watcher {
	return &Watcher{
		Root: root, MaxAge: maxAge, Handle: h, OnChange: onChange,
		Poll: 5 * time.Second, tailer: transcript.NewTailer(), known: map[string]bool{},
		skipped: map[string]time.Time{},
	}
}

// Health exposes the parser health counters.
func (w *Watcher) Health() transcript.Health { return w.tailer.Health() }

// Scan reads every transcript once (oldest first) and returns the number of
// records delivered. It is used for the initial load and by `lookout stats`.
func (w *Watcher) Scan() int {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	cutoff := time.Time{}
	if w.MaxAge > 0 {
		cutoff = time.Now().Add(-w.MaxAge)
	}
	_ = filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() || !transcript.IsJSONL(p) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished since listing; skip
		}
		w.mu.Lock()
		seen := w.known[p]
		w.known[p] = true
		if !seen && info.ModTime().Before(cutoff) {
			w.skipped[p] = info.ModTime()
			w.mu.Unlock()
			return nil
		}
		w.mu.Unlock()
		files = append(files, file{p, info.ModTime()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })

	// Files are independent, so parse them in parallel. Order within a file
	// is preserved, which is all the consumers rely on.
	var (
		total atomic.Int64
		wg    sync.WaitGroup
		next  = make(chan string)
	)
	for range max(1, min(runtime.NumCPU(), 8)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range next {
				total.Add(int64(w.read(p)))
			}
		}()
	}
	for _, f := range files {
		next <- f.path
	}
	close(next)
	wg.Wait()
	return int(total.Load())
}

func (w *Watcher) read(p string) int {
	n, _ := w.tailer.Read(p, transcript.SourceFor(w.Root, p), w.Handle)
	return n
}

// Run watches until ctx is cancelled. Call Scan first.
func (w *Watcher) Run(ctx context.Context) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return w.pollOnly(ctx)
	}
	defer fw.Close()
	w.addDirs(fw, w.Root)

	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	go w.debounce(ctx, changed)

	poll := time.NewTicker(w.Poll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-fw.Events:
			if !ok {
				return w.pollOnly(ctx)
			}
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					w.addDirs(fw, ev.Name)
					if w.scanNew() > 0 {
						notify()
					}
					continue
				}
			}
			if (ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create)) && transcript.IsJSONL(ev.Name) {
				w.mu.Lock()
				w.known[ev.Name] = true
				delete(w.skipped, ev.Name)
				w.mu.Unlock()
				if w.read(ev.Name) > 0 {
					notify()
				}
			}
		case _, ok := <-fw.Errors:
			// Keep going; the poll covers anything missed.
			if !ok {
				return w.pollOnly(ctx)
			}
		case <-poll.C:
			if w.scanNew() > 0 {
				notify()
			}
		}
	}
}

// scanNew re-reads every file (cheap when nothing changed: a stat each).
func (w *Watcher) scanNew() int {
	n := 0
	_ = filepath.WalkDir(w.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !transcript.IsJSONL(p) {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if w.isStale(p) {
			return nil
		}
		// Unchanged size: nothing new, don't even open it.
		if info, err := d.Info(); err == nil && info.Size() == w.tailer.Offset(p) {
			return nil
		}
		w.mu.Lock()
		w.known[p] = true
		w.mu.Unlock()
		n += w.read(p)
		return nil
	})
	return n
}

// isStale reports whether p was skipped as too old and hasn't changed since.
func (w *Watcher) isStale(p string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	mod, ok := w.skipped[p]
	if !ok {
		return false
	}
	if fi, err := os.Stat(p); err == nil && fi.ModTime().Equal(mod) {
		return true
	}
	delete(w.skipped, p)
	return false
}

func (w *Watcher) addDirs(fw *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = fw.Add(p)
		}
		return nil
	})
}

func (w *Watcher) debounce(ctx context.Context, changed <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			time.Sleep(150 * time.Millisecond)
			if w.OnChange != nil {
				w.OnChange()
			}
		}
	}
}

func (w *Watcher) pollOnly(ctx context.Context) error {
	t := time.NewTicker(w.Poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if w.scanNew() > 0 && w.OnChange != nil {
				w.OnChange()
			}
		}
	}
}
