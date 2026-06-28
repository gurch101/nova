package nova

import "net/http"

var (
	errorTypePrefix                  string
	errorTypeBadRequestURL           string
	errorTypeNotFoundURL             string
	errorTypeUnprocessableEntityURL  string
	errorTypeMethodNotAllowedURL     string
	errorTypeInternalServerErrorURL  string
	errorTypeRequestEntityTooLargeURL string
)

func init() {
	SetErrorTypePrefix("https://api.yourdomain.com/errors")
}

// ErrorTypePrefix returns the base URL used for ProblemDetail type fields.
func ErrorTypePrefix() string {
	return errorTypePrefix
}

// SetErrorTypePrefix sets the base URL used for ProblemDetail type fields
// and pre-computes all error type URLs to avoid per-request string allocation.
// This should be called at startup before any requests are handled.
func SetErrorTypePrefix(prefix string) {
	errorTypePrefix = prefix
	errorTypeBadRequestURL = prefix + "/bad-request"
	errorTypeNotFoundURL = prefix + "/not-found"
	errorTypeUnprocessableEntityURL = prefix + "/unprocessable-entity"
	errorTypeMethodNotAllowedURL = prefix + "/method-not-allowed"
	errorTypeInternalServerErrorURL = prefix + "/internal-server-error"
	errorTypeRequestEntityTooLargeURL = prefix + "/request-entity-too-large"
}

type FieldViolation struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
	Code   string `json:"code"`
}

type ProblemDetail struct {
	Type     string           `json:"type"`
	Title    string           `json:"title"`
	Status   int              `json:"status"`
	Detail   string           `json:"detail,omitempty"`
	Instance string           `json:"instance,omitempty"`
	Invalid  []FieldViolation `json:"invalidParams,omitempty"`
}

func (p ProblemDetail) Error() string {
	return p.Title + ": " + p.Detail
}

func (p ProblemDetail) StatusCode() int {
	return p.Status
}

func NewBadRequestProblem(detail string) ProblemDetail {
	return ProblemDetail{
		Type:   errorTypeBadRequestURL,
		Title:  "Bad Request",
		Status: http.StatusBadRequest,
		Detail: detail,
	}
}

func NewNotFoundProblem(detail string, instance string) ProblemDetail {
	return ProblemDetail{
		Type:     errorTypeNotFoundURL,
		Title:    "Resource Not Found",
		Status:   http.StatusNotFound,
		Detail:   detail,
		Instance: instance,
	}
}

func NewUnprocessableEntityProblem(detail string) ProblemDetail {
	return ProblemDetail{
		Type:   errorTypeUnprocessableEntityURL,
		Title:  "Unprocessable Entity",
		Status: http.StatusUnprocessableEntity,
		Detail: detail,
	}
}

func NewMethodNotAllowedProblem(detail string) ProblemDetail {
	return ProblemDetail{
		Type:   errorTypeMethodNotAllowedURL,
		Title:  "Method Not Allowed",
		Status: http.StatusMethodNotAllowed,
		Detail: detail,
	}
}

func NewSingleFieldProblem(field, reason, code string) error {
	return ProblemDetail{
		Type:   errorTypeUnprocessableEntityURL,
		Title:  "Unprocessable Entity",
		Status: http.StatusUnprocessableEntity,
		Detail: "The request payload failed validation.",
		Invalid: []FieldViolation{{
			Field:  field,
			Reason: reason,
			Code:   code,
		}},
	}
}

type Validator struct {
	Violations []FieldViolation
}

func (v *Validator) Add(field, reason, code string) {
	v.Violations = append(v.Violations, FieldViolation{
		Field:  field,
		Reason: reason,
		Code:   code,
	})
}

func (v *Validator) Check(ok bool, field, reason, code string) {
	if !ok {
		v.Add(field, reason, code)
	}
}

func (v *Validator) ErrorOrNil() error {
	if len(v.Violations) == 0 {
		return nil
	}
	return ProblemDetail{
		Type:    errorTypeUnprocessableEntityURL,
		Title:   "Unprocessable Entity",
		Status:  http.StatusUnprocessableEntity,
		Detail:  "The request payload failed validation.",
		Invalid: v.Violations,
	}
}
