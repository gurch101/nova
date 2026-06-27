package nova

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type contextKey string

const (
	requestIDKey   contextKey = "request_id"
	bodyKey        contextKey = "request_body"
	maxBodyCapture           = 64 * 1024
)

type bodyCapturer struct {
	io.Reader
	body io.ReadCloser
}

func (b *bodyCapturer) Close() error { return b.body.Close() }

// cappedBuffer wraps bytes.Buffer with a write limit for body capture.
// Excess writes are silently discarded to prevent unbounded memory growth
// from large request bodies.
type cappedBuffer struct {
	bytes.Buffer
	capped bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.capped {
		return len(p), nil
	}
	remaining := maxBodyCapture - b.Buffer.Len()
	if remaining <= 0 {
		b.capped = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.Buffer.Write(p[:remaining])
		b.capped = true
	} else {
		b.Buffer.Write(p)
	}
	return len(p), nil
}

// Middleware is a function that wraps an http.Handler, returning a new handler
// that may perform pre- or post-processing around the wrapped handler.
type Middleware func(next http.Handler) http.Handler

// Envelope wraps a response with a custom HTTP status code.
type Envelope[T any] struct {
	Status int
	Data   T
}

type envelope interface {
	envelopeMarker()
	GetStatus() int
	GetData() any
}

func (e Envelope[T]) envelopeMarker() {}
func (e Envelope[T]) GetStatus() int   { return e.Status }
func (e Envelope[T]) GetData() any     { return e.Data }

// Empty is a sentinel value handlers return to signal 204 No Content.
type empty struct{}

var Empty empty

var contextPool = sync.Pool{
	New: func() any { return &Context{} },
}

type Application struct {
	mux        *http.ServeMux
	middleware []Middleware
	handler    http.Handler
}

func NewApplication() *Application {
	mux := http.NewServeMux()
	app := &Application{mux: mux, handler: mux}
	app.Use(RequestLogger)
	app.Use(Recoverer)
	return app
}

func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ec := &errorCapturer{ResponseWriter: w}
	a.handler.ServeHTTP(ec, r)

	ct := w.Header().Get("Content-Type")
	isMuxText := ct == "" || ct == "text/plain; charset=utf-8"

	if ec.code == http.StatusNotFound && isMuxText {
		writeError(w, NewNotFoundProblem("route not found", r.URL.String()), r.Context())
		return
	}
	if ec.code == http.StatusMethodNotAllowed && isMuxText {
		pd := NewMethodNotAllowedProblem("The requested HTTP method is not supported for this endpoint")
		if allow := w.Header().Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		writeError(w, pd, r.Context())
	}
}

// Use adds a middleware to the application. Middleware is applied in the order
// it is added, so the first added middleware wraps the outermost layer.
func (a *Application) Use(mw Middleware) {
	a.middleware = append(a.middleware, mw)
	h := http.Handler(a.mux)
	for i := len(a.middleware) - 1; i >= 0; i-- {
		h = a.middleware[i](h)
	}
	a.handler = h
}

// Handle registers a standard http.Handler for the given method and pattern.
// The method should be an HTTP method string (e.g., "GET", "POST") or "*" for any method.
func (a *Application) Handle(method, pattern string, handler http.Handler) {
	a.mux.Handle(method+" "+pattern, handler)
}

// HandleFunc registers a standard http.HandlerFunc for the given method and pattern.
func (a *Application) HandleFunc(method, pattern string, handler func(w http.ResponseWriter, r *http.Request)) {
	a.mux.HandleFunc(method+" "+pattern, handler)
}

// Recoverer is a middleware that recovers from panics, logs the stack trace,
// and returns an HTTP 500 ProblemDetail response.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				requestID, _ := r.Context().Value(requestIDKey).(string)
				attrs := []slog.Attr{
					slog.String("request_id", requestID),
					slog.Any("panic", rec),
					slog.String("stack", string(stack)),
				}
				if bp, ok := r.Context().Value(bodyKey).(*cappedBuffer); ok && bp.Len() > 0 {
					attrs = append(attrs, slog.String("body", bp.String()))
				}
				slog.LogAttrs(r.Context(), slog.LevelError, "panic recovered", attrs...)
				writeError(w, ProblemDetail{
					Type:   ErrorTypeURLPrefix + "/internal-server-error",
					Title:  "Internal Server Error",
					Status: http.StatusInternalServerError,
					Detail: "An unexpected error occurred",
				}, r.Context())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

var (
	requestIDPrefix  string
	requestIDCounter atomic.Uint64
)

func init() {
	b := make([]byte, 8)
	rand.Read(b)
	requestIDPrefix = hex.EncodeToString(b)
}

