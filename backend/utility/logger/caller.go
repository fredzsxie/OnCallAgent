package logger

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

var sourceRoot, loggingPackage = callerMetadata()

func callerMetadata() (string, string) {
	pc, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", ""
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "", ""
	}
	name := fn.Name()
	pkg := name[:strings.LastIndex(name, ".")+1]
	file = filepath.ToSlash(file)
	const suffix = "/utility/logger/caller.go"
	if !strings.HasSuffix(file, suffix) {
		return "", pkg
	}
	return strings.TrimSuffix(file, suffix) + "/", pkg
}

// CallersFrames preserves inlined call sites; skipping a package also covers Finish and defer wrappers.
func caller() string {
	if sourceRoot == "" || loggingPackage == "" {
		return "unknown:0"
	}
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		file := filepath.ToSlash(frame.File)
		if !strings.HasPrefix(frame.Function, loggingPackage) && frame.Line > 0 && strings.HasPrefix(file, sourceRoot) {
			relative := strings.TrimPrefix(file, sourceRoot)
			if relative != "" && !strings.HasPrefix(relative, "../") && !filepath.IsAbs(relative) {
				return relative + ":" + strconv.Itoa(frame.Line)
			}
		}
		if !more {
			break
		}
	}
	return "unknown:0"
}
