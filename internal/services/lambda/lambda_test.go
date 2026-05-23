package lambda

import "testing"

func TestResolveFunctionName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"my-func", "my-func"},
		{"my-func:prod", "my-func"},
		{"arn:aws:lambda:us-east-1:123456789012:function:my-func", "my-func"},
		{"arn:aws:lambda:us-east-1:123456789012:function:my-func:prod", "my-func"},
	}
	for _, c := range cases {
		got := resolveFunctionName(c.input)
		if got != c.want {
			t.Errorf("resolveFunctionName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
