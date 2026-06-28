# Tag-Driven Validator for Nova

## Overview

Add a `validate` struct tag that declaratively validates request fields. Validation rules are parsed once at route registration time (single pass alongside the existing decoder plan) and executed per-request with **zero reflection** using the same `unsafe` offset technique. All violations are returned as an HTTP 422 (Unprocessable Entity) `ProblemDetail`.

---

## 1. Design Principles

- **Zero runtime reflection** — struct walking and tag parsing happen once at startup.
- **Single pass** — the existing `collectFields` walk is extended to also read `validate` tags, avoiding a second struct traversal.
- **Zero new dependencies** — standard library only.
- **Consistent error format** — reuses the existing `Validator` / `FieldViolation` / `ProblemDetail` infrastructure.
- **Fail fast at startup** — incompatible tag+type combinations panic during registration, not at runtime.

---

## 2. Tag Syntax

```go
type CreateUserRequest struct {
    Name     string   `json:"name"     validate:"required,min=2,max=100"`
    Age      int      `json:"age"      validate:"required,min=0,max=150"`
    Tags     []string `json:"tags"     validate:"min=1,max=10"`
    Role     string   `json:"role"     validate:"oneof=admin user viewer"`
    Email    string   `json:"email"    validate:"required,email"`
    Username string   `json:"username" validate:"required,alphanum"`
    Code     string   `json:"code"     validate:"alpha"`
    Password string   `json:"password" validate:"min=8"`
}
```

- Tags are comma-separated (`validate:"required,min=3"`).
- Parameterized tags use `=` after the tag name (`min=3`).
- `oneof` uses space-separated values (`oneof=admin user`).
- A missing or empty `validate` tag is silently skipped.

---

## 3. Supported Tags

| Tag | Applies To | Behavior | Error Code |
|---|---|---|---|
| `required` | string, numbers (int/uint/float/\*), slices | String: empty → fail. Number: zero → fail. Slice: nil or empty → fail. | `required` |
| `min=N` | string, numbers, slices | String/slice: `len(value) < N` → fail. Number: `value < N` → fail. | `min` |
| `max=N` | string, numbers, slices | String/slice: `len(value) > N` → fail. Number: `value > N` → fail. | `max` |
| `oneof=A B C` | string, number | Value must exactly match one of the space-separated literals (string comparison). | `oneof` |
| `alpha` | string | Any non-letter rune → fail. | `alpha` |
| `alphanum` | string | Any non-letter and non-digit rune → fail. | `alphanum` |
| `email` | string | Must contain exactly one `@`, non-empty local part, non-empty domain, domain contains `.`. | `email` |

\* Booleans are intentionally **not** supported for any validation tag; plan-time panic if attempted.

### Per-tag type compatibility (enforced at plan time)

| Tag | Allowed kinds |
|---|---|
| `required` | String, Int\*, Uint\*, Float\*, Slice |
| `min`/`max` | String, Int\*, Uint\*, Float\*, Slice |
| `oneof` | String, Int\*, Uint\*, Float\* |
| `alpha`/`alphanum`/`email` | String only |

Plan-time panic on any disallowed combination.

---

## 4. Error Response Format

Single 422 response with all field violations collected in `invalidParams`:

```json
{
  "type": "https://api.yourdomain.com/errors/unprocessable-entity",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "The request payload failed validation.",
  "invalidParams": [
    {"field": "name",  "reason": "must be at least 2 characters",       "code": "min"},
    {"field": "email", "reason": "must be a valid email address",        "code": "email"},
    {"field": "age",   "reason": "must be a positive number",            "code": "min"},
    {"field": "role",  "reason": "must be one of [admin user viewer]",   "code": "oneof"}
  ]
}
```

Reuses the existing `ProblemDetail.Invalid` (`[]FieldViolation`) field; no new response types.

---

## 5. Implementation

### 5.1 New Types (`decoder.go` or new shared location)

```go
type validationType int

const (
    validateRequired validationType = iota
    validateMin
    validateMax
    validateOneOf
    validateAlpha
    validateAlphanum
    validateEmail
)

type validationRule struct {
    vtype  validationType
    param  string       // parameter value (e.g. "3" for min=3, "admin user" for oneof)
    name   string       // field name for error output
    offset uintptr      // pre-computed unsafe offset
    kind   reflect.Kind // field kind for runtime dispatch
}

type validationPlan struct {
    rules []validationRule
}
```

### 5.2 Single-Pass Plan Building (`decoder.go`)

**Before** (current):
```go
func buildPlan[Req any]() *decoderPlan {
    var req Req
    t := reflect.TypeOf(req)
    if t.Kind() == reflect.Ptr {
        t = t.Elem()
    }
    plan := &decoderPlan{}
    collectFields(t, 0, plan)
    return plan
}
```

