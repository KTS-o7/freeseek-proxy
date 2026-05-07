package pow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"golang.org/x/crypto/sha3"
)

// Challenge is the JSON structure returned by DeepSeek's create_pow_challenge endpoint.
type Challenge struct {
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Salt       string `json:"salt"`
	Difficulty int    `json:"difficulty"`
	ExpireAt   int64  `json:"expire_at"`
	Signature  string `json:"signature"`
	TargetPath string `json:"target_path"`
}

// Solve finds n such that SHA3-256(challenge + salt + "_" + expireAt + "_" + n)
// has `difficulty` leading zero bits, then returns base64-encoded JSON response.
func Solve(c Challenge) (string, error) {
	prefix := c.Salt + "_" + strconv.FormatInt(c.ExpireAt, 10) + "_"
	base := c.Challenge + prefix

	answer, err := findAnswer(base, c.Difficulty)
	if err != nil {
		return "", err
	}

	result := map[string]interface{}{
		"algorithm":   c.Algorithm,
		"challenge":   c.Challenge,
		"salt":        c.Salt,
		"answer":      answer,
		"signature":   c.Signature,
		"target_path": c.TargetPath,
	}
	b, _ := json.Marshal(result)
	return base64.StdEncoding.EncodeToString(b), nil
}

func findAnswer(base string, difficulty int) (int, error) {
	const maxIter = 10_000_000
	for n := 0; n < maxIter; n++ {
		candidate := base + strconv.Itoa(n)
		h := sha3.Sum256([]byte(candidate))
		if leadingZeroBits(h[:]) >= difficulty {
			return n, nil
		}
	}
	return 0, fmt.Errorf("pow: no solution found within %d iterations", maxIter)
}

// leadingZeroBits counts the number of leading zero bits in b.
func leadingZeroBits(b []byte) int {
	count := 0
	for _, byt := range b {
		if byt == 0 {
			count += 8
			continue
		}
		for mask := byte(0x80); mask > 0; mask >>= 1 {
			if byt&mask != 0 {
				return count
			}
			count++
		}
		break
	}
	return count
}
