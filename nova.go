package nova

import (
	"encoding/json"
	"log"
	"net/http"
)

// Empty is a sentinel value handlers return to signal 204 No Content.
type empty struct{}

var Empty empty

type Application struct {
	mux *http.ServeMux
}

func NewApplication() *Application {
	app := &Application{mux: http.NewServeMux()}

	// Catch-all for unmatched routes — returns an RFC 9457 ProblemDetail 404
	// instead of the default plain-text response. Specific registered patterns
	// take precedence because they are more specific than {path...}.
	app.mux.HandleFunc("/{path...}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, NewNotFoundProblem("route not found", r.URL.String()))
	})

	return app
}

func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
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
			// Decode errors (bad path value, query param, or JSON body)
			// are always client errors → 400 Bad Request.
			writeError(w, NewBadRequestProblem(err.Error()))
			return
		}

		ctx := &Context{
			Request:  r,
			Response: w,
		}

		res, err := handler(ctx, req)
		if err != nil {
			writeError(w, err)
			return
		}

		if _, ok := any(res).(empty); ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Printf("nova: failed to encode response: %v", err)
		}
	})
}

// writeError sends an RFC 9457 ProblemDetail as JSON.
// If the error is not a ProblemDetail it falls back to a generic 500.
func writeError(w http.ResponseWriter, err error) {
	pd, ok := err.(ProblemDetail)
	if !ok {
		log.Printf("nova: unhandled error: %v", err)
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
		log.Printf("nova: failed to encode error response: %v", encodeErr)
	}
}


