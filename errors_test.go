package nova_test

import (
	"net/http"
	"testing"

	"github.com/gurch101/nova"
	"github.com/gurch101/nova/assert"
)

func TestNewBadRequestProblem(t *testing.T) {
	pd := nova.NewBadRequestProblem("bad input")
	assert.Equal(t, pd.Error(), "Bad Request: bad input")
	assert.Equal(t, pd.StatusCode(), http.StatusBadRequest)
}

func TestNewNotFoundProblem(t *testing.T) {
	pd := nova.NewNotFoundProblem("missing", "/items/99")
	assert.Equal(t, pd.Error(), "Resource Not Found: missing")
	assert.Equal(t, pd.StatusCode(), http.StatusNotFound)
	assert.Equal(t, pd.Instance, "/items/99")
}

func TestNewUnprocessableEntityProblem(t *testing.T) {
	pd := nova.NewUnprocessableEntityProblem("invalid payload")
	assert.Equal(t, pd.Error(), "Unprocessable Entity: invalid payload")
	assert.Equal(t, pd.StatusCode(), http.StatusUnprocessableEntity)
}

func TestValidator(t *testing.T) {
	var v nova.Validator

	err := v.ErrorOrNil()
	assert.Nil(t, err)

	v.Add("email", "must be a valid email address", "invalid_format")
	v.Check(false, "age", "must be a positive integer", "out_of_range")

	err = v.ErrorOrNil()
	assert.NotNil(t, err)

	pd, ok := err.(nova.ProblemDetail)
	assert.True(t, ok)
	assert.Equal(t, pd.StatusCode(), http.StatusUnprocessableEntity)
	assert.Equal(t, pd.Error(), "Unprocessable Entity: The request payload failed validation.")
	assert.Equal(t, len(pd.Invalid), 2)

	assert.Equal(t, pd.Invalid[0].Field, "email")
	assert.Equal(t, pd.Invalid[0].Reason, "must be a valid email address")
	assert.Equal(t, pd.Invalid[0].Code, "invalid_format")

	assert.Equal(t, pd.Invalid[1].Field, "age")
	assert.Equal(t, pd.Invalid[1].Reason, "must be a positive integer")
	assert.Equal(t, pd.Invalid[1].Code, "out_of_range")
}

func TestNewSingleFieldProblem(t *testing.T) {
	err := nova.NewSingleFieldProblem("email", "must not be empty", "required")
	assert.NotNil(t, err)

	pd, ok := err.(nova.ProblemDetail)
	assert.True(t, ok)
	assert.Equal(t, pd.StatusCode(), http.StatusUnprocessableEntity)
	assert.Equal(t, len(pd.Invalid), 1)
	assert.Equal(t, pd.Invalid[0].Field, "email")
	assert.Equal(t, pd.Invalid[0].Code, "required")
	assert.Equal(t, pd.Invalid[0].Reason, "must not be empty")
}

func TestValidatorCheckPasses(t *testing.T) {
	var v nova.Validator

	v.Check(true, "name", "must not be empty", "required")

	err := v.ErrorOrNil()
	assert.Nil(t, err)
}

func TestSetErrorTypePrefix(t *testing.T) {
	nova.SetErrorTypePrefix("https://api.example.com/errors")

	pd := nova.NewBadRequestProblem("bad input")
	assert.Equal(t, pd.Type, "https://api.example.com/errors/bad-request")

	pd2 := nova.NewNotFoundProblem("missing", "/items/1")
	assert.Equal(t, pd2.Type, "https://api.example.com/errors/not-found")

	pd3 := nova.NewUnprocessableEntityProblem("invalid")
	assert.Equal(t, pd3.Type, "https://api.example.com/errors/unprocessable-entity")

	pd4 := nova.NewMethodNotAllowedProblem("nope")
	assert.Equal(t, pd4.Type, "https://api.example.com/errors/method-not-allowed")

	sin := nova.NewSingleFieldProblem("name", "required", "required")
	assert.Equal(t, sin.(nova.ProblemDetail).Type, "https://api.example.com/errors/unprocessable-entity")

	nova.SetErrorTypePrefix("https://api.yourdomain.com/errors")
}
