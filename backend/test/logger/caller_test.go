package logger_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fredzsxie/OnCallAgent/backend/utility/logger"
)

func assertCaller(t *testing.T, output string, lines ...int) {
	t.Helper()
	matched := false
	for _, line := range lines {
		if strings.Contains(output, fmt.Sprintf("(test/logger/caller_test.go:%d)", line)) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("expected caller lines %v, got %s", lines, output)
	}
	for _, bad := range []string{"(utility/logger/", "github.com/", "/Users/", "/home/", "unknown:0"} {
		if strings.Contains(output, bad) {
			t.Fatalf("unexpected caller path %q: %s", bad, output)
		}
	}
}

func TestCallerAllLevelsAndWrappers(t *testing.T) {
	for _, item := range []struct {
		name string
		call func(context.Context, string, string, ...logger.Field)
	}{
		{"debug", logger.Debug}, {"info", logger.Info}, {"warn", logger.Warn}, {"error", logger.Error},
	} {
		t.Run(item.name, func(t *testing.T) {
			var output bytes.Buffer
			ctx, _ := testLogger(t, settings(), logger.Options{Console: &output})
			_, _, line, _ := runtime.Caller(0)
			item.call(ctx, "Test", "直接调用")
			assertCaller(t, output.String(), line+1)
		})
	}
	for _, err := range []error{nil, errors.New("private-provider-message"), context.Canceled} {
		t.Run(fmt.Sprint(err), func(t *testing.T) {
			var output bytes.Buffer
			ctx, _ := testLogger(t, settings(), logger.Options{Console: &output})
			_, _, line, _ := runtime.Caller(0)
			logger.Finish(ctx, "Test", "包装调用", time.Now(), err)
			assertCaller(t, output.String(), line+1)
			if strings.Contains(output.String(), "private-provider-message") {
				t.Fatal("raw error leaked")
			}
		})
	}
	var output bytes.Buffer
	ctx, _ := testLogger(t, settings(), logger.Options{Console: &output})
	_, _, line, _ := runtime.Caller(0)
	logger.Failure(ctx, "Test", "直接失败", context.DeadlineExceeded)
	assertCaller(t, output.String(), line+1)
}

func directDefer(ctx context.Context) int {
	_, _, line, _ := runtime.Caller(0)
	defer logger.Finish(ctx, "Test", "直接 defer", time.Now(), nil)
	return line + 1
}
func closureDefer(ctx context.Context) (line int) {
	defer func() {
		_, _, at, _ := runtime.Caller(0)
		logger.Finish(ctx, "Test", "闭包 defer", time.Now(), context.Canceled)
		line = at + 1
	}()
	return
}

func TestCallerDeferredAndGoroutine(t *testing.T) {
	for _, item := range []struct {
		call   func(context.Context) int
		direct bool
	}{{directDefer, true}, {closureDefer, false}} {
		var output bytes.Buffer
		ctx, _ := testLogger(t, settings(), logger.Options{Console: &output})
		line := item.call(ctx)
		if item.direct {
			// Open-coded defers can be attributed to the return that executes them.
			assertCaller(t, output.String(), line, line+1)
		} else {
			assertCaller(t, output.String(), line)
		}
	}
	var output capture
	ctx, _ := testLogger(t, settings(), logger.Options{Console: &output})
	done := make(chan int)
	go func() {
		_, _, line, _ := runtime.Caller(0)
		logger.Info(ctx, "Test", "goroutine 调用")
		done <- line + 1
	}()
	line := <-done
	assertCaller(t, output.String(), line)
}

func TestCallerTerminalFileAndLevelFilter(t *testing.T) {
	cfg := settings()
	cfg.Dir = t.TempDir()
	cfg.FileEnabled = true
	cfg.Level = "error"
	var output bytes.Buffer
	ctx, _ := testLogger(t, cfg, logger.Options{Console: &output})
	logger.Info(ctx, "Test", "被过滤的日志")
	logger.RouterTable(ctx, "被过滤的路由表", 1)
	_, _, line, _ := runtime.Caller(0)
	logger.Error(ctx, "Test", "位置一致")
	assertCaller(t, output.String(), line+1)
	files, err := filepath.Glob(filepath.Join(cfg.Dir, "oncall-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || string(disk) != output.String() {
		t.Fatal("terminal/file mismatch", err)
	}
	if strings.Contains(output.String(), "被过滤") || strings.Count(output.String(), "\n") != 1 {
		t.Fatal("level filter changed", output.String())
	}
}

func TestCallerOutsideModuleFallsBackSafely(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	program := `package main
import("context";"github.com/fredzsxie/OnCallAgent/backend/utility/logger")
func main(){ctx,logs,err:=logger.Bootstrap(context.Background(),true);if err!=nil{panic(err)};defer logs.Close();logger.Info(ctx,"Test","outside module")}
`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", source)
	command.Dir = "../.."
	env := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "ONCALL_LOG_") {
			env = append(env, value)
		}
	}
	command.Env = append(env, "GOTOOLCHAIN=local", "ONCALL_LOG_LEVEL=info", "ONCALL_LOG_FILE_ENABLED=false", "NO_COLOR=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external caller: %v %s", err, output)
	}
	if !strings.Contains(string(output), "(unknown:0) --> outside module") || strings.Contains(string(output), directory) {
		t.Fatalf("unsafe fallback: %s", output)
	}
}
