package logger

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
)

type sink struct {
	mu      sync.Mutex
	cfg     config.Logging
	console io.Writer
	now     func() time.Time
	file    *os.File
	day     string
	closed  bool
	warned  bool
}

func (s *sink) prepare() error {
	if !s.cfg.FileEnabled {
		return nil
	}
	if err := os.MkdirAll(s.cfg.Dir, 0700); err != nil {
		return err
	}
	return s.rotate(s.now())
}

func (s *sink) rotate(now time.Time) error {
	day := now.Format("2006-01-02")
	if s.file != nil && s.day == day {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(s.cfg.Dir, "oncall-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	s.file, s.day = f, day
	entries, err := os.ReadDir(s.cfg.Dir)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -s.cfg.RetentionDays).Format("2006-01-02")
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasPrefix(name, "oncall-") && strings.HasSuffix(name, ".log") {
			date := strings.TrimSuffix(strings.TrimPrefix(name, "oncall-"), ".log")
			if _, err := time.Parse("2006-01-02", date); err == nil && date <= cutoff {
				if err := os.Remove(filepath.Join(s.cfg.Dir, name)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *sink) write(line, level string, color bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.cfg.FileEnabled {
		err := s.rotate(s.now())
		if err == nil {
			_, err = io.WriteString(s.file, line)
		}
		if err != nil && !s.warned {
			s.warned = true
			if s.console != nil {
				_, _ = io.WriteString(s.console, format(s.now(), "ERROR", "Logger", caller(), "日志文件写入失败，请检查磁盘与权限", []Field{F("code", "log_write_failed")}))
			}
		}
	}
	if s.console != nil {
		if color {
			colors := map[string]string{"DEBUG": "36", "INFO": "32", "WARN": "33", "ERROR": "31"}
			line = strings.Replace(line, " "+level, " \x1b["+colors[level]+"m"+level+"\x1b[0m", 1)
		}
		_, _ = io.WriteString(s.console, line)
	}
}

func (s *sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file != nil {
		return errors.Join(s.file.Sync(), s.file.Close())
	}
	return nil
}
