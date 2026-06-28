package nova

import (
	"reflect"
	"testing"

	"github.com/gurch101/nova/assert"
)

func TestParseValidateTag(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		rules := parseValidateTag("required")
		assert.Equal(t, len(rules), 1)
		assert.Equal(t, rules[0].vtype, required)
	})

	t.Run("multiple rules", func(t *testing.T) {
		rules := parseValidateTag("required,min=2,max=100")
		assert.Equal(t, len(rules), 3)
		assert.Equal(t, rules[0].vtype, required)
		assert.Equal(t, rules[1].vtype, min)
		assert.Equal(t, rules[1].param, "2")
		assert.Equal(t, rules[2].vtype, max)
		assert.Equal(t, rules[2].param, "100")
	})

	t.Run("oneof", func(t *testing.T) {
		rules := parseValidateTag("oneof=admin user viewer")
		assert.Equal(t, len(rules), 1)
		assert.Equal(t, rules[0].vtype, oneOf)
		assert.Equal(t, rules[0].param, "admin user viewer")
	})

	t.Run("alpha", func(t *testing.T) {
		rules := parseValidateTag("alpha")
		assert.Equal(t, len(rules), 1)
		assert.Equal(t, rules[0].vtype, alpha)
	})

	t.Run("alphanum", func(t *testing.T) {
		rules := parseValidateTag("alphanum")
		assert.Equal(t, len(rules), 1)
		assert.Equal(t, rules[0].vtype, alphanum)
	})

	t.Run("email", func(t *testing.T) {
		rules := parseValidateTag("email")
		assert.Equal(t, len(rules), 1)
		assert.Equal(t, rules[0].vtype, email)
	})

	t.Run("all tags", func(t *testing.T) {
		tag := "required,min=1,max=10,oneof=a b,alpha,alphanum,email"
		rules := parseValidateTag(tag)
		assert.Equal(t, len(rules), 7)
		expected := []validationType{required, min, max, oneOf, alpha, alphanum, email}
		for i, v := range expected {
			assert.Equal(t, rules[i].vtype, v)
		}
	})
}

func TestParseValidateTag_Empty(t *testing.T) {
	assert.Equal(t, len(parseValidateTag("")), 0)
	assert.Equal(t, len(parseValidateTag("   ")), 0)
}

func TestParseValidateTag_UnknownTag(t *testing.T) {
	assert.Panics(t, func() { parseValidateTag("unknown") })
}

func TestTypeCheckRule(t *testing.T) {
	passCases := []struct {
		name string
		rule validationRule
	}{
		{"alpha on string", validationRule{vtype: alpha, kind: reflect.String}},
		{"alphanum on string", validationRule{vtype: alphanum, kind: reflect.String}},
		{"email on string", validationRule{vtype: email, kind: reflect.String}},
		{"required on string", validationRule{vtype: required, kind: reflect.String}},
		{"required on int", validationRule{vtype: required, kind: reflect.Int}},
		{"oneof on string", validationRule{vtype: oneOf, kind: reflect.String}},
		{"min on slice", validationRule{vtype: min, kind: reflect.Slice}},
	}
	for _, tc := range passCases {
		t.Run(tc.name+" passes", func(t *testing.T) {
			typeCheckRule(&tc.rule)
		})
	}

	panicCases := []struct {
		name string
		rule validationRule
	}{
		{"alpha on int", validationRule{vtype: alpha, kind: reflect.Int}},
		{"alpha on bool", validationRule{vtype: alpha, kind: reflect.Bool}},
		{"alphanum on bool", validationRule{vtype: alphanum, kind: reflect.Bool}},
		{"email on bool", validationRule{vtype: email, kind: reflect.Bool}},
		{"required on bool", validationRule{vtype: required, kind: reflect.Bool}},
		{"min on bool", validationRule{vtype: min, kind: reflect.Bool}},
		{"max on bool", validationRule{vtype: max, kind: reflect.Bool}},
		{"oneof on bool", validationRule{vtype: oneOf, kind: reflect.Bool}},
		{"oneof on slice", validationRule{vtype: oneOf, kind: reflect.Slice}},
	}
	for _, tc := range panicCases {
		t.Run(tc.name+" panics", func(t *testing.T) {
			assert.Panics(t, func() { typeCheckRule(&tc.rule) })
		})
	}
}

func TestIsAlpha(t *testing.T) {
	assert.True(t, isAlpha("abc"))
	assert.True(t, isAlpha("ABC"))
	assert.False(t, isAlpha("abc123"))
	assert.False(t, isAlpha("hello world"))
	assert.False(t, isAlpha("hello!"))
	assert.True(t, isAlpha(""))
}

