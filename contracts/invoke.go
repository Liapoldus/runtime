package contracts

// InvokeErrors is the runtime.invoke wire error catalog and public generation source.
type InvokeErrors struct {
	Version    int                      `json:"version"`
	Capability string                   `json:"capability"`
	Semantics  string                   `json:"semantics"`
	Errors     map[string]InvokeProblem `json:"errors"`
}
type InvokeProblem struct {
	HTTP      int  `json:"http"`
	Retryable bool `json:"retryable"`
}

const invokeSemantics string = "Один транспортный вызов выполняется ровно один раз. Результат, не полученный из-за транспортной ошибки или отмены, считается неизвестным и никогда не воспроизводится автоматически (replay отсутствует). Тело ответа несёт только код ошибки и никогда не раскрывает содержимое исполнения (трапов, payloads, инвариантов)."

// InvokeErrorDefinitions returns an independent map for each caller.
func InvokeErrorDefinitions() InvokeErrors {
	return InvokeErrors{Version: 1, Capability: InvokeCapability, Semantics: invokeSemantics, Errors: map[string]InvokeProblem{
		CodeNotReady:         {HTTP: 503, Retryable: true},
		CodeUnknownCommand:   {HTTP: 404, Retryable: false},
		CodeForbiddenScope:   {HTTP: 403, Retryable: false},
		CodeForbiddenEntity:  {HTTP: 403, Retryable: false},
		CodeInvalidRequest:   {HTTP: 422, Retryable: false},
		CodePayloadTooLarge:  {HTTP: 413, Retryable: false},
		CodeExecutionFailed:  {HTTP: 502, Retryable: false},
		CodeExecutionTimeout: {HTTP: 504, Retryable: false},
		CodeCancelled:        {HTTP: 499, Retryable: false},
	}}
}

// InvokeErrorCode preserves unknown-code fallback and replay hints.
func InvokeErrorCode(code string) (http int, retryable bool) {
	problem, ok := InvokeErrorDefinitions().Errors[code]
	if !ok {
		return 502, false
	}
	return problem.HTTP, problem.Retryable
}
