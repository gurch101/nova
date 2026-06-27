package main

import (
	"fmt"
	"net/http"

	"github.com/gurch101/nova"
)

type GetUserMetricsRequest struct {
	UserID     int    `path:"id"`
	Detailed   bool   `query:"detailed"`
	Timezone   string `query:"tz"`
	MetricType string `json:"metricType"`
}

type MetricsResponse struct {
	Processed bool   `json:"processed"`
	Message   string `json:"message"`
}

func GetUserMetrics(ctx *nova.Context, req GetUserMetricsRequest) (*MetricsResponse, error) {
	var v nova.Validator
	v.Check(req.UserID > 0, "id", "User ID must be greater than zero", "invalid")
	if err := v.ErrorOrNil(); err != nil {
		return nil, err
	}

	return &MetricsResponse{
		Processed: true,
		Message:   fmt.Sprintf("User %d metrics processed for type %s", req.UserID, req.MetricType),
	}, nil
}

func main() {
	app := nova.NewApplication()
	nova.Post(app, "/users/{id}/metrics", GetUserMetrics)
	http.ListenAndServe(":8080", app)
}
