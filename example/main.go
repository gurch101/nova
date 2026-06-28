package main

import (
	"net/http"

	"github.com/gurch101/nova"
)

type Address struct {
	City    string `json:"city" validate:"required"`
	Country string `json:"country" validate:"required"`
}

type Tag struct {
	Key   string `json:"key" validate:"required"`
	Value string `json:"value" validate:"required"`
}

type CreateUserRequest struct {
	Name    string  `json:"name" validate:"required"`
	Email   string  `json:"email" validate:"email"`
	Age     int     `json:"age" validate:"min=18,max=120"`
	Address Address `json:"address"`
	Tags    []Tag   `json:"tags" validate:"min=1"`
}

type CreateUserResponse struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

func CreateUser(ctx *nova.Context, req CreateUserRequest) (nova.Envelope[CreateUserResponse], error) {
	return nova.Envelope[CreateUserResponse]{
		Status: http.StatusCreated,
		Data:   CreateUserResponse{ID: 1, Email: req.Email},
	}, nil
}

func main() {
	app := nova.NewApplication()
	nova.Post(app, "/users", CreateUser)
	http.ListenAndServe(":8080", app)
}
