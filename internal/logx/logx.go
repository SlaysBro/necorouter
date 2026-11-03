// Package logx is the router's own log. It does not depend on journald or syslog, so it
// works the same on any distribution (systemd, OpenRC, etc.).
//
// File record format: "DATE TIME [level] [service]<TAB>file:line,function<TAB>message".
// show log prints "DATE TIME [level] [service] message", show log excessive adds
// "[file:line,function]". Records of user actions use the service IMISH, message
// "AUDIT [user] command".
package logx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

const (
	timeFmt     = "2006-01-02 15:04:05.000000"
	fileName    = "necorouter.log"
	maxFileSize = 2 << 20 // size of one file; when exceeded, the file is rotated
	keepOld     = 3       // how many old files to keep (necorouter.log.1 … .3)
)

// Logger writes records to a file with rotation. Safe to call from several goroutines.
type Logger struct {
	mu     sync.Mutex
	dir    string
	f      *os.File
	size   int64
	mirror io.Writer // copy of records in human-readable form (daemon stderr), may be nil
}

var (
	defMu sync.RWMutex
	def   *Logger
)

// SetDefault sets the default log; nil disables writing (this is how tests run).
func SetDefault(l *Logger) {
	defMu.Lock()
	def = l
	defMu.Unlock()
}

// Default returns the default log or nil.
func Default() *Logger {
	defMu.RLock()
	defer defMu.RUnlock()
	return def
}

// Open opens (creates) the log in directory dir. mirror is where to duplicate records (for example, stderr).
func Open(dir string, mirror io.Writer) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	l := &Logger{dir: dir, mirror: mirror}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Logger) open() error {
	f, err := os.OpenFile(filepath.Join(l.dir, fileName), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

// Close closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func (l *Logger) rotate() {
	_ = l.f.Close()
	l.f = nil
	base := filepath.Join(l.dir, fileName)
	_ = os.Remove(fmt.Sprintf("%s.%d", base, keepOld))
	for i := keepOld - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", base, i), fmt.Sprintf("%s.%d", base, i+1))
	}
	_ = os.Rename(base, base+".1")
	if err := l.open(); err != nil {
		l.f = nil
	}
}

// caller returns "file:line,function" of the calling code; skip is how many frames to skip.
func caller(skip int) string {
	pc, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return ""
	}
	fn := ""
	if f := runtime.FuncForPC(pc); f != nil {
		fn = f.Name()
		if i := strings.LastIndexByte(fn, '.'); i >= 0 {
			fn = fn[i+1:]
		}
	}
	return fmt.Sprintf("%s:%d,%s", filepath.Base(file), line, fn)
}

func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ", "[", "(", "]", ")").Replace(s)
}

// log writes one or more records (one per message line).
func (l *Logger) log(src, level, svc, msg string) {
	if l == nil {
		return
	}
	now := time.Now().Format(timeFmt)
	svc = clean(svc)
	for _, line := range strings.Split(strings.TrimRight(msg, "\r\n"), "\n") {
		line = strings.TrimRight(strings.ReplaceAll(line, "\t", " "), "\r")
		if line == "" {
			continue
		}
		rec := fmt.Sprintf("%s [%s] [%s]\t%s\t%s\n", now, level, svc, src, line)
		l.mu.Lock()
		if l.f != nil {
			if l.size > 0 && l.size+int64(len(rec)) > maxFileSize {
				l.rotate()
			}
			if l.f != nil {
				n, _ := l.f.WriteString(rec)
				l.size += int64(n)
			}
		}
		mirror := l.mirror
		l.mu.Unlock()
		if mirror != nil {
			fmt.Fprintf(mirror, "necorouter: %s [%s] [%s] %s\n", now, level, svc, line)
		}
		notify(Entry{Time: time.Now(), Level: level, Svc: svc, Src: src, Msg: line})
	}
}

func put(level, svc, f string, a []any) {
	l := Default()
	if l == nil {
		return
	}
	// frames: caller ← put ← Infof (etc.) ← calling code
	l.log(caller(2), level, svc, fmt.Sprintf(f, a...))
}

func Infof(svc, f string, a ...any)  { put(LevelInfo, svc, f, a) }
func Warnf(svc, f string, a ...any)  { put(LevelWarn, svc, f, a) }
func Errorf(svc, f string, a ...any) { put(LevelError, svc, f, a) }
func Fatalf(svc, f string, a ...any) { put(LevelFatal, svc, f, a) }

