package pow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Challenge is the JSON structure returned by DeepSeek's create_pow_challenge endpoint.
type Challenge struct {
	Algorithm  string  `json:"algorithm"`
	Challenge  string  `json:"challenge"`
	Salt       string  `json:"salt"`
	Difficulty float64 `json:"difficulty"`
	ExpireAt   int64   `json:"expire_at"`
	Signature  string  `json:"signature"`
	TargetPath string  `json:"target_path"`
}

// wasmPath returns the path to the WASM binary.
// It looks relative to this source file, then relative to the binary.
func wasmPath() string {
	// Try relative to source file (development)
	_, srcFile, _, ok := runtime.Caller(0)
	if ok {
		candidate := filepath.Join(filepath.Dir(srcFile), "..", "..", "dsk", "wasm", "sha3_wasm_bg.7b9ca65ddd.wasm")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Try relative to working directory
	candidates := []string{
		"dsk/wasm/sha3_wasm_bg.7b9ca65ddd.wasm",
		filepath.Join(filepath.Dir(os.Args[0]), "dsk", "wasm", "sha3_wasm_bg.7b9ca65ddd.wasm"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "dsk/wasm/sha3_wasm_bg.7b9ca65ddd.wasm"
}

// Solve finds the PoW answer using the WASM solver and returns base64-encoded JSON.
func Solve(c Challenge) (string, error) {
	answer, err := solveWithWasm(c)
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

func solveWithWasm(c Challenge) (int, error) {
	ctx := context.Background()

	wasmBytes, err := os.ReadFile(wasmPath())
	if err != nil {
		return 0, fmt.Errorf("pow: could not read WASM binary: %w", err)
	}

	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)

	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	mod, err := rt.Instantiate(ctx, wasmBytes)
	if err != nil {
		return 0, fmt.Errorf("pow: could not instantiate WASM: %w", err)
	}

	mem := mod.Memory()

	// Allocate memory using __wbindgen_export_0(size, align) -> ptr
	alloc := mod.ExportedFunction("__wbindgen_export_0")
	addToStack := mod.ExportedFunction("__wbindgen_add_to_stack_pointer")
	wasmSolve := mod.ExportedFunction("wasm_solve")

	if alloc == nil || addToStack == nil || wasmSolve == nil {
		return 0, fmt.Errorf("pow: required WASM exports not found")
	}

	writeStr := func(s string) (uint32, uint32, error) {
		encoded := []byte(s)
		sz := uint64(len(encoded))
		res, err := alloc.Call(ctx, sz, 1)
		if err != nil {
			return 0, 0, err
		}
		ptr := uint32(res[0])
		if !mem.Write(ptr, encoded) {
			return 0, 0, fmt.Errorf("pow: memory write failed")
		}
		return ptr, uint32(sz), nil
	}

	// Allocate return pointer on the stack (-16 bytes)
	retRes, err := addToStack.Call(ctx, uint64(^uint64(16)+1)) // -16 as uint64
	if err != nil {
		return 0, fmt.Errorf("pow: stack pointer call failed: %w", err)
	}
	retPtr := uint32(retRes[0])
	defer addToStack.Call(ctx, 16)

	prefix := c.Salt + "_" + fmt.Sprintf("%d", c.ExpireAt) + "_"

	challengePtr, challengeLen, err := writeStr(c.Challenge)
	if err != nil {
		return 0, fmt.Errorf("pow: write challenge: %w", err)
	}
	prefixPtr, prefixLen, err := writeStr(prefix)
	if err != nil {
		return 0, fmt.Errorf("pow: write prefix: %w", err)
	}

	// wasm_solve(retptr, challenge_ptr, challenge_len, prefix_ptr, prefix_len, difficulty_f64)
	diffBits := math.Float64bits(c.Difficulty)
	_, err = wasmSolve.Call(ctx,
		uint64(retPtr),
		uint64(challengePtr), uint64(challengeLen),
		uint64(prefixPtr), uint64(prefixLen),
		diffBits,
	)
	if err != nil {
		return 0, fmt.Errorf("pow: wasm_solve failed: %w", err)
	}

	// Read status (i32 at retptr)
	statusBytes, ok := mem.Read(retPtr, 4)
	if !ok {
		return 0, fmt.Errorf("pow: could not read status")
	}
	status := int32(binary.LittleEndian.Uint32(statusBytes))
	if status == 0 {
		return 0, fmt.Errorf("pow: WASM found no solution")
	}

	// Read answer (f64 at retptr+8)
	valBytes, ok := mem.Read(retPtr+8, 8)
	if !ok {
		return 0, fmt.Errorf("pow: could not read answer")
	}
	valBits := binary.LittleEndian.Uint64(valBytes)
	answer := int(math.Float64frombits(valBits))
	return answer, nil
}

// wasm_solve takes difficulty as a float64 per the WASM interface.
// The api.ValueTypeF64 needs to be passed as a uint64 bit pattern.
var _ api.Function // ensure api import is used
