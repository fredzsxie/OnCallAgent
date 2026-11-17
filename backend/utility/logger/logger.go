package logger

import (
	"context"
	"crypto/rand"
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
	minimum level
	sink    *sink
	color   bool
}
type level uint8

const (
	debugLevel level = iota
	infoLevel
	warnLevel
	errorLevel
)

type contextKey struct{}
type correlationKey struct{}
type correlation struct{ kind, id string }

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
	levels := map[string]level{"debug": debugLevel, "info": infoLevel, "warn": warnLevel, "error": errorLevel}
	return &Logger{minimum: levels[cfg.Level], sink: s, color: options.Color}, nil
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

// WithRequest preserves a safe incoming ID so every request boundary shares one ID.
func WithRequest(ctx context.Context, incoming ...string) (context.Context, string) {
	if len(incoming) > 0 && validRequestID(incoming[0]) {
		return withID(ctx, "rid", incoming[0])
	}
	return withID(ctx, "rid", newID())
}

func WithRun(ctx context.Context) (context.Context, string) { return withID(ctx, "run", newID()) }

func withID(ctx context.Context, kind, id string) (context.Context, string) {
	return context.WithValue(ctx, correlationKey{}, correlation{kind, id}), id
}

func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic("cannot generate correlation ID")
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
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
	emit(ctx, debugLevel, component, message, fields, "")
}
func Info(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, infoLevel, component, message, fields, "")
}
func Warn(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, warnLevel, component, message, fields, "")
}
func Error(ctx context.Context, component, message string, fields ...Field) {
	emit(ctx, errorLevel, component, message, fields, "")
}
func emit(ctx context.Context, severity level, component, message string, fields []Field, table string) {
	if ctx == nil {
		return
	}
	l, _ := ctx.Value(contextKey{}).(*Logger)
	if l == nil || severity < l.minimum {
		return
	}
	var contextFields []Field
	if id, ok := ctx.Value(correlationKey{}).(correlation); ok {
		contextFields = append(contextFields, F(id.kind, id.id))
	}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		contextFields = append(contextFields, F("trace", span.TraceID().String()))
	}
	fields = append(contextFields, fields...)
	label := [...]string{"DEBU", "INFO", "WARN", "ERRO"}[severity]
	line := format(l.sink.now(), label, component, caller(), message, fields)
	if table != "" {
		line += "\n" + strings.TrimRight(table, "\n") + "\n"
	}
	// The sink writes each complete record and both output copies under one lock.
	l.sink.write(line, label, l.color)
}

// Only startup route metadata uses a multiline block; request records stay single-line.
func RouterTable(ctx context.Context, table string, count int) {
	if ctx == nil || table == "" {
		return
	}
	emit(ctx, infoLevel, "Router", "已注册 API 路由", []Field{F("count", count)}, table)
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
	fmt.Fprintf(&b, "%s [%s] %s -> [%s] %s", now.Format("2006-01-02 15:04:05.000"), level, escape(position), escape(component), escape(message))
	for _, f := range fields {
		if f.Key == "" || strings.IndexFunc(f.Key, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) >= 0 {
			continue
		}
		value := scalar(f.Value)
		if value == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(f.Key)
		b.WriteByte('[')
		b.WriteString(escape(value))
		b.WriteByte(']')
	}
	b.WriteByte('\n')
	return b.String()
}

func scalar(value any) string {
	switch v := value.(type) {
	case string:
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

// Escape field delimiters and control characters so one event stays on one physical line.
func escape(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ']':
			b.WriteString(`\]`)
		default:
			if unicode.IsControl(r) || !unicode.IsPrint(r) {
				quoted := strconv.QuoteRuneToGraphic(r)
				b.WriteString(quoted[1 : len(quoted)-1])
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
