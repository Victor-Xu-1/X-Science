package toolcontract

import "testing"

func TestToolArgumentsObjectKeepsRawStructure(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"object", `{"a":1,"nested":[{"b":true}]}`, true},
		{"duplicate root", `{"a":1,"a":2}`, false},
		{"duplicate nested", `{"nested":{"a":1,"a":2}}`, false},
		{"duplicate escaped name", `{"a":1,"\u0061":2}`, false},
		{"trailing value", `{} {}`, false},
		{"truncated", `{"a":`, false},
		{"array", `[]`, false},
		{"null", `null`, false},
		{"invalid UTF8", "{\"a\":\"\xff\"}", false},
		{"numeric token", `{"n":123456789012345678901234567890}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if valid := ValidateArgumentsObject([]byte(test.raw)) == nil; valid != test.valid {
				t.Fatalf("valid=%t want=%t", valid, test.valid)
			}
		})
	}
}