func TestIsAlphanum(t *testing.T) {
	assert.True(t, isAlphanum("abc123"))
	assert.True(t, isAlphanum("ABC"))
	assert.True(t, isAlphanum("123"))
	assert.False(t, isAlphanum("hello world"))
	assert.False(t, isAlphanum("hello!"))
	assert.True(t, isAlphanum(""))
}

func TestIsEmail(t *testing.T) {
	for _, email := range []string{"user@example.com", "a@b.co"} {
		assert.True(t, isEmail(email))
	}
	for _, email := range []string{"", "notanemail", "@example.com", "user@", "user@com", "user@.com", "user@com.", "user@domain", "user@domain.", "user@@example.com"} {
		assert.False(t, isEmail(email))
	}
}

func TestValidateRequired(t *testing.T) {
	t.Run("string passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "name", offset: 0, kind: reflect.String},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Name string }{"hello"}, plan))
	})

	t.Run("string fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "name", offset: 0, kind: reflect.String},
			},
		}
		err := validateRequest(&struct{ Name string }{""}, plan)
		if err == nil {
			t.Fatal("expected error")
		}
		pd := err.(ProblemDetail)
		assert.Equal(t, len(pd.Invalid), 1)
		assert.Equal(t, pd.Invalid[0].Code, "required")
	})

	t.Run("int passes non-zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "age", offset: 0, kind: reflect.Int},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Age int }{42}, plan))
	})

	t.Run("int fails zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "age", offset: 0, kind: reflect.Int},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Age int }{0}, plan))
	})

	t.Run("uint passes non-zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "count", offset: 0, kind: reflect.Uint},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Count uint }{1}, plan))
	})

	t.Run("uint fails zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "count", offset: 0, kind: reflect.Uint},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Count uint }{0}, plan))
	})

	t.Run("float passes non-zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "rating", offset: 0, kind: reflect.Float64},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Rating float64 }{3.5}, plan))
	})

	t.Run("float fails zero", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "rating", offset: 0, kind: reflect.Float64},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Rating float64 }{0}, plan))
	})

	t.Run("slice passes non-empty", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "tags", offset: 0, kind: reflect.Slice},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Tags []string }{[]string{"a"}}, plan))
	})

	t.Run("slice fails nil", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "tags", offset: 0, kind: reflect.Slice},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Tags []string }{nil}, plan))
	})

	t.Run("slice fails empty", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: required, name: "tags", offset: 0, kind: reflect.Slice},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Tags []string }{[]string{}}, plan))
	})
}

func TestValidateMin(t *testing.T) {
	t.Run("string below min fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Name string }{"ab"}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "name", offset: 0, kind: reflect.String, param: "3"},
			},
		}))
	})

	t.Run("string at min passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{"abc"}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "name", offset: 0, kind: reflect.String, param: "3"},
			},
		}))
	})

	t.Run("string above min passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{"abcd"}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "name", offset: 0, kind: reflect.String, param: "3"},
			},
		}))
	})

	t.Run("int below min fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Age int }{15}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "age", offset: 0, kind: reflect.Int, param: "18"},
			},
		}))
	})

	t.Run("int at min passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Age int }{18}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "age", offset: 0, kind: reflect.Int, param: "18"},
			},
		}))
	})

	t.Run("int above min passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Age int }{25}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "age", offset: 0, kind: reflect.Int, param: "18"},
			},
		}))
	})

	t.Run("slice below min fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Tags []string }{[]string{"a"}}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "tags", offset: 0, kind: reflect.Slice, param: "2"},
			},
		}))
	})

	t.Run("slice at min passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Tags []string }{[]string{"a", "b"}}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "tags", offset: 0, kind: reflect.Slice, param: "2"},
			},
		}))
	})

	t.Run("min=0 on string always passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{""}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "name", offset: 0, kind: reflect.String, param: "0"},
			},
		}))
	})

	t.Run("min=0 on slice always passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Tags []string }{nil}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "tags", offset: 0, kind: reflect.Slice, param: "0"},
			},
		}))
	})
}

