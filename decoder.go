package nova

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

const maxRequestBodySize = 10 << 20 // 10 MB

func panicPointerEmbed(fieldName string, t reflect.Type) {
	panic(fmt.Sprintf("nova: embedded pointer field %s in %s is not supported; use value embedding", fieldName, t))
}

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

// buildDecoderAndValidationPlan inspects the Req struct type at startup
// (once per route) and builds a cached decoder plan and validation plan.
// It extracts struct field offsets so that at runtime we can read/write
// them directly via unsafe pointers instead of going through reflect.Value.
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

func collectFields(t reflect.Type, baseOffset uintptr, dPlan *decoderPlan, vPlan *validationPlan) {
	collectFieldsRecursive(t, baseOffset, "", dPlan, vPlan)
}

func collectFieldsRecursive(t reflect.Type, baseOffset uintptr, namePrefix string, dPlan *decoderPlan, vPlan *validationPlan) {
	for i := range t.NumField() {
		f := t.Field(i)
		absOffset := baseOffset + f.Offset

		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				panicPointerEmbed(f.Name, t)
			}
			collectFieldsRecursive(ft, absOffset, namePrefix, dPlan, vPlan)
			continue
		}

		pathTag := f.Tag.Get("path")
		queryTag := f.Tag.Get("query")
		jsonTag := f.Tag.Get("json")

		name := ""
		if jsonTag != "" {
			n, _, _ := strings.Cut(jsonTag, ",")
			if n != "" && n != "-" {
				name = n
			}
		}
		if name == "" && pathTag != "" {
			name = pathTag
		}
		if name == "" && queryTag != "" {
			name = queryTag
		}
		if name == "" {
			name = f.Name
		}

		fullName := name
		if namePrefix != "" {
			fullName = namePrefix + "." + name
		}

		if pathTag != "" && dPlan != nil {
			dPlan.fields = append(dPlan.fields, fieldInfo{
				offset: absOffset,
				source: "path",
				name:   pathTag,
				kind:   f.Type.Kind(),
			})
		}

		if queryTag != "" && dPlan != nil {
			dPlan.fields = append(dPlan.fields, fieldInfo{
				offset: absOffset,
				source: "query",
				name:   queryTag,
				kind:   f.Type.Kind(),
			})
		}

		if jsonTag != "" && dPlan != nil {
			n, _, _ := strings.Cut(jsonTag, ",")
			if n != "" && n != "-" {
				dPlan.hasJSON = true
			}
		}

		if tag := f.Tag.Get("validate"); tag != "" {
			rules := parseValidateTag(tag)
			for i := range rules {
				rules[i].name = fullName
				rules[i].offset = absOffset
				rules[i].kind = f.Type.Kind()
				typeCheckRule(&rules[i])
				vPlan.rules = append(vPlan.rules, rules[i])
			}
		}

		ft := f.Type
		isPtr := ft.Kind() == reflect.Ptr
		if isPtr {
			ft = ft.Elem()
		}

		switch {
		case ft.Kind() == reflect.Struct:
			if isPtr {
				child := &validationPlan{}
				collectFieldsRecursive(ft, 0, fullName, nil, child)
				if len(child.rules) > 0 || len(child.elementPlans) > 0 || len(child.structPlans) > 0 {
					vPlan.structPlans = append(vPlan.structPlans, structValidationPlan{
						pointerOffset: absOffset,
						parentName:    fullName,
						rules:         child.rules,
						elementPlans:  child.elementPlans,
						structPlans:   child.structPlans,
					})
				}
			} else {
				collectFieldsRecursive(ft, absOffset, fullName, nil, vPlan)
			}

		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			elemType := ft.Elem()
			child := &validationPlan{}
			collectFieldsRecursive(elemType, 0, "", nil, child)
			if len(child.rules) > 0 || len(child.elementPlans) > 0 || len(child.structPlans) > 0 {
				vPlan.elementPlans = append(vPlan.elementPlans, elementValidationPlan{
					sliceOffset:  absOffset,
					isPtr:        isPtr,
					parentName:   fullName,
					elemType:     elemType,
					rules:        child.rules,
					elementPlans: child.elementPlans,
					structPlans:  child.structPlans,
				})
			}
		}
	}
}

// decodeRequest runs on every HTTP request but uses zero reflect.Value calls.
// The JSON body is decoded first via the stdlib (which internally uses reflect),
// then path/query values are written directly through pre-computed unsafe offsets.
func decodeRequest[Req any](r *http.Request, plan *decoderPlan) (req Req, err error) {
	if plan.hasJSON && r.Body != nil {
		limited := http.MaxBytesReader(nil, r.Body, maxRequestBodySize)
		switch err := json.NewDecoder(limited).Decode(&req); err {
		case nil, io.EOF:
		default:
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return req, fmt.Errorf("request body too large (max %d bytes): %w", maxRequestBodySize, maxErr)
			}
			return req, fmt.Errorf("decoding json body: %w", err)
		}
	}

	var query url.Values
	for _, fi := range plan.fields {
		var raw string
		switch fi.source {
		case "path":
			raw = r.PathValue(fi.name)
		case "query":
			if query == nil {
				query = r.URL.Query()
			}
			raw = query.Get(fi.name)
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
