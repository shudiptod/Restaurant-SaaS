package handlers

import "testing"

func TestCalculateDiscount(t *testing.T) {
	tests := []struct {
		name  string
		base  int
		mode  string
		value int
		want  int
	}{
		{name: "fixed amount", base: 1250, mode: "amount", value: 300, want: 300},
		{name: "fixed amount capped", base: 1250, mode: "amount", value: 2000, want: 1250},
		{name: "percentage basis points", base: 1250, mode: "percent", value: 1500, want: 188},
		{name: "full percentage", base: 1250, mode: "percent", value: 10000, want: 1250},
		{name: "zero base", base: 0, mode: "amount", value: 300, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := calculateDiscount(test.base, test.mode, test.value); got != test.want {
				t.Fatalf("calculateDiscount() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseDiscount(t *testing.T) {
	mode, amount, err := parseDiscount("amount", "1.25")
	if err != nil || mode != "amount" || amount != 125 {
		t.Fatalf("amount parse = (%q, %d, %v), want (amount, 125, nil)", mode, amount, err)
	}
	mode, basisPoints, err := parseDiscount("percent", "12.5")
	if err != nil || mode != "percent" || basisPoints != 1250 {
		t.Fatalf("percentage parse = (%q, %d, %v), want (percent, 1250, nil)", mode, basisPoints, err)
	}
	if _, _, err := parseDiscount("percent", "100.01"); err == nil {
		t.Fatal("expected percentage above 100 to be rejected")
	}
}
