package nova

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unsafe"
)

type validationType int

const (
	required validationType = iota
	min
	max
	oneOf
	alpha
	alphanum
	email
)

type validationRule struct {
	vtype  validationType
	param  string
	vals   []string // pre-split values for oneOf
	name   string
	offset uintptr
	kind   reflect.Kind
}

type elementValidationPlan struct {
	sliceOffset  uintptr
	isPtr        bool
	parentName   string
	elemType     reflect.Type
	rules        []validationRule
	elementPlans []elementValidationPlan
	structPlans  []structValidationPlan
}

type structValidationPlan struct {
	pointerOffset uintptr
	parentName    string
	rules         []validationRule
	elementPlans  []elementValidationPlan
	structPlans   []structValidationPlan
}

type validationPlan struct {
	rules        []validationRule
	elementPlans []elementValidationPlan
	structPlans  []structValidationPlan
}

func parseValidateTag(tag string) []validationRule {
	if tag == "" {
		return nil
	}
	parts := strings.Split(tag, ",")
	rules := make([]validationRule, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, hasValue := strings.Cut(part, "=")
		if !hasValue {
			value = ""
		}
		switch key {
		case "required":
			rules = append(rules, validationRule{vtype: required})
		case "min":
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				panic(fmt.Sprintf("nova: invalid numeric parameter %q for validate tag min", value))
			}
			rules = append(rules, validationRule{vtype: min, param: value})
		case "max":
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				panic(fmt.Sprintf("nova: invalid numeric parameter %q for validate tag max", value))
			}
			rules = append(rules, validationRule{vtype: max, param: value})
		case "oneof":
			rules = append(rules, validationRule{vtype: oneOf, param: value, vals: strings.Fields(value)})
		case "alpha":
			rules = append(rules, validationRule{vtype: alpha})
		case "alphanum":
			rules = append(rules, validationRule{vtype: alphanum})
		case "email":
			rules = append(rules, validationRule{vtype: email})
		default:
			panic(fmt.Sprintf("nova: unknown validate tag key %q", key))
		}
	}
	return rules
}

func typeCheckRule(rule *validationRule) {
	switch rule.vtype {
	case alpha, alphanum, email:
		if rule.kind != reflect.String {
			panic(fmt.Sprintf("nova: validate tag %s is not supported on kind %s", tagName(rule.vtype), rule.kind))
		}
	case required, min, max:
		switch rule.kind {
		case reflect.String, reflect.Slice:
		default:
			if !isIntKind(rule.kind) && !isUintKind(rule.kind) && !isFloatKind(rule.kind) {
				panic(fmt.Sprintf("nova: validate tag %s is not supported on kind %s", tagName(rule.vtype), rule.kind))
			}
		}
	case oneOf:
		switch rule.kind {
		case reflect.String:
		default:
			if !isIntKind(rule.kind) && !isUintKind(rule.kind) && !isFloatKind(rule.kind) {
				panic(fmt.Sprintf("nova: validate tag %s is not supported on kind %s", tagName(rule.vtype), rule.kind))
			}
		}
	}
}

func tagName(v validationType) string {
	switch v {
	case required:
		return "required"
	case min:
		return "min"
	case max:
		return "max"
	case oneOf:
		return "oneof"
	case alpha:
		return "alpha"
	case alphanum:
		return "alphanum"
	case email:
		return "email"
	default:
		return "unknown"
	}
}

func isIntKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	}
	return false
}

