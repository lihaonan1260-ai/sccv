// Package logger emits the exe-directory JSONL event log of the sccv GUI
// (OPT-006 / DEC-O09).
//
// Contract highlights (see .work-log/交接协议-优化.md, T-302):
//
//   - Files live in <exe dir>/logs/ as sccv-YYYYMMDD.log, rolling over at
//     local midnight and at MaxBytes (default 10MB) per file, keeping at
//     most Keep (default 5) rotated copies per day (sccv-YYYYMMDD.log.1~.5,
//     .1 being the most recent).
//   - Each Event appends exactly one JSON object per line:
//     {"ts":...,"event":...,<caller fields>}. Content is paths, options,
//     statistical summaries and error text only — never tokens, never file
//     contents, and nothing ever reaches stdout/stderr (R3).
//   - Degradation is absolute: any initialization failure (exe path
//     unresolvable, directory not creatable, file not openable) or write
//     failure (disk full, medium yanked, read-only medium) permanently turns
//     the logger into a silent no-op. The application main flow can never
//     observe the logger, not even through an error return.
//   - Every method is safe for concurrent use (job goroutines write) and
//     safe on a nil receiver, so hook call sites stay one-liners.
package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Event names emitted by the seven OPT-006 hook points.
const (
	EventStartup      = "startup"
	EventAnalyzeStart = "analyze_start"
	EventAnalyzeDone  = "analyze_done"
	EventAnalyzeError = "analyze_error"
	EventShutdown     = "shutdown"
	EventReportDone   = "report_done"
	EventReportError  = "report_error"
)

// Defaults for the zero-value Config: 10MB per file, five rotated copies.
const (
	DefaultMaxBytes = 10 * 1024 * 1024
	DefaultKeep     = 5
)

// Naming and timestamp formats. Day keys are local time, matching the clock
// a desktop user correlates the folder listing against.
const (
	dayFormat  = "20060102"
	tsFormat   = "2006-01-02T15:04:05.000Z07:00"
	dirName    = "logs"
	filePrefix = "sccv-"
	fileSuffix = ".log"
)

// Config carries the injectable seams. The zero value means production
// behavior: <exe dir>/logs, 10MB files, five copies, real clock.
type Config struct {
	// Dir is the log directory. Empty derives <exe dir>/logs; derivation
	// failure degrades to the no-op logger.
	Dir string
	// MaxBytes is the per-file size cap before rotation; <=0 uses the 10MB
	// default. A single line larger than the cap still lands whole in a
	// freshly rotated file (rotation never loops).
	MaxBytes int
	// Keep is the maximum number of rotated copies per day; <=0 uses 5.
	Keep int
	// Now supplies timestamps; nil uses time.Now. Tests inject a stepped
	// clock to drive day rollover deterministically.
	Now func() time.Time
}

// Logger is the process-wide event sink. Construct with New; the zero value
// and nil are valid permanently-silent instances. All state is guarded by mu.
type Logger struct {
	mu       sync.Mutex
	dir      string
	maxBytes int
	keep     int
	now      func() time.Time
	day      string   // day key the current file was opened for
	file     *os.File // current append handle; nil means closed/disabled
	size     int64    // bytes written through the current handle
	disabled bool     // sticky: any failure funnels here forever
}

// New resolves the configuration and opens today's file. Any failure along
// the way yields the silent no-op logger — New never reports an error and
// never panics, because the surrounding application must stay unaware of
// the logging subsystem.
func New(cfg Config) *Logger {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.Keep <= 0 {
		cfg.Keep = DefaultKeep
	}
	dir := cfg.Dir
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return &Logger{disabled: true, now: cfg.Now}
		}
		dir = filepath.Join(filepath.Dir(exe), dirName)
	}
	l := &Logger{dir: dir, maxBytes: cfg.MaxBytes, keep: cfg.Keep, now: cfg.Now}
	if err := l.openDay(cfg.Now()); err != nil {
		return &Logger{disabled: true, now: cfg.Now}
	}
	return l
}

// Event appends one JSONL line for the named event. fields may be nil; its
// entries are merged over the fixed ts/event keys. Every failure — clock
// rollover, rotation, marshal, write — silently disables the logger for the
// rest of the process lifetime. Safe on a nil receiver.
func (l *Logger) Event(event string, fields map[string]any) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.disabled || l.file == nil {
		l.disableLocked()
		return
	}

	clock := l.now
	if clock == nil {
		clock = time.Now
	}
	ts := clock()
	if ts.Format(dayFormat) != l.day {
		if err := l.openDay(ts); err != nil {
			l.disableLocked()
			return
		}
	}

	entry := make(map[string]any, len(fields)+2)
	entry["ts"] = ts.Format(tsFormat)
	entry["event"] = event
	for k, v := range fields {
		entry[k] = v
	}
	line, err := json.Marshal(entry)
	if err != nil {
		l.disableLocked()
		return
	}
	line = append(line, '\n')

	if l.size > 0 && l.size+int64(len(line)) > int64(l.maxBytes) {
		if err := l.rotateLocked(); err != nil {
			return // already disabled by rotateLocked
		}
	}
	if _, err := l.file.Write(line); err != nil {
		l.disableLocked()
		return
	}
	l.size += int64(len(line))
}

// Close releases the current file handle. It is idempotent, nil-safe and
// used by tests (t.TempDir cleanup on Windows refuses directories with open
// handles); production relies on process exit instead. Events after Close
// degrade silently.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// openDay closes any current handle and opens (creating or appending) the
// file for the day of now. Caller holds mu.
func (l *Logger) openDay(now time.Time) error {
	return l.openDayFile(now.Format(dayFormat))
}

// openDayFile is the single file-acquisition path: ensure the directory,
// open in append mode, adopt the on-disk size. Caller holds mu.
func (l *Logger) openDayFile(day string) error {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.dayPath(day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if l.file != nil {
		l.file.Close()
	}
	l.file = f
	l.day = day
	l.size = info.Size()
	return nil
}

// rotateLocked shifts the day's file through the .1~.<keep> ladder: the
// oldest copy is removed, every existing copy moves one step down and the
// current file becomes .1. Caller holds mu; every failure is terminal.
func (l *Logger) rotateLocked() error {
	l.file.Close()
	l.file = nil
	base := l.dayPath(l.day)
	if err := os.Remove(base + "." + strconv.Itoa(l.keep)); err != nil && !os.IsNotExist(err) {
		l.disableLocked()
		return err
	}
	for i := l.keep - 1; i >= 1; i-- {
		if err := os.Rename(base+"."+strconv.Itoa(i), base+"."+strconv.Itoa(i+1)); err != nil && !os.IsNotExist(err) {
			l.disableLocked()
			return err
		}
	}
	if err := os.Rename(base, base+".1"); err != nil {
		l.disableLocked()
		return err
	}
	if err := l.openDayFile(l.day); err != nil {
		l.disableLocked()
		return err
	}
	return nil
}

// disableLocked is the single degradation funnel: drop the handle and stay
// silent forever. Caller holds mu.
func (l *Logger) disableLocked() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	l.disabled = true
}

// dayPath joins the directory with the day-stamped file name.
func (l *Logger) dayPath(day string) string {
	return filepath.Join(l.dir, filePrefix+day+fileSuffix)
}
