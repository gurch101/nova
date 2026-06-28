package nova_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	type Req struct {
		ID int `path:"id"`
	}
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
	type Req struct {
		ID int `path:"id"`
	}
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
	type Req struct {
		ID string `path:"id"`
	}
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
	type Req struct {
		Name string `json:"name"`
	}
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
	type Req struct {
		Name string `json:"name"`
	}
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

func TestHandle(t *testing.T) {
	app := nova.NewApplication()
	app.Handle("GET", "/hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Hello"))
	}))

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/hello")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), "text/plain")

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, string(body), "Hello")
}

func TestHandleFunc(t *testing.T) {
	app := nova.NewApplication()
	app.HandleFunc("POST", "/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusCreated)
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Post(server.URL+"/echo", "text/plain", strings.NewReader("data"))
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusCreated)
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/octet-stream")

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, string(body), "data")
}

func TestEmbeddedPointerStructPanics(t *testing.T) {
	type Base struct {
		ID int `path:"id"`
	}
	type BadRequest struct {
		*Base
		Name string `json:"name"`
	}
	type Res struct{}

	app := nova.NewApplication()
	assert.Panics(t, func() {
		nova.Get(app, "/items/{id}", func(ctx *nova.Context, req BadRequest) (Res, error) {
			return Res{}, nil
		})
	})
}

func TestHandleMethodNotAllowed(t *testing.T) {
	app := nova.NewApplication()
	app.Handle("POST", "/only-post", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/only-post")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, resp.StatusCode, http.StatusMethodNotAllowed)
}

// --- Edge-case tests --------------------------------------------------------

func TestAllHTTPMethods(t *testing.T) {
	type Req struct {
		ID int `path:"id"`
	}
	type Res struct {
		Method string `json:"method"`
	}

	tests := []struct {
		name   string
		method string
		setup  func(*nova.Application)
	}{
		{"GET", "GET",
			func(app *nova.Application) {
				nova.Get(app, "/methods/{id}", func(ctx *nova.Context, req Req) (Res, error) { return Res{Method: "GET"}, nil })
			}},
		{"POST", "POST",
			func(app *nova.Application) {
				nova.Post(app, "/methods/{id}", func(ctx *nova.Context, req Req) (Res, error) { return Res{Method: "POST"}, nil })
			}},
		{"PUT", "PUT",
			func(app *nova.Application) {
				nova.Put(app, "/methods/{id}", func(ctx *nova.Context, req Req) (Res, error) { return Res{Method: "PUT"}, nil })
			}},
		{"DELETE", "DELETE",
			func(app *nova.Application) {
				nova.Delete(app, "/methods/{id}", func(ctx *nova.Context, req Req) (Res, error) { return Res{Method: "DELETE"}, nil })
			}},
		{"PATCH", "PATCH",
			func(app *nova.Application) {
				nova.Patch(app, "/methods/{id}", func(ctx *nova.Context, req Req) (Res, error) { return Res{Method: "PATCH"}, nil })
			}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := nova.NewApplication()
			tt.setup(app)
			server := httptest.NewServer(app)
			defer server.Close()

			req, err := http.NewRequest(tt.method, server.URL+"/methods/1", nil)
			assert.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			assert.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var res Res
			assert.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
			assert.Equal(t, tt.method, res.Method)
		})
	}
}

