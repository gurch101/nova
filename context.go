package nova

import (
	"log/slog"
	"net/http"
)

type Context struct {
	Request   *http.Request
	Response  http.ResponseWriter
	Payload   any
	RequestID string

	logger *slog.Logger
}

func (c *Context) Logger() *slog.Logger {
	if c.logger == nil {
		c.logger = slog.With("request_id", c.RequestID)
	}
	return c.logger
}
