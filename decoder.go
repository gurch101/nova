package nova

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

var errPointerEmbed = "nova: embedded pointer field %s in %s is not supported; use value embedding"

type fieldInfo struct {
	offset uintptr
	source string
	name   string
	kind   reflect.Kind
}

type decoderPlan struct {
	fields  []fieldInfo
	hasJSON bool
}

// buildPlan inspects the Req struct type at startup (once per route) and
// builds a cached decoder plan.  It extracts struct field offsets so that
// at runtime we can write to them directly via unsafe pointers instead of
// going through reflect.Value — the reflect work happens only here.
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

func collectFields(t reflect.Type, baseOffset uintptr, plan *decoderPlan) {
	for i := range t.NumField() {
		f := t.Field(i)
		absOffset := baseOffset + f.Offset

		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				panic(fmt.Sprintf(errPointerEmbed, f.Name, t))
			}
			collectFields(ft, absOffset, plan)
			continue
		}

		if tag := f.Tag.Get("path"); tag != "" {
			plan.fields = append(plan.fields, fieldInfo{
				offset: absOffset,
				source: "path",
				name:   tag,
				kind:   f.Type.Kind(),
			})
		}

		if tag := f.Tag.Get("query"); tag != "" {
			plan.fields = append(plan.fields, fieldInfo{
				offset: absOffset,
				source: "query",
				name:   tag,
				kind:   f.Type.Kind(),
			})
		}

		if tag := f.Tag.Get("json"); tag != "" {
			name := strings.Split(tag, ",")[0]
			if name != "" && name != "-" {
				plan.hasJSON = true
			}
		}
	}
}

// decodeRequest runs on every HTTP request but uses zero reflect.Value calls.
// The JSON body is decoded first via the stdlib (which internally uses reflect),
// then path/query values are written directly through pre-computed unsafe offsets.
func decodeRequest[Req any](r *http.Request, plan *decoderPlan) (req Req, err error) {
	if plan.hasJSON && r.Body != nil {
		switch err := json.NewDecoder(r.Body).Decode(&req); err {
		case nil, io.EOF:
		default:
			return req, fmt.Errorf("decoding json body: %w", err)
		}
	}

	for _, fi := range plan.fields {
		var raw string
		switch fi.source {
		case "path":
			raw = r.PathValue(fi.name)
		case "query":
			raw = r.URL.Query().Get(fi.name)
		}
		if raw == "" {
			continue
		}

		ptr := unsafe.Pointer(uintptr(unsafe.Pointer(&req)) + fi.offset)
		if err = setFieldUnsafe(ptr, fi.kind, raw); err != nil {
			return req, fmt.Errorf("decoding %s field %q: %w", fi.source, fi.name, err)
		}
	}

	return req, nil
}

func setFieldUnsafe(ptr unsafe.Pointer, kind reflect.Kind, raw string) error {
	switch kind {
	case reflect.String:
		*(*string)(ptr) = raw
	case reflect.Int:
		n, e := strconv.ParseInt(raw, 10, strconv.IntSize)
		if e != nil {
			return fmt.Errorf("cannot parse %q as int", raw)
		}
		*(*int)(ptr) = int(n)
	case reflect.Int8:
		n, e := strconv.ParseInt(raw, 10, 8)
		if e != nil {
			return fmt.Errorf("cannot parse %q as int8", raw)
		}
		*(*int8)(ptr) = int8(n)
	case reflect.Int16:
		n, e := strconv.ParseInt(raw, 10, 16)
		if e != nil {
			return fmt.Errorf("cannot parse %q as int16", raw)
		}
		*(*int16)(ptr) = int16(n)
	case reflect.Int32:
		n, e := strconv.ParseInt(raw, 10, 32)
		if e != nil {
			return fmt.Errorf("cannot parse %q as int32", raw)
		}
		*(*int32)(ptr) = int32(n)
	case reflect.Int64:
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil {
			return fmt.Errorf("cannot parse %q as int64", raw)
		}
		*(*int64)(ptr) = n
	case reflect.Uint:
		n, e := strconv.ParseUint(raw, 10, strconv.IntSize)
		if e != nil {
			return fmt.Errorf("cannot parse %q as uint", raw)
		}
		*(*uint)(ptr) = uint(n)
	case reflect.Uint8:
		n, e := strconv.ParseUint(raw, 10, 8)
		if e != nil {
			return fmt.Errorf("cannot parse %q as uint8", raw)
		}
		*(*uint8)(ptr) = uint8(n)
	case reflect.Uint16:
		n, e := strconv.ParseUint(raw, 10, 16)
		if e != nil {
			return fmt.Errorf("cannot parse %q as uint16", raw)
		}
		*(*uint16)(ptr) = uint16(n)
	case reflect.Uint32:
		n, e := strconv.ParseUint(raw, 10, 32)
		if e != nil {
			return fmt.Errorf("cannot parse %q as uint32", raw)
		}
		*(*uint32)(ptr) = uint32(n)
	case reflect.Uint64:
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			return fmt.Errorf("cannot parse %q as uint64", raw)
		}
		*(*uint64)(ptr) = n
	case reflect.Float32:
		n, e := strconv.ParseFloat(raw, 32)
		if e != nil {
			return fmt.Errorf("cannot parse %q as float32", raw)
		}
		*(*float32)(ptr) = float32(n)
	case reflect.Float64:
		n, e := strconv.ParseFloat(raw, 64)
		if e != nil {
			return fmt.Errorf("cannot parse %q as float64", raw)
		}
		*(*float64)(ptr) = n
	case reflect.Bool:
		b, e := strconv.ParseBool(raw)
		if e != nil {
			return fmt.Errorf("cannot parse %q as bool", raw)
		}
		*(*bool)(ptr) = b
	default:
		return fmt.Errorf("unsupported field type kind %d", kind)
	}
	return nil
}
