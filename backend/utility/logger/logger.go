package logger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fredzsxie/OnCallAgent/backend/internal/config"
	"github.com/gogf/gf/v2/os/glog"
	"github.com/gogf/gf/v2/util/guid"
	"github.com/mattn/go-isatty"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Field struct {
	Key   string
	Value any
}

func F(key string, value any) Field { return Field{key, value} }

type Options struct {
	Console io.Writer
	Color   bool
	Now     func() time.Time
}

type Logger struct {
	base *glog.Logger
	sink *sink
}
type contextKey struct{}
type correlationKey struct{}
type correlation struct{ kind, id string }
type record struct {
	component, message string
	caller             string
	fields             []Field
	table              string
}

func New(cfg config.Logging, options Options) (*Logger, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	s := &sink{cfg: cfg, console: options.Console, now: options.Now}
	if err := s.prepare(); err != nil {
		_ = s.Close()
		return nil, errors.New("cannot initialize log files; check log directory permissions")
	}
	base := glog.New()
	c := glog.DefaultConfig()
	c.StdoutPrint, c.HeaderPrint, c.StStatus = false, false, 0
	c.Handlers = []glog.Handler{func(ctx context.Context, in *glog.HandlerInput) {
		if len(in.Values) != 1 {
			return
		}
		r, ok := in.Values[0].(record)
		if !ok {
			return
		}
		level := map[int]string{glog.LEVEL_DEBU: "DEBUG", glog.LEVEL_INFO: "INFO", glog.LEVEL_WARN: "WARN", glog.LEVEL_ERRO: "ERROR"}[in.Level]
		var fields []Field
		if id, ok := ctx.Value(correlationKey{}).(correlation); ok {
			fields = append(fields, F(id.kind, id.id))
		}
		if span := trace.SpanContextFromContext(ctx); span.IsValid() {
			fields = append(fields, F("trace", span.TraceID().String()))
		}
		fields = append(fields, r.fields...)
		line := format(options.Now(), level, r.component, r.caller, r.message, fields)
		if r.table != "" {
			line += "\n" + strings.TrimRight(r.table, "\n") + "\n"
		}
		// One record is written under one lock, including its terminal and file copies.
		s.write(line, level, options.Color)
	}}
	if err := base.SetConfig(c); err != nil {
		s.Close()
		return nil, errors.New("cannot initialize logger")
	}
	levels := map[string]int{"debug": glog.LEVEL_DEBU | glog.LEVEL_INFO | glog.LEVEL_WARN | glog.LEVEL_ERRO, "info": glog.LEVEL_INFO | glog.LEVEL_WARN | glog.LEVEL_ERRO, "warn": glog.LEVEL_WARN | glog.LEVEL_ERRO, "error": glog.LEVEL_ERRO}
	base.SetLevel(levels[cfg.Level])
	return &Logger{base, s}, nil
}

func (l *Logger) Close() error { return l.sink.Close() }
func WithLogger(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}
func Inherit(ctx, parent context.Context) context.Context {
	if l, ok := parent.Value(contextKey{}).(*Logger); ok {
		return WithLogger(ctx, l)
	}
	return ctx
}
func WithRequest(ctx context.Context) (context.Context, string) { return withID(ctx, "req") }
func WithRun(ctx context.Context) (context.Context, string)     { return withID(ctx, "run") }
func withID(ctx context.Context, kind string) (context.Context, string) {
	id := guid.S()
	return context.WithValue(ctx, correlationKey{}, correlation{kind, id}), id
}

func Bootstrap(ctx context.Context, web bool) (context.Context, *Logger, error) {
	cfg, err := config.LoadLogging()
	if err != nil {
		return ctx, nil, err
	}
	options := Options{}
	if web {
		options.Console = os.Stderr
		options.Color = isatty.IsTerminal(os.Stderr.Fd()) && os.Getenv("NO_COLOR") == ""
	}
	l, err := New(cfg, options)
	if err != nil {
		return ctx, nil, err
	}
	ctx = WithLogger(ctx, l)
	ctx, _ = WithRun(ctx)
	Info(ctx, "App", "日志已初始化", F("level", cfg.Level), F("file_enabled", cfg.FileEnabled))
	return ctx, l, nil
}

