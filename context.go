package nova

import (
	"log/slog"
	"net/http"
)

// Context holds per-request state including the original Request, Response,
// and a request-scoped logger. Instances are pooled via sync.Pool in register().
type Context struct {
	Request   *http.Request
	Response  http.ResponseWriter
	RequestID string

	logger *slog.Logger
}

func (c *Context) Logger() *slog.Logger {
	if c.logger == nil {
		c.logger = slog.With("request_id", c.RequestID)
	}
	return c.logger
}
