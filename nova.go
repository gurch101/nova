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
	"time"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	bodyKey      contextKey = "request_body"
)

type bodyCapturer struct {
	io.Reader
	body io.ReadCloser
}

func (b *bodyCapturer) Close() error { return b.body.Close() }

// Middleware is a function that wraps an http.Handler, returning a new handler
// that may perform pre- or post-processing around the wrapped handler.
type Middleware func(next http.Handler) http.Handler

// Envelope wraps a response with a custom HTTP status code.
type Envelope[T any] struct {
	Status int
	Data   T
}

type envelope interface {
	GetStatus() int
	GetData() any
}

func (e Envelope[T]) GetStatus() int { return e.Status }
func (e Envelope[T]) GetData() any   { return e.Data }

// Empty is a sentinel value handlers return to signal 204 No Content.
type empty struct{}

var Empty empty

type Application struct {
	mux        *http.ServeMux
	middleware []Middleware
}

func NewApplication() *Application {
	app := &Application{mux: http.NewServeMux()}

	// Catch-all for unmatched routes — returns an RFC 9457 ProblemDetail 404
	// instead of the default plain-text response. Specific registered patterns
	// take precedence because they are more specific than {path...}.
	app.mux.HandleFunc("/{path...}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, NewNotFoundProblem("route not found", r.URL.String()), r.Context())
	})

	app.Use(RequestLogger)
	app.Use(Recoverer)
	return app
}

func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var h http.Handler = a.mux
	for i := len(a.middleware) - 1; i >= 0; i-- {
		h = a.middleware[i](h)
	}
	h.ServeHTTP(w, r)
}

// Use adds a middleware to the application. Middleware is applied in the order
// it is added, so the first added middleware wraps the outermost layer.
func (a *Application) Use(mw Middleware) {
	a.middleware = append(a.middleware, mw)
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
				if bp, ok := r.Context().Value(bodyKey).(*bytes.Buffer); ok && bp.Len() > 0 {
					attrs = append(attrs, slog.String("body", bp.String()))
				}
				slog.LogAttrs(r.Context(), slog.LevelError, "panic recovered", attrs...)
				writeError(w, ProblemDetail{
					Type:   "https://api.yourdomain.com/errors/internal-server-error",
					Title:  "Internal Server Error",
					Status: http.StatusInternalServerError,
					Detail: "An unexpected error occurred",
				}, r.Context())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func generateRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
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

		var buf bytes.Buffer
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
		req, err := decodeRequest[Req](r, plan)
		if err != nil {
			writeError(w, NewBadRequestProblem(err.Error()), r.Context())
			return
		}

		requestID, _ := r.Context().Value(requestIDKey).(string)
		ctx := &Context{
			Request:   r,
			Response:  w,
			RequestID: requestID,
		}

		res, err := handler(ctx, req)
		if err != nil {
			writeError(w, err, r.Context())
			return
		}

		if _, ok := any(res).(empty); ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if env, ok := any(res).(envelope); ok {
			status := env.GetStatus()
			if status == 0 {
				status = http.StatusOK
			}
			writeJSON(w, status, env.GetData(), r.Context())
			return
		}

		writeJSON(w, http.StatusOK, res, r.Context())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any, ctx context.Context) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		requestID, _ := ctx.Value(requestIDKey).(string)
		slog.LogAttrs(ctx, slog.LevelWarn, "failed to encode response",
			slog.String("request_id", requestID),
			slog.Any("error", err),
		)
	}
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
			Type:   "https://api.yourdomain.com/errors/internal-server-error",
			Title:  "Internal Server Error",
			Status: http.StatusInternalServerError,
			Detail: "An unexpected error occurred",
		}
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(pd.Status)
	if encodeErr := json.NewEncoder(w).Encode(pd); encodeErr != nil {
		slog.LogAttrs(ctx, slog.LevelError, "failed to encode error response",
			slog.String("request_id", requestID),
			slog.Any("error", encodeErr),
		)
	}
}


