package pow

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestSolveProducesBase64JSON(t *testing.T) {
	// Use a real challenge with known difficulty from DeepSeek's format.
	// difficulty=144000 is the real production value.
	c := Challenge{
		Algorithm:  "DeepSeekHashV1",
		Challenge:  "cc5f4b1580e64350147977b073834ae8c28e695cb5a6d563f7da2deb0a7fc067",
		Salt:       "3eeb544efdf116c66fa2",
		Difficulty: 144000,
		ExpireAt:   1778173072575,
		Signature:  "52537ee1b0eaf451fdec223636267a2b7c5299f81976adf9e553e08d05a74509",
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

	// Decode and verify answer
	b, err := base64.StdEncoding.DecodeString(result)
	if err != nil {
		t.Fatalf("result is not valid base64: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	answer, ok := decoded["answer"]
	if !ok {
		t.Fatal("result JSON missing 'answer' field")
	}
	t.Logf("answer: %v", answer)

	// The known correct answer for this challenge is 105336
	answerFloat, ok := answer.(float64)
	if !ok {
		t.Fatalf("answer is not a number: %T %v", answer, answer)
	}
	if int(answerFloat) != 105336 {
		t.Errorf("answer = %d, want 105336", int(answerFloat))
	}
}