**After**:
```go
func buildDecoderAndValidationPlan[Req any]() (*decoderPlan, *validationPlan) {
    var req Req
    t := reflect.TypeOf(req)
    if t.Kind() == reflect.Ptr {
        t = t.Elem()
    }
    dPlan := &decoderPlan{}
    vPlan := &validationPlan{}
    collectFields(t, 0, dPlan, vPlan)
    return dPlan, vPlan
}
```

### 5.3 Extended `collectFields`

Add `vPlan *validationPlan` parameter. In the per-field loop, after reading `path`/`query`/`json` tags:

```go
// Resolve display name for error output (json > path > query > struct field)
name := ""
if tag := f.Tag.Get("json"); tag != "" {
    n, _, _ := strings.Cut(tag, ",")
    if n != "" && n != "-" {
        name = n
    }
}
if name == "" {
    if tag := f.Tag.Get("path"); tag != "" {
        name = tag
    }
}
if name == "" {
    if tag := f.Tag.Get("query"); tag != "" {
        name = tag
    }
}
if name == "" {
    name = f.Name
}

// ---- decode plan (existing) ----
if tag := f.Tag.Get("path"); tag != "" {
    dPlan.fields = append(dPlan.fields, fieldInfo{offset: absOffset, source: "path", name: tag, kind: f.Type.Kind()})
}
if tag := f.Tag.Get("query"); tag != "" {
    dPlan.fields = append(dPlan.fields, fieldInfo{offset: absOffset, source: "query", name: tag, kind: f.Type.Kind()})
}
if tag := f.Tag.Get("json"); tag != "" {
    n, _ := strings.Cut(tag, ",")
    if n != "" && n != "-" {
        dPlan.hasJSON = true
    }
}

// ---- validation plan (NEW) ----
if tag := f.Tag.Get("validate"); tag != "" {
    rules := parseValidateTag(tag)
    for i := range rules {
        rules[i].name = name
        rules[i].offset = absOffset
        rules[i].kind = f.Type.Kind()
        typeCheckRule(&rules[i]) // panics on type incompatibility
        vPlan.rules = append(vPlan.rules, rules[i])
    }
}
```

The `collectFields` recursion for embedded structs passes `vPlan` through unchanged, so validate tags on embedded fields are collected naturally.

### 5.4 Tag Parsing (`validator.go`)

```go
func parseValidateTag(tag string) []validationRule
```

- Split by `,`.
- For each segment: split by first `=` to get `[key, value]` (or just `key` for non-parameterized tags).
- Match key against known tags, emit the corresponding `validationRule` with `param` set where applicable.
- Unknown tag key → panic.

### 5.5 Type Checking (`validator.go`)

```go
func typeCheckRule(rule *validationRule)
```

Verify `rule.kind` is compatible with `rule.vtype`. Panic with a descriptive message if not.

### 5.6 Runtime Validation (`validator.go`)

```go
func validateRequest(req any, plan *validationPlan) error
```

- Iterate over `plan.rules`.
- For each rule, compute `ptr := unsafe.Pointer(uintptr(unsafe.Pointer(req)) + rule.offset)`.
- Dispatch on `(rule.vtype, rule.kind)` to run the check.
- Accumulate failures into a `Validator`.
- Return `validator.ErrorOrNil()`.

#### Runtime unsafe reads (zero reflection)

| Kind | Read method | Used for |
|---|---|---|
| `reflect.String` | `*(*string)(ptr)`, then `len(s)` | Length checks, rune iteration |
| `reflect.Int`, `Int8/16/32/64` | `*(*int64)(ptr)` (cast from specific size) | Numeric comparison |
| `reflect.Uint`, `Uint8/16/32/64` | `*(*uint64)(ptr)` (cast from specific size) | Numeric comparison |
| `reflect.Float32/64` | `*(*float64)(ptr)` | Numeric comparison |
| `reflect.Slice` | `(*[2]uintptr)(ptr)[1]` reads the `Len` field of the slice header | Length checks |

#### Validation logic

| Tag | Runtime check |
|---|---|
| `required` (string) | `s == ""` |
| `required` (int) | `n == 0` |
| `required` (uint) | `n == 0` |
| `required` (float) | `n == 0` |
| `required` (slice) | nil check via `*(*unsafe.Pointer)(ptr) == nil` or `Len == 0` |
| `min` (string) | `len(s) < paramInt` |
| `min` (int) | `n < paramInt` |
| `min` (slice) | `Len < paramInt` |
| `max` (string) | `len(s) > paramInt` |
| `max` (int) | `n > paramInt` |
| `max` (slice) | `Len > paramInt` |
| `oneof` (string) | `s` not in `strings.Fields(param)` |
| `oneof` (int) | string representation not in `strings.Fields(param)` |
| `alpha` | any rune fails `unicode.IsLetter` |
| `alphanum` | any rune fails `unicode.IsLetter && unicode.IsDigit` |
| `email` | `isEmail(s)` fails |

### 5.7 Helper Functions (`validator.go`)

```go
func isAlpha(s string) bool
func isAlphanum(s string) bool
func isEmail(s string) bool
```