func TestEmptyBodyPost(t *testing.T) {
	type Req struct {
		Name string `json:"name"`
	}
	type Res struct {
		Received bool `json:"received"`
	}

	app := nova.NewApplication()
	nova.Post(app, "/empty", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{Received: req.Name == ""}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Post(server.URL+"/empty", "application/json", nil)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var res Res
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
	assert.True(t, res.Received)
}

func TestExtraJSONFieldsAreIgnored(t *testing.T) {
	type Req struct {
		Name string `json:"name"`
	}
	type Res struct {
		Name string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Post(app, "/extra", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{Name: req.Name}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := strings.NewReader(`{"name":"alice","extra":"ignored","unknown":99}`)
	resp, err := http.Post(server.URL+"/extra", "application/json", body)
	assert.NoError(t, err)
	defer resp.Body.Close()

	var res Res
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
	assert.Equal(t, "alice", res.Name)
}

func TestRequestIDIsUniqueConcurrently(t *testing.T) {
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

	const goroutines = 50
	var mu sync.Mutex
	ids := make(map[string]struct{}, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			resp, err := http.Get(server.URL + "/rid")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var body struct{ RequestID string }
			json.NewDecoder(resp.Body).Decode(&body)
			mu.Lock()
			ids[body.RequestID] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Equal(t, len(ids), goroutines)
}

func TestDeeplyNestedEmbeddedStruct(t *testing.T) {
	type Base struct {
		ID int `path:"id"`
	}
	type Middle struct {
		Base
		Sort string `query:"sort"`
	}
	type NestedRequest struct {
		Middle
		Name string `json:"name"`
	}
	type NestedResponse struct {
		ID   int    `json:"id"`
		Sort string `json:"sort"`
		Name string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Put(app, "/nested/{id}", func(ctx *nova.Context, req NestedRequest) (NestedResponse, error) {
		return NestedResponse{ID: req.ID, Sort: req.Sort, Name: req.Name}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := `{"name":"deep"}`
	req, _ := http.NewRequest("PUT", server.URL+"/nested/7?sort=asc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result NestedResponse
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, 7, result.ID)
	assert.Equal(t, "asc", result.Sort)
	assert.Equal(t, "deep", result.Name)
}

func TestMissingOptionalQueryParamDefaultsToZero(t *testing.T) {
	type Req struct {
		ID     int    `path:"id"`
		SortBy string `query:"sort"`
		Page   int    `query:"page"`
	}
	type Res struct {
		ID     int    `json:"id"`
		SortBy string `json:"sortBy"`
		Page   int    `json:"page"`
	}

	app := nova.NewApplication()
	nova.Get(app, "/items/{id}", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{ID: req.ID, SortBy: req.SortBy, Page: req.Page}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/items/42?sort=name")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var res Res
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&res))
	assert.Equal(t, 42, res.ID)
	assert.Equal(t, "name", res.SortBy)
	assert.Equal(t, 0, res.Page)
}

func TestHandleFuncCustomContentTypeNotOverwritten(t *testing.T) {
	app := nova.NewApplication()
	app.Handle("GET", "/custom-404", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.api+json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}))

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/custom-404")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "application/vnd.api+json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, `{"error":"not found"}`, string(body))
}

func TestValidateRequest(t *testing.T) {
	type ValidateReq struct {
		Name     string   `json:"name"     validate:"required,min=2,max=100"`
		Age      int      `json:"age"      validate:"required,min=0,max=150"`
		Tags     []string `json:"tags"     validate:"required,min=1,max=10"`
		Role     string   `json:"role"     validate:"oneof=admin user viewer"`
		Email    string   `json:"email"    validate:"required,email"`
		Username string   `json:"username" validate:"required,alphanum"`
		Code     string   `json:"code"     validate:"alpha"`
	}
	type ValidateRes struct {
		OK bool `json:"ok"`
	}

	type NoTagsReq struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	type NoTagsRes struct {
		OK bool `json:"ok"`
	}

	type EmbeddedBase struct {
		Name string `json:"name" validate:"required,min=2"`
	}
	type EmbeddedReq struct {
		EmbeddedBase
	}
	type EmbeddedRes struct {
		Name string `json:"name"`
	}

	app := nova.NewApplication()
	nova.Post(app, "/validate", func(ctx *nova.Context, req ValidateReq) (ValidateRes, error) {
		return ValidateRes{OK: true}, nil
	})
	nova.Get(app, "/validate-path/{id}", func(ctx *nova.Context, req struct {
		ID int `path:"id" validate:"required,min=1"`
	}) (ValidateRes, error) {
		return ValidateRes{OK: true}, nil
	})
	nova.Get(app, "/validate-query", func(ctx *nova.Context, req struct {
		Sort string `query:"sort" validate:"oneof=asc desc"`
	}) (ValidateRes, error) {
		return ValidateRes{OK: true}, nil
	})
	nova.Post(app, "/validate-no-tags", func(ctx *nova.Context, req NoTagsReq) (NoTagsRes, error) {
		return NoTagsRes{OK: true}, nil
	})
	nova.Post(app, "/validate-embedded", func(ctx *nova.Context, req EmbeddedReq) (EmbeddedRes, error) {
		return EmbeddedRes{Name: req.Name}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	validBody := `{"name":"alice","age":30,"tags":["a","b"],"role":"admin","email":"a@b.com","username":"alice42","code":"ABC"}`

	tests := []struct {
		name              string
		method            string
		url               string
		body              string
		wantStatus        int
		wantCode          string
		wantMinViolations int
	}{
		{
			name:       "valid request",
			method:     "POST",
			url:        "/validate",
			body:       validBody,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing required string",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"","age":30,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "required",
		},
		{
			name:       "zero-value required int",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":0,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "required",
		},
		{
			name:       "nil required slice",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":null,"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "required",
		},
		{
			name:       "string below min length",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"a","age":30,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "min",
		},
		{
			name:       "string above max length",
			method:     "POST",
			url:        "/validate",
			body:       fmt.Sprintf(`{"name":"%s","age":30,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`, strings.Repeat("x", 101)),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "max",
		},
		{
			name:       "int below min value",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":-1,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "min",
		},
		{
			name:       "int above max value",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":200,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "max",
		},
		{
			name:       "slice below min length",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":[],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "min",
		},
		{
			name:       "slice above max length",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":["a","b","c","d","e","f","g","h","i","j","k"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "max",
		},
		{
			name:       "value not in oneof set",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":["a"],"role":"superadmin","email":"a@b.com","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "oneof",
		},
		{
			name:       "non-alpha characters",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice","code":"ABC123"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "alpha",
		},
		{
			name:       "non-alphanum characters",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":["a"],"role":"admin","email":"a@b.com","username":"alice-user","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "alphanum",
		},
		{
			name:       "invalid email",
			method:     "POST",
			url:        "/validate",
			body:       `{"name":"alice","age":30,"tags":["a"],"role":"admin","email":"notanemail","username":"alice","code":"ABC"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "email",
		},
		{
			name:              "multiple simultaneous violations",
			method:            "POST",
			url:               "/validate",
			body:              `{"name":"","age":0,"tags":null,"role":"","email":"","username":"","code":""}`,
			wantStatus:        http.StatusUnprocessableEntity,
			wantCode:          "required",
			wantMinViolations: 2,
		},
		{
			name:       "struct without validate tags",
			method:     "POST",
			url:        "/validate-no-tags",
			body:       `{"name":"","age":0}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "required zero int without required tag",
			method:     "POST",
			url:        "/validate-no-tags",
			body:       `{"name":"hello","age":0}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "embedded struct with validate",
			method:     "POST",
			url:        "/validate-embedded",
			body:       `{"name":""}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, server.URL+tt.url, strings.NewReader(tt.body))
			assert.NoError(t, err)
			if tt.body != "" && tt.method == "POST" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			assert.NoError(t, err)
			defer resp.Body.Close()

			respBody, _ := io.ReadAll(resp.Body)

			assert.Equal(t, resp.StatusCode, tt.wantStatus)

			if tt.wantStatus == http.StatusOK {
				return
			}

			var pd nova.ProblemDetail
			json.Unmarshal(respBody, &pd)
			assert.Equal(t, pd.Status, http.StatusUnprocessableEntity)
			assert.Equal(t, pd.Title, "Unprocessable Entity")

			if tt.wantMinViolations > 0 && len(pd.Invalid) < tt.wantMinViolations {
				t.Fatalf("expected at least %d violations, got %d: %+v", tt.wantMinViolations, len(pd.Invalid), pd.Invalid)
			}
			assertViolationFound(t, tt.wantCode, pd.Invalid)
		})
	}

	t.Run("validate on path params", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/validate-path/0")
		assert.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, resp.StatusCode, http.StatusUnprocessableEntity)
	})

	t.Run("validate on query params", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/validate-query?sort=invalid")
		assert.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, resp.StatusCode, http.StatusUnprocessableEntity)
	})

	t.Run("embedded struct with valid data", func(t *testing.T) {
		resp, err := http.Post(server.URL+"/validate-embedded", "application/json", strings.NewReader(`{"name":"alice"}`))
		assert.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, resp.StatusCode, http.StatusOK)
	})
}

