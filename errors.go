package nova

import "net/http"

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
		Type:   "https://api.yourdomain.com/errors/bad-request",
		Title:  "Bad Request",
		Status: http.StatusBadRequest,
		Detail: detail,
	}
}

func NewNotFoundProblem(detail string, instance string) ProblemDetail {
	return ProblemDetail{
		Type:     "https://api.yourdomain.com/errors/not-found",
		Title:    "Resource Not Found",
		Status:   http.StatusNotFound,
		Detail:   detail,
		Instance: instance,
	}
}

func NewUnprocessableEntityProblem(detail string) ProblemDetail {
	return ProblemDetail{
		Type:   "https://api.yourdomain.com/errors/unprocessable-entity",
		Title:  "Unprocessable Entity",
		Status: http.StatusUnprocessableEntity,
		Detail: detail,
	}
}

func NewSingleFieldProblem(field, reason, code string) error {
	return ProblemDetail{
		Type:   "https://api.yourdomain.com/errors/unprocessable-entity",
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
		Type:    "https://api.yourdomain.com/errors/unprocessable-entity",
		Title:   "Unprocessable Entity",
		Status:  http.StatusUnprocessableEntity,
		Detail:  "The request payload failed validation.",
		Invalid: v.Violations,
	}
}
