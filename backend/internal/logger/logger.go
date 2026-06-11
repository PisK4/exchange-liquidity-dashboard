package logger

import (
	"fmt"
	stdlog "log"
	"path/filepath"
	"runtime"
	"strings"

	"edgex-ops-intelligence/backend/internal/metrics"
)

// Printf keeps a standard-library-compatible entry point for incremental
// adoption. Existing log call sites can migrate here without changing format.
func Printf(format string, args ...any) {
	stdlog.Printf(format, args...)
}

func Infof(format string, args ...any) {
	stdlog.Printf(format, args...)
}

func Warnf(format string, args ...any) {
	stdlog.Printf("WARN: "+format, args...)
}

func Errorf(format string, args ...any) {
	module, funcName, line := caller(2)
	metrics.ReportErrorLog(module, funcName, line)
	stdlog.Printf("ERROR: "+format, args...)
}

func Fatalf(format string, args ...any) {
	module, funcName, line := caller(2)
	metrics.ReportErrorLog(module, funcName, line)
	stdlog.Fatalf(format, args...)
}

func Error(args ...any) {
	module, funcName, line := caller(2)
	metrics.ReportErrorLog(module, funcName, line)
	stdlog.Print("ERROR: " + fmt.Sprint(args...))
}

func caller(skip int) (string, string, int) {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown", "unknown", 0
	}
	fn := runtime.FuncForPC(pc)
	funcName := "unknown"
	if fn != nil {
		funcName = shortFuncName(fn.Name())
	}
	return strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)), funcName, line
}

func shortFuncName(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}
