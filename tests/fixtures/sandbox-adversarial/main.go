package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"

	"github.com/Liapoldus/runtime/internal/infrastructure/wasm"
)

type stepRequest struct {
	Module   string          `json:"module"`
	Validate bool            `json:"validate"`
	Execute  bool            `json:"execute"`
	Input    json.RawMessage `json:"input"`
}

type gateResult struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Panic  string          `json:"panic,omitempty"`
}

type stepResult struct {
	Name     string      `json:"name"`
	Validate *gateResult `json:"validate,omitempty"`
	Execute  *gateResult `json:"execute,omitempty"`
}

var registry = map[string]string{
	"import-env":    "0061736d0100000001100360017f017f60027f7f017e60017f00020c0103656e76046e6f7065000203030200010503010001071b03066d656d6f7279020005616c6c6f63000106696e766f6b6500020a1302040041000b0c002001ad4220862000ad840b",
	"import-wasi":   "0061736d0100000001140360017f017f60027f7f017e60047f7f7f7f017f02230116776173695f736e617073686f745f70726576696577310866645f7772697465000203030200010503010001071b03066d656d6f7279020005616c6c6f63000106696e766f6b6500020a1302040041000b0c002001ad4220862000ad840b",
	"import-global": "0061736d01000000010c0260017f017f60027f7f017e020b0104686f73740167037f0003030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b",
	"bogus-length":  "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1202040041000b0b004284808080808080100b",
	"bogus-pointer": "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1002040041000b09004280e0ffffcf000b",
	"non-json":      "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a0f02040041000b08004280e08380200b0b0a01004180e0030b02fffe",
	"invoke-loop":   "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a0f02040041000b080003400c000b000b",
	"alloc-range":   "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041700b0c002001ad4220862000ad840b",
	"recursion":     "0061736d0100000001100360017f017f60027f7f017e6000017f0304030001020503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1303040041000b070010021a42000b040010020b",
	"echo":          "0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b",
	"echo-16":       "0061736d01000000010c0260017f017f60027f7f017e03030200010503010010071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b",
}

func decodeModule(name string) ([]byte, error) {
	hexText, ok := registry[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return hex.DecodeString(hexText)
}

func runGate(fn func() error, output []byte) *gateResult {
	result := &gateResult{}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Panic = base64.StdEncoding.EncodeToString([]byte(errText(recovered)))
		}
	}()
	if err := fn(); err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	if len(output) > 0 {
		result.Output = json.RawMessage(append([]byte(nil), output...))
	}
	return result
}

func errText(recovered any) string {
	if err, ok := recovered.(error); ok {
		return err.Error()
	}
	return "panic"
}

func main() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	var spec struct {
		Steps []stepRequest `json:"steps"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		os.Exit(2)
	}
	ctx := context.Background()
	results := make([]stepResult, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		moduleBytes, err := decodeModule(step.Module)
		if err != nil {
			results = append(results, stepResult{Name: step.Module, Validate: &gateResult{Error: "unknown_module"}})
			continue
		}
		result := stepResult{Name: step.Module}
		if step.Validate {
			result.Validate = runGate(func() error { return wasm.ValidateModule(ctx, moduleBytes) }, nil)
		}
		if step.Execute {
			if len(step.Input) == 0 {
				step.Input = json.RawMessage("null")
			}
			inputBytes := append([]byte(nil), step.Input...)
			rawOutput, err := (wasm.Executor{}).Execute(ctx, moduleBytes, inputBytes)
			result.Execute = runGate(func() error { return err }, rawOutput)
		}
		results = append(results, result)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"results": results}); err != nil {
		os.Exit(1)
	}
}
