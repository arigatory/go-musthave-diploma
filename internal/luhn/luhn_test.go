package luhn

import "testing"

func TestValid(t *testing.T) {
	tests := []struct {
		number string
		want   bool
	}{
		{"12345678903", true},
		{"9278923470", true},
		{"2377225624", true},
		{"79927398713", true},
		{"0", true},
		{"12345678901", false},
		{"", false},
		{"12a45", false},
		{" 12345678903", false},
		{"-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.number, func(t *testing.T) {
			if got := Valid(tt.number); got != tt.want {
				t.Errorf("Valid(%q) = %v, want %v", tt.number, got, tt.want)
			}
		})
	}
}