func TestValidateMax(t *testing.T) {
	t.Run("string above max fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Name string }{"abcdef"}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "name", offset: 0, kind: reflect.String, param: "5"},
			},
		}))
	})

	t.Run("string at max passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{"abcde"}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "name", offset: 0, kind: reflect.String, param: "5"},
			},
		}))
	})

	t.Run("string below max passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{"abcd"}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "name", offset: 0, kind: reflect.String, param: "5"},
			},
		}))
	})

	t.Run("int above max fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Age int }{200}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "age", offset: 0, kind: reflect.Int, param: "150"},
			},
		}))
	})

	t.Run("int at max passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Age int }{150}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "age", offset: 0, kind: reflect.Int, param: "150"},
			},
		}))
	})

	t.Run("max=0 on empty string passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Name string }{""}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "name", offset: 0, kind: reflect.String, param: "0"},
			},
		}))
	})

	t.Run("max=0 on non-empty string fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Name string }{"a"}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "name", offset: 0, kind: reflect.String, param: "0"},
			},
		}))
	})

	t.Run("slice above max fails", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&struct{ Tags []string }{[]string{"a", "b", "c"}}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "tags", offset: 0, kind: reflect.Slice, param: "2"},
			},
		}))
	})

	t.Run("slice at max passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Tags []string }{[]string{"a", "b"}}, &validationPlan{
			rules: []validationRule{
				{vtype: max, name: "tags", offset: 0, kind: reflect.Slice, param: "2"},
			},
		}))
	})
}

func TestValidateOneOf(t *testing.T) {
	t.Run("string in set passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: oneOf, name: "role", offset: 0, kind: reflect.String, param: "admin user viewer"},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Role string }{"admin"}, plan))
	})

	t.Run("string not in set fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: oneOf, name: "role", offset: 0, kind: reflect.String, param: "admin user viewer"},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Role string }{"superadmin"}, plan))
	})

	t.Run("int in set passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: oneOf, name: "code", offset: 0, kind: reflect.Int, param: "200 404 500"},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Code int }{200}, plan))
	})

	t.Run("int not in set fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: oneOf, name: "code", offset: 0, kind: reflect.Int, param: "200 404 500"},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Code int }{418}, plan))
	})
}

func TestValidateAlpha(t *testing.T) {
	t.Run("alpha string passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: alpha, name: "code", offset: 0, kind: reflect.String},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Code string }{"ABC"}, plan))
	})

	t.Run("non-alpha string fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: alpha, name: "code", offset: 0, kind: reflect.String},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Code string }{"ABC123"}, plan))
	})
}

func TestValidateAlphanum(t *testing.T) {
	t.Run("alphanum string passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: alphanum, name: "username", offset: 0, kind: reflect.String},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Username string }{"user123"}, plan))
	})

	t.Run("non-alphanum string fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: alphanum, name: "username", offset: 0, kind: reflect.String},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Username string }{"user-123"}, plan))
	})
}

func TestValidateEmail(t *testing.T) {
	t.Run("valid email passes", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: email, name: "email", offset: 0, kind: reflect.String},
			},
		}
		assert.NoError(t, validateRequest(&struct{ Email string }{"user@example.com"}, plan))
	})

	t.Run("invalid email fails", func(t *testing.T) {
		plan := &validationPlan{
			rules: []validationRule{
				{vtype: email, name: "email", offset: 0, kind: reflect.String},
			},
		}
		assert.NotNil(t, validateRequest(&struct{ Email string }{"not-an-email"}, plan))
	})
}

func TestValidateMultipleErrors(t *testing.T) {
	plan := &validationPlan{
		rules: []validationRule{
			{vtype: required, name: "name", offset: 0, kind: reflect.String},
			{vtype: min, name: "name", offset: 0, kind: reflect.String, param: "2"},
			{vtype: required, name: "age", offset: 16, kind: reflect.Int},
			{vtype: max, name: "age", offset: 16, kind: reflect.Int, param: "150"},
		},
	}
	req := struct {
		Name string
		Age  int
	}{"", 200}
	err := validateRequest(&req, plan)
	assert.NotNil(t, err)
	assert.True(t, len(err.(ProblemDetail).Invalid) >= 2)
}

func TestBuildDecoderAndValidationPlan(t *testing.T) {
	type TestReq struct {
		Name string `json:"name" validate:"required,min=2,max=100"`
		Age  int    `json:"age" validate:"min=0,max=150"`
	}

	dPlan, vPlan := buildDecoderAndValidationPlan[TestReq]()
	assert.NotNil(t, dPlan)
	assert.NotNil(t, vPlan)
	assert.Equal(t, len(vPlan.rules), 5)
	expectedNames := []string{"name", "name", "name", "age", "age"}
	for i, rule := range vPlan.rules {
		assert.Equal(t, rule.name, expectedNames[i])
	}
	assert.Equal(t, vPlan.rules[0].vtype, required)
}

