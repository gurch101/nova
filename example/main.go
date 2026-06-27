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

type CreateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type CreateUserResponse struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

func CreateUser(ctx *nova.Context, req CreateUserRequest) (nova.Envelope[CreateUserResponse], error) {
	var v nova.Validator
	v.Check(req.Name != "", "name", "must not be empty", "required")
	if err := v.ErrorOrNil(); err != nil {
		return nova.Envelope[CreateUserResponse]{}, err
	}

	return nova.Envelope[CreateUserResponse]{
		Status: http.StatusCreated,
		Data:   CreateUserResponse{ID: 1, Email: req.Email},
	}, nil
}

func main() {
	app := nova.NewApplication()
	nova.Post(app, "/users/{id}/metrics", GetUserMetrics)
	nova.Post(app, "/users", CreateUser)
	http.ListenAndServe(":8080", app)
}
