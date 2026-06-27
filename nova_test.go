package nova_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gurch101/nova"
	"github.com/gurch101/nova/assert"
)

func TestGetRoute(t *testing.T) {
	type GetRequest struct {
		ID   int    `path:"id"`
		Name string `query:"name"`
	}

	type GetResponse struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Get(app, "/users/{id}", func(ctx *nova.Context, req GetRequest) (GetResponse, error) {
		return GetResponse{ID: req.ID, Name: req.Name}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/users/42?name=alice")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)

	var body GetResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, body.ID, 42)
	assert.Equal(t, body.Name, "alice")
}

func TestPostWithBody(t *testing.T) {
	type CreateRequest struct {
		Title  string `json:"title"`
		Author string `json:"author"`
	}

	type CreateResponse struct {
		OK bool `json:"ok"`
	}

	app := nova.NewApplication()
	nova.Post(app, "/books", func(ctx *nova.Context, req CreateRequest) (CreateResponse, error) {
		return CreateResponse{OK: req.Title != ""}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := `{"title":"The Go Programming Language","author":"Donovan & Kernighan"}`
	resp, err := http.Post(server.URL+"/books", "application/json", strings.NewReader(body))
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)

	var res CreateResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
	assert.True(t, res.OK)
}

func TestMixedPathQueryAndBody(t *testing.T) {
	type MixedRequest struct {
		UserID int    `path:"id"`
		Page   int    `query:"page"`
		Filter string `json:"filter"`
	}

	type MixedResponse struct {
		UserID int    `json:"userId"`
		Page   int    `json:"page"`
		Filter string `json:"filter"`
	}

	app := nova.NewApplication()
	nova.Put(app, "/users/{id}", func(ctx *nova.Context, req MixedRequest) (MixedResponse, error) {
		return MixedResponse{UserID: req.UserID, Page: req.Page, Filter: req.Filter}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := `{"filter":"active"}`
	req, _ := http.NewRequest("PUT", server.URL+"/users/99?page=2", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)

	var result MixedResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, result.UserID, 99)
	assert.Equal(t, result.Page, 2)
	assert.Equal(t, result.Filter, "active")
}

func TestDecodeErrorIsBadRequest(t *testing.T) {
	type Req struct{ ID int `path:"id"` }
	type Res struct{}

	app := nova.NewApplication()
	nova.Get(app, "/things/{id}", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/things/notanumber")
	assert.NoError(t, err)
	defer resp.Body.Close()

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusBadRequest)
}

func TestHandlerErrorFallsBackTo500(t *testing.T) {
	type Req struct{ ID int `path:"id"` }
	type Res struct{}

	app := nova.NewApplication()
	nova.Get(app, "/items/{id}", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, errors.New("handler blew up")
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/items/1")
	assert.NoError(t, err)
	defer resp.Body.Close()

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusInternalServerError)
}

func TestMultipleRoutes(t *testing.T) {
	type GetReq struct {
		ID int `path:"id"`
	}
	type GetRes struct {
		ID    int    `json:"id"`
		Route string `json:"route"`
	}
	type PostReq struct {
		Name string `json:"name"`
	}
	type PostRes struct {
		Route string `json:"route"`
	}

	app := nova.NewApplication()

	nova.Get(app, "/users/{id}", func(ctx *nova.Context, req GetReq) (GetRes, error) {
		return GetRes{ID: req.ID, Route: "get"}, nil
	})

	nova.Post(app, "/users", func(ctx *nova.Context, req PostReq) (PostRes, error) {
		if req.Name == "" {
			return PostRes{}, nova.NewBadRequestProblem("name is required")
		}
		return PostRes{Route: "post"}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	getResp, err := http.Get(server.URL + "/users/7")
	assert.NoError(t, err)
	defer getResp.Body.Close()

	var getRes GetRes
	assert.NoError(t, json.NewDecoder(getResp.Body).Decode(&getRes))
	assert.Equal(t, getRes.ID, 7)
	assert.Equal(t, getRes.Route, "get")

	postResp, err := http.Post(server.URL+"/users", "application/json", strings.NewReader(`{"name":"bob"}`))
	assert.NoError(t, err)
	defer postResp.Body.Close()

	var postRes PostRes
	assert.NoError(t, json.NewDecoder(postResp.Body).Decode(&postRes))
	assert.Equal(t, postRes.Route, "post")
}

// --- HTTP-level error response tests ---------------------------------------

func TestHandleBadRequest(t *testing.T) {
	type Req struct {
		ID int `path:"id"`
	}
	type Res struct {
		Msg string `json:"msg"`
	}

	app := nova.NewApplication()
	nova.Get(app, "/items/{id}", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nova.NewBadRequestProblem("invalid id")
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/items/1")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/problem+json")

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusBadRequest)
	assert.Equal(t, pd.Title, "Bad Request")
}

func TestHandleNotFound(t *testing.T) {
	type Req struct{ ID string `path:"id"` }
	type Res struct{}

	app := nova.NewApplication()
	nova.Get(app, "/resources/{id}", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nova.NewNotFoundProblem("resource not found", "/resources/"+req.ID)
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/resources/abc")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusNotFound)

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusNotFound)
	assert.Equal(t, pd.Instance, "/resources/abc")
}

func TestUnprocessableEntity(t *testing.T) {
	type Req struct{ Name string `json:"name"` }
	type Res struct{}

	app := nova.NewApplication()
	nova.Post(app, "/items", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nova.NewUnprocessableEntityProblem("validation failed")
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Post(server.URL+"/items", "application/json", nil)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusUnprocessableEntity)

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Title, "Unprocessable Entity")
	assert.Equal(t, pd.Detail, "validation failed")
}

func TestRouteNotFound(t *testing.T) {
	type Req struct{}
	type Res struct{}

	app := nova.NewApplication()
	nova.Get(app, "/api/health", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/nonexistent")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusNotFound)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/problem+json")

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusNotFound)
	assert.Equal(t, pd.Title, "Resource Not Found")
}

func TestHandleValidationProblem(t *testing.T) {
	type Req struct{ Name string `json:"name"` }
	type Res struct{}

	app := nova.NewApplication()
	nova.Post(app, "/validate", func(ctx *nova.Context, req Req) (Res, error) {
		var v nova.Validator
		v.Check(req.Name != "", "name", "must not be empty", "required")
		return Res{}, v.ErrorOrNil()
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Post(server.URL+"/validate", "application/json", strings.NewReader(`{"name":""}`))
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusUnprocessableEntity)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/problem+json")

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Title, "Unprocessable Entity")
	assert.Equal(t, len(pd.Invalid), 1)
	assert.Equal(t, pd.Invalid[0].Field, "name")
}

func TestEmptyResponseReturns204(t *testing.T) {
	type Req struct{}

	app := nova.NewApplication()
	nova.Get(app, "/noop", func(ctx *nova.Context, req Req) (any, error) {
		return nova.Empty, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/noop")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusNoContent)
	assert.Equal(t, resp.ContentLength, int64(0))
}

func TestEnvelopeWithCustomStatus(t *testing.T) {
	type CreateResp struct {
		ID int `json:"id"`
	}

	app := nova.NewApplication()
	nova.Post(app, "/users", func(ctx *nova.Context, req struct{}) (nova.Envelope[CreateResp], error) {
		return nova.Envelope[CreateResp]{Status: http.StatusCreated, Data: CreateResp{ID: 1}}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Post(server.URL+"/users", "application/json", nil)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusCreated)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/json")

	var body CreateResp
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, body.ID, 1)
}

func TestEnvelopeDefaultsTo200WhenStatusIsZero(t *testing.T) {
	type Data struct {
		Msg string `json:"msg"`
	}

	app := nova.NewApplication()
	nova.Get(app, "/data", func(ctx *nova.Context, req struct{}) (nova.Envelope[Data], error) {
		return nova.Envelope[Data]{Data: Data{Msg: "ok"}}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/data")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/json")

	var body Data
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, body.Msg, "ok")
}

func TestRequestIDIsSetOnContext(t *testing.T) {
	type Req struct{}
	type Res struct {
		RequestID string `json:"requestId"`
	}

	app := nova.NewApplication()

	nova.Get(app, "/echo-rid", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{RequestID: ctx.RequestID}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/echo-rid")
	assert.NoError(t, err)
	defer resp.Body.Close()

	var body Res
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.True(t, body.RequestID != "")
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	type Req struct{}
	type Res struct {
		RequestID string `json:"requestId"`
	}

	app := nova.NewApplication()

	nova.Get(app, "/rid", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{RequestID: ctx.RequestID}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	getRID := func() string {
		resp, _ := http.Get(server.URL + "/rid")
		var body struct{ RequestID string }
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		return body.RequestID
	}

	assert.NotEqual(t, getRID(), getRID())
}

func TestContextLoggerIncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	orig := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(orig)

	app := nova.NewApplication()

	nova.Get(app, "/log-test", func(ctx *nova.Context, req struct{}) (struct{}, error) {
		slog.Info("before")
		ctx.Logger().Info("handler message")
		slog.Info("after")
		return struct{}{}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/log-test")
	assert.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusOK)

	output := buf.String()
	assert.True(t, strings.Contains(output, "handler message"))
	assert.True(t, strings.Contains(output, "request_id="))
}

func TestPanicRecovery(t *testing.T) {
	type Req struct{}
	type Res struct{}

	app := nova.NewApplication()

	nova.Get(app, "/panic", func(ctx *nova.Context, req Req) (Res, error) {
		panic("something went wrong")
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/panic")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusInternalServerError)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/problem+json")

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, pd.Status, http.StatusInternalServerError)
	assert.Equal(t, pd.Title, "Internal Server Error")
	assert.Equal(t, pd.Detail, "An unexpected error occurred")
}

func TestEmbeddedStructDecode(t *testing.T) {
	type PathQuery struct {
		ID     int    `path:"id"`
		SortBy string `query:"sort"`
	}

	type EmbeddedRequest struct {
		PathQuery
		Name string `json:"name"`
	}

	type EmbeddedResponse struct {
		ID     int    `json:"id"`
		SortBy string `json:"sortBy"`
		Name   string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Put(app, "/resources/{id}", func(ctx *nova.Context, req EmbeddedRequest) (EmbeddedResponse, error) {
		return EmbeddedResponse{ID: req.ID, SortBy: req.SortBy, Name: req.Name}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := `{"name":"test-resource"}`
	req, _ := http.NewRequest("PUT", server.URL+"/resources/99?sort=name", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)

	var result EmbeddedResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, result.ID, 99)
	assert.Equal(t, result.SortBy, "name")
	assert.Equal(t, result.Name, "test-resource")
}

func TestValueResponseReturns200(t *testing.T) {
	type Req struct{}
	type Res struct {
		Name string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Get(app, "/hello", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{Name: "world"}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/hello")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/json")

	var body Res
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, body.Name, "world")
}