func isUintKind(k reflect.Kind) bool {
	switch k {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func isFloatKind(k reflect.Kind) bool {
	switch k {
	case reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func readInt64(ptr unsafe.Pointer, k reflect.Kind) (int64, bool) {
	switch k {
	case reflect.Int:
		return int64(*(*int)(ptr)), true
	case reflect.Int8:
		return int64(*(*int8)(ptr)), true
	case reflect.Int16:
		return int64(*(*int16)(ptr)), true
	case reflect.Int32:
		return int64(*(*int32)(ptr)), true
	case reflect.Int64:
		return *(*int64)(ptr), true
	default:
		return 0, false
	}
}

func readUint64(ptr unsafe.Pointer, k reflect.Kind) (uint64, bool) {
	switch k {
	case reflect.Uint:
		return uint64(*(*uint)(ptr)), true
	case reflect.Uint8:
		return uint64(*(*uint8)(ptr)), true
	case reflect.Uint16:
		return uint64(*(*uint16)(ptr)), true
	case reflect.Uint32:
		return uint64(*(*uint32)(ptr)), true
	case reflect.Uint64:
		return *(*uint64)(ptr), true
	default:
		return 0, false
	}
}

func readFloat64(ptr unsafe.Pointer, k reflect.Kind) (float64, bool) {
	switch k {
	case reflect.Float32:
		return float64(*(*float32)(ptr)), true
	case reflect.Float64:
		return *(*float64)(ptr), true
	default:
		return 0, false
	}
}

func readSliceData(ptr unsafe.Pointer) unsafe.Pointer {
	return *(*unsafe.Pointer)(ptr)
}

func readSliceLen(ptr unsafe.Pointer) int {
	return *(*int)(unsafe.Pointer(uintptr(ptr) + unsafe.Sizeof(uintptr(0))))
}

func validateRequest[Req any](req *Req, plan *validationPlan) error {
	var v Validator

	ptr := unsafe.Pointer(req)

	for _, rule := range plan.rules {
		fieldPtr := unsafe.Pointer(uintptr(ptr) + rule.offset)
		applyRule(&v, fieldPtr, rule)
	}

	for _, ep := range plan.elementPlans {
		validateElements(&v, ptr, ep)
	}

	for _, sp := range plan.structPlans {
		validateStruct(&v, ptr, sp)
	}

	return v.ErrorOrNil()
}

func validateElements(v *Validator, basePtr unsafe.Pointer, ep elementValidationPlan) {
	slicePtr := unsafe.Pointer(uintptr(basePtr) + ep.sliceOffset)

	if ep.isPtr {
		indirect := *(*unsafe.Pointer)(slicePtr)
		if indirect == nil {
			return
		}
		slicePtr = indirect
	}

	dataPtr := readSliceData(slicePtr)
	length := readSliceLen(slicePtr)
	elemSize := ep.elemType.Size()

	for i := 0; i < length; i++ {
		elemPtr := unsafe.Pointer(uintptr(dataPtr) + uintptr(i)*elemSize)
		prefix := fmt.Sprintf("%s[%d].", ep.parentName, i)
		for _, rule := range ep.rules {
			r := rule
			r.name = prefix + rule.name
			fieldPtr := unsafe.Pointer(uintptr(elemPtr) + r.offset)
			applyRule(v, fieldPtr, r)
		}
		for _, childEp := range ep.elementPlans {
			childEp.parentName = prefix + childEp.parentName
			validateElements(v, elemPtr, childEp)
		}
		for _, childSp := range ep.structPlans {
			childSp.parentName = prefix + childSp.parentName
			validateStruct(v, elemPtr, childSp)
		}
	}
}

func validateStruct(v *Validator, basePtr unsafe.Pointer, sp structValidationPlan) {
	ptr := unsafe.Pointer(uintptr(basePtr) + sp.pointerOffset)
	dataPtr := *(*unsafe.Pointer)(ptr)
	if dataPtr == nil {
		return
	}
	for _, rule := range sp.rules {
		fieldPtr := unsafe.Pointer(uintptr(dataPtr) + rule.offset)
		applyRule(v, fieldPtr, rule)
	}
	for _, ep := range sp.elementPlans {
		validateElements(v, dataPtr, ep)
	}
	for _, sp2 := range sp.structPlans {
		validateStruct(v, dataPtr, sp2)
	}
}

func isZero(ptr unsafe.Pointer, kind reflect.Kind) bool {
	switch {
	case kind == reflect.String:
		return *(*string)(ptr) == ""
	case isIntKind(kind):
		n, ok := readInt64(ptr, kind)
		return ok && n == 0
	case isUintKind(kind):
		n, ok := readUint64(ptr, kind)
		return ok && n == 0
	case isFloatKind(kind):
		n, ok := readFloat64(ptr, kind)
		return ok && n == 0
	case kind == reflect.Slice:
		return readSliceLen(ptr) == 0
	}
	return false
}

func applyRule(v *Validator, fieldPtr unsafe.Pointer, rule validationRule) {
	switch rule.vtype {
	case required:
		if isZero(fieldPtr, rule.kind) {
			msg := "must not be empty"
			if isIntKind(rule.kind) || isUintKind(rule.kind) || isFloatKind(rule.kind) {
				msg = "must not be zero"
			}
			v.Add(rule.name, msg, "required")
		}

	case min:
		switch {
		case rule.kind == reflect.String:
			if s := *(*string)(fieldPtr); len(s) < atoi(rule.param) {
				v.Add(rule.name, fmt.Sprintf("must be at least %s characters", rule.param), "min")
			}
		case isIntKind(rule.kind):
			if n, ok := readInt64(fieldPtr, rule.kind); ok {
				minVal, _ := strconv.ParseInt(rule.param, 10, 64)
				if n < minVal {
					v.Add(rule.name, fmt.Sprintf("must be at least %d", minVal), "min")
				}
			}
		case isUintKind(rule.kind):
			if n, ok := readUint64(fieldPtr, rule.kind); ok {
				minVal, _ := strconv.ParseUint(rule.param, 10, 64)
				if n < minVal {
					v.Add(rule.name, fmt.Sprintf("must be at least %d", minVal), "min")
				}
			}
		case isFloatKind(rule.kind):
			if n, ok := readFloat64(fieldPtr, rule.kind); ok {
				minVal, _ := strconv.ParseFloat(rule.param, 64)
				if n < minVal {
					v.Add(rule.name, fmt.Sprintf("must be at least %v", minVal), "min")
				}
			}
		case rule.kind == reflect.Slice:
			if readSliceLen(fieldPtr) < atoi(rule.param) {
				v.Add(rule.name, fmt.Sprintf("must have at least %s items", rule.param), "min")
			}
		}

	case max:
		switch {
		case rule.kind == reflect.String:
			if s := *(*string)(fieldPtr); len(s) > atoi(rule.param) {
				v.Add(rule.name, fmt.Sprintf("must be no more than %s characters", rule.param), "max")
			}
		case isIntKind(rule.kind):
			if n, ok := readInt64(fieldPtr, rule.kind); ok {
				maxVal, _ := strconv.ParseInt(rule.param, 10, 64)
				if n > maxVal {
					v.Add(rule.name, fmt.Sprintf("must be no more than %d", maxVal), "max")
				}
			}
		case isUintKind(rule.kind):
			if n, ok := readUint64(fieldPtr, rule.kind); ok {
				maxVal, _ := strconv.ParseUint(rule.param, 10, 64)
				if n > maxVal {
					v.Add(rule.name, fmt.Sprintf("must be no more than %d", maxVal), "max")
				}
			}
		case isFloatKind(rule.kind):
			if n, ok := readFloat64(fieldPtr, rule.kind); ok {
				maxVal, _ := strconv.ParseFloat(rule.param, 64)
				if n > maxVal {
					v.Add(rule.name, fmt.Sprintf("must be no more than %v", maxVal), "max")
				}
			}
		case rule.kind == reflect.Slice:
			if readSliceLen(fieldPtr) > atoi(rule.param) {
				v.Add(rule.name, fmt.Sprintf("must have no more than %s items", rule.param), "max")
			}
		}

	case oneOf:
		vals := rule.vals
		if vals == nil {
			vals = strings.Fields(rule.param)
		}
		switch {
		case rule.kind == reflect.String:
			if !slices.Contains(vals, *(*string)(fieldPtr)) {
				v.Add(rule.name, fmt.Sprintf("must be one of [%s]", rule.param), "oneof")
			}
		case isIntKind(rule.kind):
			if n, ok := readInt64(fieldPtr, rule.kind); ok {
				if !slices.Contains(vals, strconv.FormatInt(n, 10)) {
					v.Add(rule.name, fmt.Sprintf("must be one of [%s]", rule.param), "oneof")
				}
			}
		case isUintKind(rule.kind):
			if n, ok := readUint64(fieldPtr, rule.kind); ok {
				if !slices.Contains(vals, strconv.FormatUint(n, 10)) {
					v.Add(rule.name, fmt.Sprintf("must be one of [%s]", rule.param), "oneof")
				}
			}
		case isFloatKind(rule.kind):
			if n, ok := readFloat64(fieldPtr, rule.kind); ok {
				if !slices.Contains(vals, strconv.FormatFloat(n, 'f', -1, 64)) {
					v.Add(rule.name, fmt.Sprintf("must be one of [%s]", rule.param), "oneof")
				}
			}
		}

	case alpha:
		if s := *(*string)(fieldPtr); !isAlpha(s) {
			v.Add(rule.name, "must contain only letters", "alpha")
		}

	case alphanum:
		if s := *(*string)(fieldPtr); !isAlphanum(s) {
			v.Add(rule.name, "must contain only letters and digits", "alphanum")
		}

	case email:
		if s := *(*string)(fieldPtr); !isEmail(s) {
			v.Add(rule.name, "must be a valid email address", "email")
		}
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isAlphanum(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') {
		return false
	}
	local := s[:at]
	domain := s[at+1:]
	if local == "" || domain == "" {
		return false
	}
	if !strings.Contains(domain, ".") {
		return false
	}
	parts := strings.Split(domain, ".")
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}