- `isAlpha` — iterate runes, all must pass `unicode.IsLetter`.
- `isAlphanum` — iterate runes, all must pass `unicode.IsLetter` || `unicode.IsDigit`.
- `isEmail` — simple validation: exactly one `@`, non-empty local part, non-empty domain, domain contains `.`.

### 5.8 Registration Changes (`nova.go`)

```go
func register[Req, Res any](app *Application, method, pattern string, handler func(ctx *Context, req Req) (Res, error)) {
    plan, vPlan := buildDecoderAndValidationPlan[Req]()   // single-pass

    fullPattern := method + " " + pattern
    app.mux.HandleFunc(fullPattern, func(w http.ResponseWriter, r *http.Request) {
        req, err := decodeRequest[Req](r, plan)
        if err != nil {
            // existing max-body / bad-request error handling...
            return
        }

        // NEW: run validation
        if len(vPlan.rules) > 0 {
            if err := validateRequest(&req, vPlan); err != nil {
                writeError(w, err, r.Context())
                return
            }
        }

        // existing handler call...
        res, err := handler(ctx, req)
        // ...
    })
}
```

---

## 6. Edge Cases

| Scenario | Expected behavior |
|---|---|
| `required` on zero-value int (0) | Fails (`code: "required"`) |
| `required` on bool | Panics at plan time (bool not in allowed kinds) |
| `min`/`max` on bool | Panics at plan time |
| `alpha`/`alphanum`/`email` on non-string | Panics at plan time |
| `oneof` on bool | Panics at plan time |
| `validate:""` (empty tag) | Skipped, no rules emitted |
| `min=0` on string | Always passes (any string has len >= 0) |
| `max=0` on string | Only passes if string is empty |
| `min=0` on slice | Always passes |
| Slice with nil body for `required` | Fails (nil slice → zero length) |
| Multiple violations | All reported in a single 422 response |
| No `validate` tags on any field | `vPlan.rules` is empty, validation skipped (zero per-request overhead) |
| Embedded struct with `validate` tags | Collected recursively, same as decoder |

---

## 7. Tests

### `validator_test.go` — Unit tests

| Test | What it covers |
|---|---|
| `TestParseValidateTag` | Correct rules emitted from various tag strings |
| `TestParseValidateTag_Empty` | Empty tag returns nil |
| `TestParseValidateTag_UnknownTag` | Panics on unknown tag key |
| `TestTypeCheckRule` | Panics on incompatible tag+kind combos |
| `TestIsAlpha` | Pure letters passes; digits/symbols/spaces fail |
| `TestIsAlphanum` | Letters+digits passes; symbols fail |
| `TestIsEmail` | Valid and invalid email cases |
| `TestValidateRequired` | Each allowed kind: passes and fails |
| `TestValidateMin` | String/int/slice below, at, above threshold |
| `TestValidateMax` | String/int/slice below, at, above threshold |
| `TestValidateOneOf` | In-set passes; out-of-set fails |
| `TestValidateAlpha` | Alpha string passes; non-alpha fails |
| `TestValidateAlphanum` | Alphanum string passes; other chars fail |
| `TestValidateEmail` | Valid/invalid email addresses |
| `TestValidateMultipleErrors` | Two failing fields produce two violations |

### `nova_test.go` — Single table-driven integration test

```
TestValidateRequest
```

Covers all tags and edge cases through the full HTTP stack (post to server, decode ProblemDetail response).

| Case | Assertions |
|---|---|
| valid request (all fields ok) | 200, correct response body |
| missing required string | 422, `code: "required"` |
| zero-value required int | 422, `code: "required"` |
| nil required slice | 422, `code: "required"` |
| string below min length | 422, `code: "min"` |
| string above max length | 422, `code: "max"` |
| int below min value | 422, `code: "min"` |
| int above max value | 422, `code: "max"` |
| slice below min length | 422, `code: "min"` |
| slice above max length | 422, `code: "max"` |
| value not in oneof set | 422, `code: "oneof"` |
| non-alpha characters | 422, `code: "alpha"` |
| non-alphanum characters | 422, `code: "alphanum"` |
| invalid email | 422, `code: "email"` |
| multiple simultaneous violations | 422 with 2+ invalidParams entries |
| validate on path params | 422 for invalid path value |
| validate on query params | 422 for invalid query value |
| struct without validate tags | 200 (unchanged behavior) |
| embedded struct with validate | 422 for invalid embedded field |
| required zero int without `required` tag | 200 (not validated) |

---

## 8. Files Changed

| File | Action | Lines |
|---|---|---|
| `validator.go` | **New** | ~200 |
| `decoder.go` | Edit: extend `buildPlan` → `buildDecoderAndValidationPlan`, extend `collectFields`, add new types | ~30 |
| `nova.go` | Edit: update `register` to call new planner and run validation | ~5 |
| `validator_test.go` | **New** | ~300 |
| `nova_test.go` | Edit: add table-driven integration test | ~100 |