func Debug(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, glog.LEVEL_DEBU, component, message, fields)
}
func Info(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, glog.LEVEL_INFO, component, message, fields)
}
func Warn(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, glog.LEVEL_WARN, component, message, fields)
}
func Error(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, glog.LEVEL_ERRO, component, message, fields)
}
func emit(ctx context.Context, level int, component, message string, fields []Field) {
	if ctx == nil {
		return
	}
	l, _ := ctx.Value(contextKey{}).(*Logger)
	if l == nil || l.base.GetLevel()&level == 0 {
		return
	}
	r := record{component: component, message: message, caller: caller(), fields: fields}
	switch level {
	case glog.LEVEL_DEBU:
		l.base.Debug(ctx, r)
	case glog.LEVEL_INFO:
		l.base.Info(ctx, r)
	case glog.LEVEL_WARN:
		l.base.Warning(ctx, r)
	case glog.LEVEL_ERRO:
		l.base.Error(ctx, r)
	}
}

// Only startup route metadata uses a multiline block; request records stay single-line.
func RouterTable(ctx context.Context, table string, count int) {
	if ctx == nil || table == "" {
		return
	}
	if l, ok := ctx.Value(contextKey{}).(*Logger); ok && l != nil && l.base.GetLevel()&glog.LEVEL_INFO != 0 {
		l.base.Info(ctx, record{component: "Router", message: "已注册 API 路由", caller: caller(), fields: []Field{F("count", count)}, table: table})
	}
}

func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

// Classify errors without formatting provider messages, URLs, bodies or credentials.
func ErrorType(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var n net.Error
	if errors.As(err, &n) {
		if n.Timeout() {
			return "timeout"
		}
		return "network_error"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "not_found"
	}
	switch status.Code(err) {
	case codes.Unavailable:
		return "unavailable"
	case codes.Unauthenticated, codes.PermissionDenied:
		return "authentication_failed"
	case codes.InvalidArgument:
		return "invalid_input"
	case codes.DeadlineExceeded:
		return "timeout"
	case codes.Canceled:
		return "cancelled"
	}
	return "execution_failed"
}

func Failure(ctx context.Context, component, message string, err error, fields ...Field) {
	errorType := ErrorType(err)
	found := false
	for _, field := range fields {
		if field.Key == "error_type" {
			found = true
			break
		}
	}
	if !found {
		fields = append(fields, F("error_type", errorType))
	}
	reasons := map[string]string{"cancelled": "请求已取消", "timeout": "执行超时", "network_error": "网络连接失败", "permission_denied": "权限不足", "not_found": "资源不存在", "unavailable": "依赖不可用", "authentication_failed": "认证失败", "invalid_input": "输入无效", "execution_failed": "调用未完成"}
	if reason := reasons[errorType]; reason != "" {
		message += "：" + reason
	}
	if errorType == "cancelled" {
		Warn(ctx, component, message, fields...)
		return
	}
	Error(ctx, component, message, fields...)
}

// Finish observes the existing execution boundary without introducing stream consumers.
func Finish(ctx context.Context, component, message string, started time.Time, err error, fields ...Field) {
	fields = append(fields, F("elapsed", time.Since(started)))
	if err != nil {
		Failure(ctx, component, message+"失败", err, fields...)
		return
	}
	Info(ctx, component, message+"完成", fields...)
}

func format(now time.Time, level, component, position, message string, fields []Field) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s [%s](%s) --> %s", now.Format("2006-01-02 15:04:05.000 -07:00"), level, escape(component), escape(position), escape(message))
	first := true
	for _, f := range fields {
		if f.Key == "" || strings.IndexFunc(f.Key, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) >= 0 {
			continue
		}
		value := scalar(f.Value)
		if value == "" {
			continue
		}
		if first {
			b.WriteString(" | ")
			first = false
		} else {
			b.WriteByte(' ')
		}
		b.WriteString(f.Key)
		b.WriteByte('=')
		b.WriteString(value)
	}
	b.WriteByte('\n')
	return b.String()
}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return ""
		}
		if strings.IndexFunc(v, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || r == '"' || r == '\\' || r == '=' || r == '|'
		}) >= 0 {
			return strconv.QuoteToGraphic(v)
		}
		return v
	case time.Duration:
		if v < time.Second {
			return fmt.Sprintf("%gms", float64(v.Round(time.Microsecond))/float64(time.Millisecond))
		}
		return fmt.Sprintf("%gs", float64(v.Round(10*time.Millisecond))/float64(time.Second))
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func escape(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return quoted[1 : len(quoted)-1]
}
