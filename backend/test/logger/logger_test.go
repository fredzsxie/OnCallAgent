package logger_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

type capture struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *capture) Write(p []byte) (int, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.b.Write(p) }
func (b *capture) String() string              { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func settings() config.Logging                 { return config.Logging{Level: "debug", RetentionDays: 7, Dir: "./log"} }
func testLogger(t *testing.T, cfg config.Logging, opts logger.Options) (context.Context, *logger.Logger) {
	t.Helper()
	l, err := logger.New(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	return logger.WithLogger(context.Background(), l), l
}

func TestReadableFormatColorAndFileRetention(t *testing.T) {
	dir := t.TempDir()
	cfg := settings()
	cfg.Dir = dir
	cfg.FileEnabled = true
	for _, name := range []string{"oncall-2026-10-14.log", "oncall-2026-10-15.log", "oncall-not-a-date.log", "other-2020-01-01.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	fixed := time.Date(2026, 10, 21, 14, 30, 0, 125000000, time.FixedZone("CST", 8*3600))
	var console bytes.Buffer
	ctx, l := testLogger(t, cfg, logger.Options{Console: &console, Color: true, Now: func() time.Time { return fixed }})
	ctx, id := logger.WithRequest(ctx)
	_, _, line, _ := runtime.Caller(0)
	logger.Info(ctx, "HTTP", "请求完成", logger.F("status", 200), logger.F("elapsed", 2130*time.Millisecond), logger.F("empty", ""), logger.F("bad\nkey", "secret"))
	file, err := os.ReadFile(filepath.Join(dir, "oncall-2026-10-21.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-10-21 14:30:00.125 +08:00 INFO", fmt.Sprintf("[HTTP  ] [test/logger/logger_test.go:%d] 请求完成", line+1), "req=" + id, "status=200", "elapsed=2.13s"} {
		if !strings.Contains(string(file), want) {
			t.Fatalf("missing %q in %s", want, file)
		}
	}
	if !strings.Contains(console.String(), "\x1b[32mINFO") || strings.Contains(string(file), "\x1b") || strings.Contains(string(file), "empty=") || strings.Contains(string(file), "bad") {
		t.Fatalf("bad output %q %s", console.String(), file)
	}
	if _, err := os.Stat(filepath.Join(dir, "oncall-2026-10-14.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired owned log retained", err)
	}
	for _, name := range []string{"oncall-2026-10-15.log", "oncall-not-a-date.log", "other-2020-01-01.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("unrelated/recent log removed", name, err)
		}
	}
	fixed = fixed.Add(24 * time.Hour)
	logger.Warn(ctx, "Tool", "工具失败", logger.F("code", "upstream_error"))
	if _, err := os.Stat(filepath.Join(dir, "oncall-2026-10-22.log")); err != nil {
		t.Fatal("date rotation failed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "oncall-2026-10-15.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("daily cleanup failed", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	logger.Info(ctx, "HTTP", "关闭后不再写入")
}

func TestLevelEscapingAndSafeErrors(t *testing.T) {
	cfg := settings()
	cfg.Level = "warn"
	var out bytes.Buffer
	ctx, _ := testLogger(t, cfg, logger.Options{Console: &out})
	logger.Info(ctx, "HTTP", "不得出现")
	logger.Failure(ctx, "Model", "模型失败", errors.New("private-key https://user:password@host/?secret=abc"), logger.F("stage", "query\nforged\x1b[31m"), logger.F("raw_error", errors.New("secret")))
	logger.Failure(ctx, "SSE", "请求取消", context.Canceled)
	if strings.Count(out.String(), "\n") != 2 || strings.Contains(out.String(), "不得出现") || strings.Contains(out.String(), "private-key") || strings.Contains(out.String(), "raw_error=") || strings.Contains(out.String(), "password") || strings.Contains(out.String(), "\x1b") {
		t.Fatal("unsafe log", out.String())
	}
	for _, want := range []string{`\nforged\x1b[31m`, `code=execution_failed`, `WARN`, `code=cancelled`} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("missing", want, out.String())
		}
	}
}

func TestConcurrentContextIsolation(t *testing.T) {
	var out capture
	root, _ := testLogger(t, settings(), logger.Options{Console: &out})
	var wg sync.WaitGroup
	ids := make([]string, 20)
	for i := range ids {
		ctx, id := logger.WithRequest(root)
		ids[i] = id
		wg.Add(1)
		go func(ctx context.Context, index int) {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				logger.Info(ctx, "HTTP", "并发事件", logger.F("index", index))
			}
		}(ctx, i)
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 60 {
		t.Fatal("torn writes", len(lines))
	}
	for i, id := range ids {
		n := 0
		for _, line := range lines {
			if strings.Contains(line, "req="+id) {
				n++
				if !strings.HasSuffix(line, fmt.Sprintf("index=%d", i)) {
					t.Fatal("mixed context", line)
				}
			}
		}
		if n != 3 {
			t.Fatal("lost records", id, n)
		}
	}
}

func TestInvalidConfigAndUnwritableTarget(t *testing.T) {
	cfg := settings()
	cfg.FileEnabled = true
	cfg.Dir = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(cfg.Dir, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := logger.New(cfg, logger.Options{}); err == nil || strings.Contains(err.Error(), cfg.Dir) {
		t.Fatal("missing/unsafe initialization error", err)
	}
	for _, level := range []string{"", "trace"} {
		cfg = settings()
		cfg.Level = level
		if _, err := logger.New(cfg, logger.Options{}); err == nil {
			t.Fatal("accepted level", level)
		}
	}
}