func TestValidateNestedStruct(t *testing.T) {
	type Address struct {
		City string `validate:"required"`
	}
	type Request struct {
		Addr Address
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("nested struct field passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Addr: Address{City: "NYC"}}, vPlan))
	})

	t.Run("nested struct field fails", func(t *testing.T) {
		err := validateRequest(&Request{Addr: Address{City: ""}}, vPlan)
		assert.NotNil(t, err)
	})
}

func TestValidateSliceElements(t *testing.T) {
	type Item struct {
		Name string `validate:"required"`
	}
	type Request struct {
		Items []Item
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("valid slice elements pass", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Items: []Item{{Name: "a"}, {Name: "b"}}}, vPlan))
	})

	t.Run("slice element with missing required field fails", func(t *testing.T) {
		err := validateRequest(&Request{Items: []Item{{Name: ""}}}, vPlan)
		assert.NotNil(t, err)
	})

	t.Run("mixed valid and invalid elements reports errors", func(t *testing.T) {
		req := Request{Items: []Item{{Name: "valid"}, {Name: ""}, {Name: "also valid"}}}
		err := validateRequest(&req, vPlan)
		assert.NotNil(t, err)
	})

	t.Run("empty slice has no element violations", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Items: []Item{}}, vPlan))
	})

	t.Run("nil slice has no element violations", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{}, vPlan))
	})
}

func TestValidateSliceWithMinAndElements(t *testing.T) {
	type Item struct {
		Name string `validate:"required"`
	}
	type Request struct {
		Items []Item `validate:"min=1"`
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("empty slice fails min", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&Request{}, vPlan))
	})

	t.Run("element with missing name fails element check", func(t *testing.T) {
		assert.NotNil(t, validateRequest(&Request{Items: []Item{{Name: ""}}}, vPlan))
	})

	t.Run("valid slice passes both checks", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Items: []Item{{Name: "a"}}}, vPlan))
	})
}

func TestValidateNestedSliceElements(t *testing.T) {
	type Tag struct {
		Value string `validate:"required"`
	}
	type Item struct {
		Tags []Tag `validate:"min=1"`
	}
	type Request struct {
		Items []Item `validate:"min=1"`
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("valid nested slice passes", func(t *testing.T) {
		req := Request{Items: []Item{{Tags: []Tag{{Value: "a"}}}}}
		assert.NoError(t, validateRequest(&req, vPlan))
	})

	t.Run("inner element fails", func(t *testing.T) {
		req := Request{Items: []Item{{Tags: []Tag{{Value: ""}}}}}
		err := validateRequest(&req, vPlan)
		assert.NotNil(t, err)
	})

	t.Run("empty inner slice fails min", func(t *testing.T) {
		req := Request{Items: []Item{{Tags: []Tag{}}}}
		err := validateRequest(&req, vPlan)
		assert.NotNil(t, err)
	})
}

func TestValidatePointerToStruct(t *testing.T) {
	type Config struct {
		Version string `validate:"required"`
	}
	type Request struct {
		Cfg *Config
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("nil pointer skips validation", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{}, vPlan))
	})

	t.Run("non-nil pointer with valid data passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Cfg: &Config{Version: "v1"}}, vPlan))
	})

	t.Run("non-nil pointer with invalid data fails", func(t *testing.T) {
		err := validateRequest(&Request{Cfg: &Config{Version: ""}}, vPlan)
		assert.NotNil(t, err)
	})
}

func TestValidatePointerToSlice(t *testing.T) {
	type Item struct {
		Name string `validate:"required"`
	}
	type Request struct {
		Items *[]Item
	}

	_, vPlan := buildDecoderAndValidationPlan[Request]()

	t.Run("nil pointer skips validation", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{}, vPlan))
	})

	t.Run("non-nil pointer with valid elements passes", func(t *testing.T) {
		assert.NoError(t, validateRequest(&Request{Items: &[]Item{{Name: "a"}}}, vPlan))
	})

	t.Run("non-nil pointer with invalid element fails", func(t *testing.T) {
		err := validateRequest(&Request{Items: &[]Item{{Name: ""}}}, vPlan)
		assert.NotNil(t, err)
	})
}

func TestValidateMinZeroOnNumeric(t *testing.T) {
	t.Run("min=0 on int passes any value including zero", func(t *testing.T) {
		assert.NoError(t, validateRequest(&struct{ Age int }{0}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "age", offset: 0, kind: reflect.Int, param: "0"},
			},
		}))
		assert.NoError(t, validateRequest(&struct{ Age int }{1}, &validationPlan{
			rules: []validationRule{
				{vtype: min, name: "age", offset: 0, kind: reflect.Int, param: "0"},
			},
		}))
	})
}
