package logfile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logPrefix = "vaps-"
	logSuffix = ".log"
)

type DailyRotatingWriter struct {
	mu            sync.Mutex
	dir           string
	retentionDays int
	currentDate   string
	file          *os.File
}

func NewDailyRotatingWriter(dir string, retentionDays int) (*DailyRotatingWriter, error) {
	writer := &DailyRotatingWriter{dir: dir, retentionDays: retentionDays}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writer.rotateLocked(time.Now()); err != nil {
		return nil, err
	}
	return writer, nil
}

func (w *DailyRotatingWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	if w.currentDate != logDate(now) {
		if err := w.rotateLocked(now); err != nil {
			return 0, err
		}
	}
	return w.file.Write(data)
}

func (w *DailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *DailyRotatingWriter) rotateLocked(now time.Time) error {
	date := logDate(now)
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	file, err := os.OpenFile(filepath.Join(w.dir, logPrefix+date+logSuffix), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file = file
	w.currentDate = date
	return w.cleanupLocked(now)
}

func (w *DailyRotatingWriter) cleanupLocked(now time.Time) error {
	if w.retentionDays <= 0 {
		return nil
	}

	cutoff := dateOnly(now).AddDate(0, 0, -w.retentionDays+1)
	matches, err := filepath.Glob(filepath.Join(w.dir, logPrefix+"*"+logSuffix))
	if err != nil {
		return err
	}
	for _, path := range matches {
		date, ok := dateFromLogPath(path)
		if !ok || !date.Before(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func logDate(t time.Time) string {
	return dateOnly(t).Format("2006-01-02")
}

func dateOnly(t time.Time) time.Time {
	year, month, day := t.Local().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Local().Location())
}

func dateFromLogPath(path string) (time.Time, bool) {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, logPrefix) || !strings.HasSuffix(name, logSuffix) {
		return time.Time{}, false
	}
	value := strings.TrimSuffix(strings.TrimPrefix(name, logPrefix), logSuffix)
	date, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return date, true
}

var _ io.WriteCloser = (*DailyRotatingWriter)(nil)