// Auditf writes a user action record: "[info] [IMISH] AUDIT [user] message".
func Auditf(user, f string, a ...any) {
	l := Default()
	if l == nil {
		return
	}
	l.log(caller(1), LevelInfo, "IMISH", fmt.Sprintf("AUDIT [%s] %s", clean(user), fmt.Sprintf(f, a...)))
}

// Entry is one log record.
type Entry struct {
	Time            time.Time
	Level, Svc, Src string
	Msg             string
}

// String is the line for output; excessive adds "[file:line,function]".
func (e Entry) String(excessive bool) string {
	ts := e.Time.Format(timeFmt)
	if excessive && e.Src != "" {
		return fmt.Sprintf("%s [%s] [%s] [%s] %s", ts, e.Level, e.Svc, e.Src, e.Msg)
	}
	return fmt.Sprintf("%s [%s] [%s] %s", ts, e.Level, e.Svc, e.Msg)
}

var prefixRe = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{6}) \[([a-z]+)\] \[([^\]]*)\]$`)

func parseLine(line string) (Entry, bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 {
		return Entry{}, false
	}
	m := prefixRe.FindStringSubmatch(parts[0])
	if m == nil {
		return Entry{}, false
	}
	t, err := time.ParseInLocation(timeFmt, m[1], time.Local)
	if err != nil {
		return Entry{}, false
	}
	return Entry{Time: t, Level: m[2], Svc: m[3], Src: parts[1], Msg: parts[2]}, true
}

// parseData parses file data; returns the records and the number of bytes up to the end of the last complete line.
func parseData(data []byte) ([]Entry, int64) {
	var out []Entry
	var used int64
	for len(data) > 0 {
		i := strings.IndexByte(string(data), '\n')
		if i < 0 {
			break // incomplete last line: will be completed later
		}
		if e, ok := parseLine(string(data[:i])); ok {
			out = append(out, e)
		}
		used += int64(i + 1)
		data = data[i+1:]
	}
	return out, used
}

// ReadAll reads all stored records from oldest to newest (taking rotation into account).
// The second value is the offset in the current file from which Follow will continue reading.
func ReadAll(dir string) ([]Entry, int64, error) {
	base := filepath.Join(dir, fileName)
	var all []Entry
	var off int64
	for i := keepOld; i >= 0; i-- {
		path := base
		if i > 0 {
			path = fmt.Sprintf("%s.%d", base, i)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, 0, err
		}
		es, used := parseData(data)
		all = append(all, es...)
		if i == 0 {
			off = used
		}
	}
	return all, off, nil
}

// Follow emits new records appearing in the log after offset off (from ReadAll),
// until ctx is cancelled. File rotation is taken into account.
func Follow(ctx context.Context, dir string, off int64, fn func(Entry)) {
	base := filepath.Join(dir, fileName)
	read := func(path string, from int64) int64 {
		f, err := os.Open(path)
		if err != nil {
			return from
		}
		defer f.Close()
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return from
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return from
		}
		es, used := parseData(data)
		for _, e := range es {
			fn(e)
		}
		return from + used
	}
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := os.Stat(base)
		if err != nil {
			continue
		}
		if st.Size() < off { // the file was rotated: read the rest of the previous one and continue from the start of the new one
			read(base+".1", off)
			off = 0
		}
		off = read(base, off)
	}
}

// Dir returns the log directory.
func (l *Logger) Dir() string { return l.dir }

// BootTime is the system boot time (from /proc/uptime); zero time if it cannot be determined.
func BootTime() time.Time {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}
	}
	var up float64
	if _, err := fmt.Sscan(string(b), &up); err != nil || up <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-time.Duration(up * float64(time.Second)))
}

// Logf writes a record, substituting the caller into "file:line,function": skip 0 is the function that called Logf.
func Logf(skip int, level, svc, f string, a ...any) {
	l := Default()
	if l == nil {
		return
	}
	l.log(caller(skip+1), level, svc, fmt.Sprintf(f, a...))
}

// Subscribers receive every new record (terminal monitor). They are called outside the log's locks.
var (
	subMu  sync.Mutex
	subs   = map[int]func(Entry){}
	subSeq int
)

// Subscribe registers a handler for new records; cancel removes it.
func Subscribe(fn func(Entry)) (cancel func()) {
	subMu.Lock()
	subSeq++
	id := subSeq
	subs[id] = fn
	subMu.Unlock()
	return func() {
		subMu.Lock()
		delete(subs, id)
		subMu.Unlock()
	}
}

func notify(e Entry) {
	subMu.Lock()
	list := make([]func(Entry), 0, len(subs))
	for _, fn := range subs {
		list = append(list, fn)
	}
	subMu.Unlock()
	for _, fn := range list {
		fn(e)
	}
}
