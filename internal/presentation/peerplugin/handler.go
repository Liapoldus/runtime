// Package peerplugin exposes Runtime invocation over authenticated peer transport.
package peerplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
	"github.com/Liapoldus/runtime/contracts"
	runtimemodule "github.com/Liapoldus/runtime/internal/application/module"
)

// errInvalidHandler reports an unusable handler assembly. It is deliberately
// opaque: the peer never exposes anything but the versioned invoke error codes.
const (
	messageInvalidHandler string = "peerplugin: invalid handler"
)

var errInvalidHandler = errors.New(messageInvalidHandler)

// Handler serves the runtime.invoke peer method. Only the peer transport enters
// here; every product decision is taken by the application Invoker, and the
// returned payload always matches the response action expected by the Server v1
// caller, with a JSON body that carries either the success document or a bare
// error code from the versioned invoke error contract.
type Handler struct {
	peer.Handler
	invoker *runtimemodule.Invoker
}

func New(invoker *runtimemodule.Invoker, authorizer peer.Authorizer) (*Handler, error) {
	if invoker == nil {
		return nil, errInvalidHandler
	}
	handler := &Handler{invoker: invoker}
	registered, err := peer.NewRegistry().WithAuthorizer(authorizer).
		RegisterCall(contracts.InvokeCapability, handler.invoke).
		Build()
	if err != nil {
		return nil, errInvalidHandler
	}
	handler.Handler = registered
	return handler, nil
}

func (handler *Handler) invoke(ctx context.Context, call peer.Call) (peer.Result, error) {
	if string(call.Method) != contracts.InvokeCapability {
		return peer.Result{}, peer.ErrMethodNotFound
	}
	request, code := runtimemodule.DecodeInvocation(requestPayload(call.Payload))
	if code != "" {
		return invokeResponse(codeStatus(code), map[string]any{"code": code}), nil
	}
	output, err := handler.invoker.Invoke(ctx, request)
	if err != nil {
		code := invokeErrorCode(err)
		return invokeResponse(codeStatus(code), map[string]any{"code": code}), nil
	}
	return invokeResponse(200, map[string]any{
		"command": request.Command,
		"output":  json.RawMessage(output),
	}), nil
}

// requestPayload extracts the product body from a Server v1 httpRequestPayload
// envelope whenever the payload carries one, and otherwise treats the whole
// payload as the request document (a direct pluginprotocol caller). Transport
// context such as method, path, headers or remote address is never interpreted
// by product logic.
func requestPayload(payload []byte) []byte {
	var envelope struct {
		Method     string            `json:"method"`
		Path       string            `json:"path"`
		Query      string            `json:"query"`
		Headers    map[string]string `json:"headers"`
		Cookies    json.RawMessage   `json:"cookies"`
		Body       []byte            `json:"body"`
		RequestID  string            `json:"requestId"`
		RemoteAddr string            `json:"remoteAddr"`
	}
	if decodeObject(payload, &envelope) == nil && envelope.Body != nil {
		return envelope.Body
	}
	return payload
}

func invokeErrorCode(err error) string {
	switch {
	case errors.Is(err, runtimemodule.ErrInvokeNotReady):
		return contracts.CodeNotReady
	case errors.Is(err, runtimemodule.ErrInvokeUnknownCommand):
		return contracts.CodeUnknownCommand
	case errors.Is(err, runtimemodule.ErrInvokeForbiddenScope):
		return contracts.CodeForbiddenScope
	case errors.Is(err, runtimemodule.ErrInvokeForbiddenEntity):
		return contracts.CodeForbiddenEntity
	case errors.Is(err, runtimemodule.ErrInvokePayloadTooLarge):
		return contracts.CodePayloadTooLarge
	case errors.Is(err, runtimemodule.ErrInvokeExecutionTimeout):
		return contracts.CodeExecutionTimeout
	case errors.Is(err, runtimemodule.ErrInvokeCancelled):
		return contracts.CodeCancelled
	default:
		return contracts.CodeExecutionFailed
	}
}

func codeStatus(code string) int {
	status, _ := contracts.InvokeErrorCode(code)
	return status
}

func invokeResponse(status int, value any) peer.Result {
	body, err := json.Marshal(value)
	if err != nil {
		return peer.Result{Payload: []byte(`{"status":502,"headers":{"Content-Type":"application/json"},"body":"{\"code\":\"execution_failed\"}"}`)}
	}
	payload, err := json.Marshal(map[string]any{
		"status":  status,
		"headers": map[string]string{"Content-Type": "application/json"},
		"body":    string(body),
	})
	if err != nil {
		return peer.Result{Payload: []byte(`{"status":502,"headers":{"Content-Type":"application/json"},"body":"{\"code\":\"execution_failed\"}"}`)}
	}
	return peer.Result{Payload: payload}
}

func decodeObject(payload []byte, target any) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return peer.ErrInvalidRequest
	}
	if err := rejectDuplicateKeys(trimmed); err != nil {
		return peer.ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return peer.ErrInvalidRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return peer.ErrInvalidRequest
	}
	return nil
}

func rejectDuplicateKeys(document []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(document))
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if err := scanJSONValue(decoder, first, true); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return peer.ErrInvalidRequest
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, token json.Token, root bool) error {
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		rootKeys := make(map[string]string)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return peer.ErrInvalidRequest
			}
			if _, exists := keys[key]; exists {
				return peer.ErrInvalidRequest
			}
			keys[key] = struct{}{}
			if root {
				for existing := range rootKeys {
					if strings.EqualFold(existing, key) {
						return peer.ErrInvalidRequest
					}
				}
				rootKeys[key] = key
			}
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(decoder, value, false); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return peer.ErrInvalidRequest
		}
	case '[':
		for decoder.More() {
			value, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := scanJSONValue(decoder, value, false); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return peer.ErrInvalidRequest
		}
	default:
		return peer.ErrInvalidRequest
	}
	return nil
}

var _ peer.Handler = (*Handler)(nil)
