package pow

import (
	"strings"
	"testing"
)

func TestLeadingZeroBits(t *testing.T) {
	tests := []struct {
		b    []byte
		want int
	}{
		{[]byte{0x00}, 8},
		{[]byte{0x80}, 0},
		{[]byte{0x40}, 1},
		{[]byte{0x00, 0x80}, 8},
	}
	for _, tc := range tests {
		got := leadingZeroBits(tc.b)
		if got != tc.want {
			t.Errorf("leadingZeroBits(%x) = %d, want %d", tc.b, got, tc.want)
		}
	}
}

func TestSolveProducesBase64JSON(t *testing.T) {
	c := Challenge{
		Algorithm:  "DeepSeekHashV1",
		Challenge:  "testchallenge",
		Salt:       "testsalt",
		Difficulty: 1,
		ExpireAt:   9999999999,
		Signature:  "sig",
		TargetPath: "/api/v0/chat/completion",
	}
	result, err := Solve(c)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if result == "" {
		t.Fatal("Solve returned empty string")
	}
	if strings.ContainsAny(result, " \n\t") {
		t.Errorf("result contains whitespace: %q", result)
	}
}