func assertViolationFound(t *testing.T, wantCode string, violations []nova.FieldViolation) {
	t.Helper()
	for _, v := range violations {
		if v.Code == wantCode {
			return
		}
	}
	t.Errorf("expected violation with code %q, got %+v", wantCode, violations)
}

func TestJSONMarshalFailureReturns500(t *testing.T) {
	type BadMarshalResponse struct {
		Fn func()
	}
	type Req struct{}

	app := nova.NewApplication()
	nova.Get(app, "/bad-marshal", func(ctx *nova.Context, req Req) (BadMarshalResponse, error) {
		return BadMarshalResponse{}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/bad-marshal")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, http.StatusInternalServerError, pd.Status)
	assert.Equal(t, "Internal Server Error", pd.Title)
}

func TestRequestBodyTooLarge(t *testing.T) {
	type Req struct {
		Data string `json:"data"`
	}
	type Res struct{}

	app := nova.NewApplication()
	nova.Post(app, "/large", func(ctx *nova.Context, req Req) (Res, error) {
		return Res{}, nil
	})

	server := httptest.NewServer(app)
	defer server.Close()

	body := strings.NewReader(`{"data":"` + strings.Repeat("x", 11<<20) + `"}`)
	resp, err := http.Post(server.URL+"/large", "application/json", body)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)

	var pd nova.ProblemDetail
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(&pd))
	assert.Equal(t, http.StatusRequestEntityTooLarge, pd.Status)
	assert.True(t, strings.Contains(pd.Detail, "too large"))
}