func generateRequestID() string {
	var b strings.Builder
	b.Grow(len(requestIDPrefix) + 12)
	b.WriteString(requestIDPrefix)
	b.WriteString(strconv.FormatUint(requestIDCounter.Add(1), 36))
	return b.String()
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// errorCapturer buffers the response body only when the status code is 404
// or 405 and Content-Type is empty or the mux's default text/plain,
// allowing ServeHTTP to replace mux-generated text error responses
// with RFC 9457 ProblemDetail JSON without buffering successful responses.
type errorCapturer struct {
	http.ResponseWriter
	code    int
	buf     bytes.Buffer
	handled bool // set true when a typed route handler starts execution
}

// markHandlerCapturer traverses known ResponseWriter wrappers to find and
// mark the errorCapturer, indicating that a registered route handler is
// executing so that intentional 404/405 responses are not replaced.
func markHandlerCapturer(w http.ResponseWriter) {
	for {
		if ec, ok := w.(*errorCapturer); ok {
			ec.handled = true
			return
		}
		switch v := w.(type) {
		case *statusRecorder:
			w = v.ResponseWriter
		default:
			return
		}
	}
}

func (w *errorCapturer) WriteHeader(code int) {
	w.code = code
	if w.isMuxError() {
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *errorCapturer) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	if w.isMuxError() {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *errorCapturer) isMuxError() bool {
	if w.handled || (w.code != http.StatusNotFound && w.code != http.StatusMethodNotAllowed) {
		return false
	}
	ct := w.Header().Get("Content-Type")
	return ct == "" || ct == "text/plain; charset=utf-8"
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	if real := r.Header.Get("X-Real-Ip"); real != "" {
		return real
	}
	return r.RemoteAddr
}

// RequestLogger is a middleware that logs each incoming HTTP request with its
// method, URL, response status, duration, client IP, user-agent, and request ID.
// The request ID is also set on the request context so it can be picked up by
// Context.RequestID. On 5xx responses the request body is included in the log.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := generateRequestID()

		var buf cappedBuffer
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = &bodyCapturer{
				Reader: io.TeeReader(r.Body, &buf),
				body:   r.Body,
			}
		}

		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID))
		r = r.WithContext(context.WithValue(r.Context(), bodyKey, &buf))

		rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		attrs := []slog.Attr{
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("url", r.URL.String()),
			slog.Int("status", rec.statusCode),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("ip", clientIP(r)),
			slog.String("user_agent", r.UserAgent()),
		}
		if rec.statusCode >= http.StatusInternalServerError && buf.Len() > 0 {
			attrs = append(attrs, slog.String("body", buf.String()))
		}
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
	})
}

func Get[Req, Res any](app *Application, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	register(app, "GET", pattern, handler)
}

func Post[Req, Res any](app *Application, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	register(app, "POST", pattern, handler)
}

func Put[Req, Res any](app *Application, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	register(app, "PUT", pattern, handler)
}

func Delete[Req, Res any](app *Application, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	register(app, "DELETE", pattern, handler)
}

func Patch[Req, Res any](app *Application, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	register(app, "PATCH", pattern, handler)
}

func register[Req, Res any](app *Application, method, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
	// Build the decoder plan once at startup — zero reflection per request.
	plan := buildPlan[Req]()

	fullPattern := method + " " + pattern
	app.mux.HandleFunc(fullPattern, func(w http.ResponseWriter, r *http.Request) {
		markHandlerCapturer(w)

		req, err := decodeRequest[Req](r, plan)
		if err != nil {
			writeError(w, NewBadRequestProblem(err.Error()), r.Context())
			return
		}

		requestID, _ := r.Context().Value(requestIDKey).(string)
		ctx := contextPool.Get().(*Context)
		ctx.Request = r
		ctx.Response = w
		ctx.RequestID = requestID
		defer func() {
			ctx.Request = nil
			ctx.Response = nil
			ctx.Payload = nil
			ctx.RequestID = ""
			ctx.logger = nil
			contextPool.Put(ctx)
		}()

		res, err := handler(ctx, req)
		if err != nil {
			writeError(w, err, r.Context())
			return
		}

		switch v := any(res).(type) {
		case empty:
			w.WriteHeader(http.StatusNoContent)
		case envelope:
			status := v.GetStatus()
			if status == 0 {
				status = http.StatusOK
			}
			writeJSON(w, status, v.GetData(), r.Context())
		default:
			writeJSON(w, http.StatusOK, v, r.Context())
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any, ctx context.Context) {
	data, err := json.Marshal(v)
	if err != nil {
		requestID, _ := ctx.Value(requestIDKey).(string)
		slog.LogAttrs(ctx, slog.LevelWarn, "failed to encode response",
			slog.String("request_id", requestID),
			slog.Any("error", err),
		)
		data = []byte("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}

// writeError sends an RFC 9457 ProblemDetail as JSON.
// If the error is not a ProblemDetail it falls back to a generic 500.
func writeError(w http.ResponseWriter, err error, ctx context.Context) {
	var pd ProblemDetail
	requestID, _ := ctx.Value(requestIDKey).(string)
	if !errors.As(err, &pd) {
		slog.LogAttrs(ctx, slog.LevelError, "unhandled error",
			slog.String("request_id", requestID),
			slog.Any("error", err),
		)
		pd = ProblemDetail{
			Type:   ErrorTypeURLPrefix + "/internal-server-error",
			Title:  "Internal Server Error",
			Status: http.StatusInternalServerError,
			Detail: "An unexpected error occurred",
		}
	}

	data, encodeErr := json.Marshal(pd)
	if encodeErr != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to encode error response",
			slog.String("request_id", requestID),
			slog.Any("error", encodeErr),
		)
		data = []byte("{}")
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(pd.Status)
	w.Write(data)
}


