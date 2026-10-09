package contracts

type WasmABI struct {
	Version        int      `json:"version"`
	InputFormat    string   `json:"inputFormat"`
	OutputFormat   string   `json:"outputFormat"`
	ResultEncoding string   `json:"resultEncoding"`
	HostModules    []string `json:"hostModules"`
	Exports        struct {
		Memory   string `json:"memory"`
		Allocate string `json:"allocate"`
		Invoke   string `json:"invoke"`
	} `json:"exports"`
	Signatures struct {
		Allocate WasmFunctionSignature `json:"allocate"`
		Invoke   WasmFunctionSignature `json:"invoke"`
	} `json:"signatures"`
	Limits struct {
		MaxInputBytes     int    `json:"maxInputBytes"`
		MaxOutputBytes    int    `json:"maxOutputBytes"`
		MaxModuleBytes    int    `json:"maxModuleBytes"`
		MaxMemoryPages    uint32 `json:"maxMemoryPages"`
		MaxDurationMillis int    `json:"maxDurationMillis"`
	} `json:"limits"`
}

type WasmFunctionSignature struct {
	Parameters []string `json:"parameters"`
	Results    []string `json:"results"`
}

type PluginMetadata struct {
	Manifest            []byte
	ConfigurationSchema []byte
	AdminSurface        []byte
}

// WasmABIDefinition returns a fresh, typed sandbox contract.
func WasmABIDefinition() WasmABI {
	result := WasmABI{Version: 1, InputFormat: "json", OutputFormat: "json", ResultEncoding: "i64:high32=length,low32=pointer", HostModules: []string{}}
	result.Exports.Memory = "memory"
	result.Exports.Allocate = "alloc"
	result.Exports.Invoke = "invoke"
	result.Signatures.Allocate = WasmFunctionSignature{Parameters: []string{"i32"}, Results: []string{"i32"}}
	result.Signatures.Invoke = WasmFunctionSignature{Parameters: []string{"i32", "i32"}, Results: []string{"i64"}}
	result.Limits.MaxInputBytes = 1048576
	result.Limits.MaxOutputBytes = 1048576
	result.Limits.MaxModuleBytes = 16777216
	result.Limits.MaxMemoryPages = 1024
	result.Limits.MaxDurationMillis = 5000
	return result
}
