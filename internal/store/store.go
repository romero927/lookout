// Package store is Lookout's SQLite log (pure Go, no cgo).
//
// It records each Strategy suggestion and the model the user actually used
// next, so the thresholds can be tuned against real behaviour.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/romero927/lookout/internal/strategy"
)

// Store wraps the database.
type Store struct {
	db   *sql.DB
	last map[string]string // session -> fingerprint of last logged suggestion
}

const schema = `
CREATE TABLE IF NOT EXISTS suggestions (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	ts_ms           INTEGER NOT NULL,
	session_id      TEXT NOT NULL,
	current_model   TEXT NOT NULL,
	suggested_model TEXT NOT NULL,
	action          TEXT NOT NULL,
	reason          TEXT NOT NULL,
	signals         TEXT NOT NULL,
	context_tokens  INTEGER NOT NULL,
	next_model      TEXT,
	next_ts_ms      INTEGER
);
CREATE INDEX IF NOT EXISTS suggestions_session ON suggestions(session_id, ts_ms);
`

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("update %s: %w", path, err)
	}
	return &Store{db: db, last: map[string]string{}}, nil
}

// migrate adds columns introduced after the first release.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(suggestions)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	if !have["outcome"] {
		if _, err := db.Exec(`ALTER TABLE suggestions ADD COLUMN outcome TEXT`); err != nil {
			return err
		}
	}
	return nil
}

// Outcomes of a followed suggestion, judged on the turns after the switch.
const (
	OutcomeSmooth = "smooth"
	OutcomeRough  = "rough"
)

// Followed is a followed upgrade/downgrade whose outcome isn't known yet.
type Followed struct {
	ID        int64
	NextModel string
	NextAt    time.Time
}

// AwaitingOutcome lists followed upgrade/downgrade suggestions in a session
// that have no outcome yet.
func (s *Store) AwaitingOutcome(sessionID string) ([]Followed, error) {
	rows, err := s.db.Query(`SELECT id, current_model, suggested_model, action, next_model, next_ts_ms FROM suggestions
		WHERE session_id = ? AND action != 'keep' AND next_model IS NOT NULL AND outcome IS NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Followed
	for rows.Next() {
		var o Followed
		var r Row
		var ms int64
		if err := rows.Scan(&o.ID, &r.Current, &r.Suggested, &r.Action, &r.NextModel, &ms); err != nil {
			return nil, err
		}
		if f := r.Followed(); f != nil && *f {
			o.NextModel, o.NextAt = r.NextModel, time.UnixMilli(ms)
			out = append(out, o)
		}
	}
	return out, rows.Err()
}

// SetOutcome records how a followed suggestion went.
func (s *Store) SetOutcome(id int64, outcome string) error {
	_, err := s.db.Exec(`UPDATE suggestions SET outcome = ? WHERE id = ?`, outcome, id)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// fingerprint identifies a recommendation. The reason text is left out so
// changing counts ("5 failing results" -> "6") don't log a new row.
func fingerprint(sg strategy.Suggestion) string {
	return string(sg.Action) + "|" + sg.Current + "|" + sg.Suggested
}

// LogSuggestion records sg unless it repeats the last suggestion logged for
// the same session. It reports whether a row was written.
func (s *Store) LogSuggestion(sg strategy.Suggestion) (bool, error) {
	if sg.SessionID == "" || sg.Current == "" {
		return false, nil
	}
	fp := fingerprint(sg)
	if prev, ok := s.last[sg.SessionID]; ok && prev == fp {
		return false, nil
	}
	if _, ok := s.last[sg.SessionID]; !ok {
		// First time this run: compare against the database.
		var a, cur, sug string
		err := s.db.QueryRow(`SELECT action, current_model, suggested_model FROM suggestions
			WHERE session_id = ? ORDER BY ts_ms DESC, id DESC LIMIT 1`, sg.SessionID).Scan(&a, &cur, &sug)
		if err == nil && a+"|"+cur+"|"+sug == fp {
			s.last[sg.SessionID] = fp
			return false, nil
		}
	}
	sigs, _ := json.Marshal(sg.Signals)
	_, err := s.db.Exec(`INSERT INTO suggestions
		(ts_ms, session_id, current_model, suggested_model, action, reason, signals, context_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sg.At.UnixMilli(), sg.SessionID, sg.Current, sg.Suggested, string(sg.Action), sg.Reason, string(sigs), sg.Context.Tokens)
	if err != nil {
		return false, err
	}
	s.last[sg.SessionID] = fp
	return true, nil
}

// ResolveNext fills in the model used for the first response after each
// open suggestion in the session.
func (s *Store) ResolveNext(sessionID, model string, at time.Time) error {
	if sessionID == "" || model == "" || at.IsZero() {
		return nil
	}
	_, err := s.db.Exec(`UPDATE suggestions SET next_model = ?, next_ts_ms = ?
		WHERE session_id = ? AND next_model IS NULL AND ts_ms < ?`,
		model, at.UnixMilli(), sessionID, at.UnixMilli())
	return err
}

// Row is one logged suggestion.
type Row struct {
	At        time.Time
	SessionID string
	Current   string
	Suggested string
	Action    string
	Reason    string
	NextModel string
	Outcome   string // OutcomeSmooth, OutcomeRough, or "" while unknown
}

// Followed reports whether the next model matches the suggestion
// (nil while unknown).
func (r Row) Followed() *bool {
	if r.NextModel == "" {
		return nil
	}
	var ok bool
	if r.Action == string(strategy.Keep) {
		ok = r.NextModel == r.Current
	} else {
		ok = strings.Contains(strings.ToLower(r.NextModel), strings.ToLower(r.Suggested))
	}
	return &ok
}

// Recent returns the latest suggestions, newest first.
func (s *Store) Recent(limit int) ([]Row, error) {
	rows, err := s.db.Query(`SELECT ts_ms, session_id, current_model, suggested_model, action, reason, COALESCE(next_model, ''), COALESCE(outcome, '')
		FROM suggestions ORDER BY ts_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var ms int64
		if err := rows.Scan(&ms, &r.SessionID, &r.Current, &r.Suggested, &r.Action, &r.Reason, &r.NextModel, &r.Outcome); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(ms)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Adherence counts resolved non-keep suggestions, how many were followed,
// and how many of those went smoothly afterwards.
func (s *Store) Adherence() (resolved, followed, smooth int, err error) {
	rows, err := s.db.Query(`SELECT current_model, suggested_model, action, next_model, COALESCE(outcome, '') FROM suggestions
		WHERE next_model IS NOT NULL AND action != 'keep'`)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Current, &r.Suggested, &r.Action, &r.NextModel, &r.Outcome); err != nil {
			return 0, 0, 0, err
		}
		resolved++
		if f := r.Followed(); f != nil && *f {
			followed++
			if r.Outcome == OutcomeSmooth {
				smooth++
			}
		}
	}
	return resolved, followed, smooth, rows.Err()
}
