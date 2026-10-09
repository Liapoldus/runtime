package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	"github.com/Liapoldus/plugin-sdk/infrastructure"
	protocolpeer "github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

var errScenario = errors.New("runtime invoke integration scenario failed")

const (
	instanceID     = "runtime-invoke-e2e"
	replicaID      = "runtime-invoke-e2e-1"
	coreCN         = "runtime-invoke-core"
	runtimeCN      = "runtime-invoke-plugin"
	callerCN       = "runtime-invoke-caller"
	outsiderCN     = "runtime-invoke-outsider"
	runtimePeerURI = "spiffe://liapoldus.test/runtime"
	callerPeerURI  = "spiffe://liapoldus.test/server"
	outsiderURI    = "spiffe://liapoldus.test/outsider"
	schemaV1       = "1"
)

type storedGeneration struct {
	bytes  []byte
	digest string
}

type scenario struct {
	contract    infrastructure.HTTPContract
	generations map[string]storedGeneration
}

// echoWasm returns the pointer it was handed so the host reads back the exact
// composed input document as output. It carries no host functions.
const echoWasm = "0061736d01000000" +
	"010c0260017f017f60027f7f017e" +
	"0303020001" +
	"0503010001" +
	"071b03066d656d6f7279020005616c6c6f63000006696e766f6b650001" +
	"0a1302040041000b0c002001ad4220862000ad840b"

// startLoopWasm spins forever in its start function; it is the module used to
// observe cancellation and the bounded 5000ms execution deadline.
const startLoopWasm = "0061736d01000000" +
	"010f0360017f017f60027f7f017e600000" +
	"030403000102" +
	"0503010001" +
	"071b03066d656d6f7279020005616c6c6f63000006696e766f6b650001" +
	"080102" +
	"0a1b03040041000b0c002001ad4220862000ad840b070003400c000b0b"

type outcome struct {
	Status     int             `json:"status,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	Code       string          `json:"code,omitempty"`
	Transport  string          `json:"transport,omitempty"`
	Err        string          `json:"err,omitempty"`
	Command    string          `json:"command,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	Categories string          `json:"categories,omitempty"`
}

type result struct {
	NotReady          outcome `json:"notReady"`
	Success           outcome `json:"success"`
	SuccessWithEntity outcome `json:"successWithEntity"`
	UnknownCommand    outcome `json:"unknownCommand"`
	ForbiddenScope    outcome `json:"forbiddenScope"`
	ForbiddenEntity   outcome `json:"forbiddenEntity"`
	InvalidRequest    outcome `json:"invalidRequest"`
	PayloadTooLarge   outcome `json:"payloadTooLarge"`
	UnknownMethod     outcome `json:"unknownMethod"`
	Unauthorized      outcome `json:"unauthorized"`
	Cancelled         outcome `json:"cancelled"`
	Timeout           outcome `json:"timeout"`
	Envelope          outcome `json:"envelope"`
	ShutdownDrain     struct {
		AcceptedCall           outcome `json:"acceptedCall"`
		RefusedAfterDrain      outcome `json:"refusedAfterDrain"`
		ProcessExitedAfterCall bool    `json:"processExitedAfterCall"`
	} `json:"shutdownDrain"`
}

type harness struct {
	client      protocolpeer.Client
	peerAddress string
}

func main() {
	if err := run(); err != nil {
		if _, err := fmt.Fprintln(os.Stderr, "Runtime invoke integration failed"); err != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run() error {
	contract, err := infrastructure.LoadHTTPContract()
	if err != nil {
		return errScenario
	}
	work, err := os.MkdirTemp("", "runtime-invoke-e2e-")
	if err != nil {
		return errScenario
	}
	defer func() { checkCleanup(os.RemoveAll(work)) }()
	credentials, err := issueTestCredentials(work)
	if err != nil {
		return errScenario
	}
	artifactStore := artifacts.Store{Root: filepath.Join(work, "artifacts")}
	echo, err := hex.DecodeString(echoWasm)
	if err != nil {
		return errScenario
	}
	slow, err := hex.DecodeString(startLoopWasm)
	if err != nil {
		return errScenario
	}
	echoDigest, slowDigest := sdkmodels.Digest(echo), sdkmodels.Digest(slow)
	metadata, err := contracts.PluginDocuments()
	if err != nil {
		return errScenario
	}
	adminActions := contracts.ModuleAdminActionDefinitions()
	core := &scenario{contract: contract, generations: map[string]storedGeneration{
		"runtime-invoke-slow": publish(configurationDocument("runtimeinvokegroup", slowDigest, "spin")),
		"runtime-invoke-echo": publish(configurationDocument("runtimeinvokegroup", echoDigest, "echo")),
	}}
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return errScenario
	}
	defer func() { checkCleanup(listener.Close()) }()
	ca, err := os.ReadFile(credentials.caFile)
	if err != nil {
		return errScenario
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errScenario
	}
	coreCertificate, err := tls.LoadX509KeyPair(credentials.coreServerCert, credentials.coreServerKey)
	if err != nil {
		return errScenario
	}
	coreServer := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           core.handler(),
		TLSConfig: &tls.Config{
			MinVersion:   contract.TransportSecurity.MinimumTLSVersion,
			Certificates: []tls.Certificate{coreCertificate},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    roots,
		},
	}
	go func() {
		if err := coreServer.ServeTLS(listener, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			os.Exit(1)
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		checkCleanup(coreServer.Shutdown(ctx))
	}()

	runtimeBinary := filepath.Join(work, "runtime")
	//nolint:gosec // G204: runs a fixed Go command or the fixture-built Runtime binary, without a shell.
	build := exec.CommandContext(context.Background(), "go", "build", "-o", runtimeBinary, "./cmd/runtime")
	build.Dir = repositoryRoot()
	build.Env = append(os.Environ(), "GOWORK=off")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		if _, err := fmt.Fprintf(os.Stderr, "runtime build failed: %v\n", err); err != nil {
			os.Exit(1)
		}
		return errScenario
	}
	//nolint:gosec // G204: runs a fixed Go command or the fixture-built Runtime binary, without a shell.
	command := exec.CommandContext(context.Background(), runtimeBinary,
		"--listen", "127.0.0.1:0",
		"--core-url", "https://"+listener.Addr().String(),
		"--core-server-name", "localhost",
		"--core-peer-cn", coreCN,
		"--instance-id", instanceID,
		"--replica-id", replicaID,
		"--tls-ca-file", credentials.caFile,
		"--tls-server-cert-file", credentials.runtimeServerCert,
		"--tls-server-key-file", credentials.runtimeServerKey,
		"--tls-client-cert-file", credentials.runtimeClientCert,
		"--tls-client-key-file", credentials.runtimeClientKey,
		"--tls-crl-file", credentials.crlFile,
		"--artifact-dir", artifactStore.Root,
		"--peer-listen", "127.0.0.1:0",
		"--peer-identity", runtimePeerURI,
		"--peer-allowed-caller", callerPeerURI,
		"--peer-ca-file", credentials.caFile,
		"--peer-cert", credentials.runtimePeerCert,
		"--peer-key", credentials.runtimePeerKey,
	)
	command.Dir = repositoryRoot()
	command.Env = append(os.Environ(), "GOWORK=off")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return errScenario
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return errScenario
	}
	processWait := make(chan error, 1)
	go func() { processWait <- command.Wait() }()
	processWaited := false
	defer func() {
		if command.Process != nil && !processWaited {
			checkCleanup(command.Process.Signal(os.Interrupt))
			select {
			case <-processWait:
			case <-time.After(5 * time.Second):
				checkCleanup(command.Process.Kill())
				<-processWait
			}
		}
	}()
	readyLine, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		return errScenario
	}
	var address struct {
		ListenAddress     string `json:"listenAddress"`
		PeerListenAddress string `json:"peerListenAddress"`
	}
	if json.Unmarshal(readyLine, &address) != nil || address.ListenAddress == "" || address.PeerListenAddress == "" {
		return errScenario
	}

	runParent, cancelParent := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelParent()
	callerHandler, err := protocolpeer.NewRegistry().Build()
	if err != nil {
		return errScenario
	}
	callerCertificate, err := tls.LoadX509KeyPair(credentials.callerPeerCert, credentials.callerPeerKey)
	if err != nil {
		return errScenario
	}
	session, err := protocolpeer.Dial(runParent, protocolpeer.ClientConfig{
		Network: protocolpeer.NetworkConfig{
			Carrier: protocolpeer.CarrierTCP, Endpoint: address.PeerListenAddress, ServerName: "localhost",
		},
		Security: protocolpeer.SecurityConfig{
			Identity: callerPeerURI, Certificate: callerCertificate, Roots: roots,
		},
		Handler: callerHandler,
		Limits:  protocolpeer.DefaultLimits(),
	})
	if err != nil {
		return errScenario
	}
	defer func() { checkCleanup(session.Close()) }()
	sut := &harness{client: session, peerAddress: address.PeerListenAddress}

	notReady := sut.call(runParent, request("echo", "tenant-a/site-1", nil, map[string]any{"message": "hello"}))
	late := result{NotReady: notReady}

	coreCredentials, err := infrastructure.LoadCredentials(contract, infrastructure.CredentialsMaterial{
		CAFile:                credentials.caFile,
		ServerCertificateFile: credentials.coreServerCert,
		ServerKeyFile:         credentials.coreServerKey,
		ClientCertificateFile: credentials.coreClientCert,
		ClientKeyFile:         credentials.coreClientKey,
	})
	if err != nil {
		return errScenario
	}
	provider, err := infrastructure.NewStaticCredentialsProvider(coreCredentials)
	if err != nil {
		return errScenario
	}
	revocation, err := infrastructure.NewRevocation(infrastructure.RevocationConfiguration{
		Authorities: coreCredentials.TrustAuthorities(),
		Files:       []string{credentials.crlFile},
	})
	if err != nil {
		return errScenario
	}
	peer, err := sdkmodels.NewPeerIdentity(runtimeCN, "")
	if err != nil {
		return errScenario
	}
	transport, err := infrastructure.NewMutualTLSClient(contract, provider, infrastructure.MutualTLSClientConfig{
		Peer:       peer,
		ServerName: "localhost",
		Revocation: revocation,
	})
	if err != nil {
		return errScenario
	}
	defer transport.CloseIdleConnections()
	client, err := infrastructure.NewPluginClient(contract, "https://"+address.ListenAddress, transport, peer)
	if err != nil {
		return errScenario
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	surfaceHash := sha256.Sum256(metadata.AdminSurface)
	surfaceDigest := "sha256:" + hex.EncodeToString(surfaceHash[:])
	upload := func(digest string, module []byte, requestID string) error {
		invocation := sdkmodels.ArtifactInvocation{
			CallerID: "runtime-invoke-core", InstanceID: instanceID,
			PageID:        adminActions.Publish.SurfaceBinding.PageID,
			ActionID:      adminActions.Publish.SurfaceBinding.ActionID,
			SurfaceDigest: surfaceDigest, IdempotencyKey: requestID, RequestID: requestID,
		}
		requestMetadata, marshalErr := json.Marshal(map[string]string{"sha256": digest})
		if marshalErr != nil {
			return errScenario
		}
		response, streamErr := client.ArtifactStream(ctx, invocation, requestMetadata, adminActions.Publish.ArchiveLimits.MediaType, io.NopCloser(bytes.NewReader(module)))
		if streamErr != nil || response.StatusCode != adminActions.Publish.AcceptedHTTPStatus ||
			!bytes.Equal(response.Body, requestMetadata) {
			return errScenario
		}
		return nil
	}
	if upload(echoDigest, echo, "echo-module-upload") != nil || upload(slowDigest, slow, "slow-module-upload") != nil {
		return errScenario
	}
	ack, err := client.Reload(ctx, sdkmodels.Reload{Generation: "runtime-invoke-slow", SHA256: core.generations["runtime-invoke-slow"].digest, SchemaVersion: schemaV1})
	if err != nil || !ack.Applied {
		if _, err := fmt.Fprintf(os.Stderr, "reload slow refused ack=%+v err=%v\n", ack, err); err != nil {
			os.Exit(1)
		}
		return errScenario
	}
	ready, err := client.Readiness(ctx)
	if err != nil || !ready.Ready || ready.Generation != "runtime-invoke-slow" {
		return errScenario
	}
	late.Cancelled = sut.callCancelledAlong(200*time.Millisecond, protocolpeer.Method("runtime.invoke"),
		request("spin", "tenant-a/site-1", nil, map[string]any{}))
	late.Timeout = sut.callRaw(runParent, protocolpeer.Method("runtime.invoke"),
		request("spin", "tenant-a/site-1", nil, map[string]any{}))

	echoAck, err := client.Reload(ctx, sdkmodels.Reload{Generation: "runtime-invoke-echo", SHA256: core.generations["runtime-invoke-echo"].digest, SchemaVersion: schemaV1})
	if err != nil || !echoAck.Applied {
		if _, err := fmt.Fprintf(os.Stderr, "reload echo refused ack=%+v err=%v\n", echoAck, err); err != nil {
			os.Exit(1)
		}
		return errScenario
	}
	ready, err = client.Readiness(ctx)
	if err != nil || !ready.Ready || ready.Generation != "runtime-invoke-echo" {
		return errScenario
	}

	late.Success = sut.call(runParent, request("echo", "tenant-a/site-1", nil, map[string]any{"message": "hello"}))
	late.SuccessWithEntity = sut.call(runParent, request("echo", "tenant-a/site-1", []string{"entity1"}, map[string]any{"message": "go"}))
	late.UnknownCommand = sut.call(runParent, request("missing", "tenant-a/site-1", nil, map[string]any{}))
	late.ForbiddenScope = sut.call(runParent, request("echo", "tenant-a/site-2", nil, map[string]any{}))
	late.ForbiddenEntity = sut.call(runParent, request("echo", "tenant-a/site-1", []string{"entity3"}, map[string]any{}))
	late.InvalidRequest = sut.call(runParent, requestWithField("echo", "tenant-a/site-1", map[string]any{"bogus": true}))
	late.PayloadTooLarge = sut.call(runParent, request("echo", "tenant-a/site-1", nil, map[string]any{"blob": strings.Repeat("x", 1<<20+256)}))
	late.UnknownMethod = sut.callRaw(runParent, protocolpeer.Method("runtime.noop"), []byte(`{}`))
	late.Unauthorized = sut.callAs(outsiderURI, credentials, runParent, protocolpeer.Method("runtime.invoke"),
		request("echo", "tenant-a/site-1", nil, map[string]any{"message": "hello"}))
	late.Envelope = sut.call(runParent, envelopePayload(
		request("echo", "tenant-a/site-1", nil, map[string]any{"message": "wrapped"})))

	// Keep a real Runtime child-process invocation in flight while shutdown is
	// requested. The long-running module is bounded by the ABI deadline; Runtime
	// must fence new work and wait for this admitted call before exiting.
	if _, err := client.Reload(ctx, sdkmodels.Reload{
		Generation: "runtime-invoke-slow", SHA256: core.generations["runtime-invoke-slow"].digest,
		SchemaVersion: schemaV1,
	}); err != nil {
		return errScenario
	}
	longCall := make(chan outcome, 1)
	go func() {
		callContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		longCall <- sut.call(callContext, request("spin", "tenant-a/site-1", nil, map[string]any{}))
	}()
	time.Sleep(200 * time.Millisecond)
	if err := command.Process.Signal(os.Interrupt); err != nil {
		return errScenario
	}
	time.Sleep(100 * time.Millisecond)
	refusalContext, cancelRefusal := context.WithTimeout(context.Background(), 2*time.Second)
	refusedAfterDrain := sut.call(refusalContext, request("spin", "tenant-a/site-1", nil, map[string]any{}))
	cancelRefusal()
	acceptedCall := <-longCall
	select {
	case processErr := <-processWait:
		processWaited = true
		late.ShutdownDrain.ProcessExitedAfterCall = processErr == nil
	case <-time.After(2 * time.Second):
		return errScenario
	}
	late.ShutdownDrain.AcceptedCall = acceptedCall
	late.ShutdownDrain.RefusedAfterDrain = refusedAfterDrain

	if late.Success.Status != 200 || late.Envelope.Status != 200 {
		return errScenario
	}
	return json.NewEncoder(os.Stdout).Encode(late)
}

func (sut *harness) call(ctx context.Context, payload []byte) outcome {
	return sut.callRaw(ctx, protocolpeer.Method("runtime.invoke"), payload)
}

func (sut *harness) callRaw(ctx context.Context, method protocolpeer.Method, payload []byte) outcome {
	raw, err := sut.client.Call(ctx, method, payload)
	return classify(raw.Payload, err)
}

func (sut *harness) callAs(identity string, credentials testCredentials, ctx context.Context, method protocolpeer.Method, payload []byte) outcome {
	if identity != outsiderURI {
		return outcome{Err: "unexpected identity"}
	}
	rootPEM, err := os.ReadFile(credentials.caFile)
	if err != nil {
		return outcome{Err: "read ca"}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(rootPEM)
	outsiderCertificate, err := tls.LoadX509KeyPair(credentials.outsiderPeerCert, credentials.outsiderPeerKey)
	if err != nil {
		return outcome{Err: "load outsider cert"}
	}
	handler, err := protocolpeer.NewRegistry().Build()
	if err != nil {
		return outcome{Err: "assemble"}
	}
	session, err := protocolpeer.Dial(ctx, protocolpeer.ClientConfig{
		Network: protocolpeer.NetworkConfig{Carrier: protocolpeer.CarrierTCP, Endpoint: sut.peerAddress, ServerName: "localhost"},
		Security: protocolpeer.SecurityConfig{
			Identity: outsiderURI, Certificate: outsiderCertificate, Roots: roots,
		},
		Handler: handler,
		Limits:  protocolpeer.DefaultLimits(),
	})
	if err != nil {
		return outcome{Err: "dial"}
	}
	defer func() { checkCleanup(session.Close()) }()
	raw, err := session.Call(ctx, method, payload)
	return classify(raw.Payload, err)
}

func (sut *harness) callCancelledAlong(cancelAfter time.Duration, method protocolpeer.Method, payload []byte) outcome {
	callContext, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(cancelAfter)
		cancel()
	}()
	raw, err := sut.client.Call(callContext, method, payload)
	return classify(raw.Payload, err)
}

func classify(raw []byte, err error) outcome {
	if err != nil {
		return outcome{Err: err.Error(), Transport: transportKind(err)}
	}
	var action struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if json.Unmarshal(raw, &action) != nil {
		return outcome{Err: "malformed action", Transport: "malformed"}
	}
	returned := outcome{Status: action.Status}
	var document struct {
		Code    string          `json:"code"`
		Command string          `json:"command"`
		Output  json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal([]byte(action.Body), &document); err != nil {
		return outcome{Err: "malformed body", Transport: "malformed"}
	}
	returned.Code, returned.Command = document.Code, document.Command
	returned.Body = json.RawMessage(action.Body)
	if len(document.Output) > 0 {
		returned.Output = json.RawMessage(document.Output)
	}
	return returned
}

func transportKind(err error) string {
	switch {
	case errors.Is(err, protocolpeer.ErrMethodNotFound):
		return "method_not_found"
	case errors.Is(err, protocolpeer.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, context.Canceled), errors.Is(err, protocolpeer.ErrCanceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, protocolpeer.ErrDeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, protocolpeer.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, protocolpeer.ErrOverloaded):
		return "overloaded"
	default:
		return "other"
	}
}

func request(command, scope string, entities []string, input map[string]any) []byte {
	document := map[string]any{"command": command, "scope": scope, "input": input}
	if len(entities) > 0 {
		document["entities"] = entities
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return []byte(`{}`)
	}
	return payload
}

func requestWithField(command, scope string, fields map[string]any) []byte {
	document := map[string]any{"command": command, "scope": scope, "input": map[string]any{}}
	for key, value := range fields {
		document[key] = value
	}
	payload, err := json.Marshal(document)
	if err != nil {
		os.Exit(1)
	}
	return payload
}

func envelopePayload(body []byte) []byte {
	payload, err := json.Marshal(map[string]any{
		"method": "POST", "path": "/runtimes/runtime-invoke-e2e/invoke", "query": "",
		"headers":    map[string]string{"Content-Type": "application/json"},
		"cookies":    nil,
		"body":       base64.StdEncoding.EncodeToString(body),
		"requestId":  "peer-envelope-1",
		"remoteAddr": "10.0.0.1:443",
	})
	if err != nil {
		return []byte(`{}`)
	}
	return payload
}

func (core *scenario) handler() http.Handler {
	mux := http.NewServeMux()
	pull := core.contract.Core.ConfigPull
	mux.HandleFunc(pull.Method+" "+pull.PathTemplate, func(writer http.ResponseWriter, request *http.Request) {
		generation, ok := core.generations[request.PathValue("generation")]
		if !ok {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		headers := pull.ResponseHeaders
		writer.Header().Set(headers["generation"], request.PathValue("generation"))
		writer.Header().Set(headers["sha256"], generation.digest)
		writer.Header().Set(headers["schemaVersion"], schemaV1)
		writer.Header().Set(headers["generationState"], "active")
		writer.Header().Set("Content-Type", pull.ResponseMediaType)
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write(generation.bytes); err != nil {
			return
		}
	})
	return mux
}

func publish(document string) storedGeneration {
	bytes := []byte(document)
	return storedGeneration{bytes: bytes, digest: sdkmodels.Digest(bytes)}
}

func configurationDocument(groupID, moduleDigest, commandName string) string {
	return fmt.Sprintf(`{"schemaVersion":"1","groupId":%q,"modelInstanceId":"invoke","module":{"sha256":%q},"commands":[{"name":%q,"export":"invoke","tenantSites":["tenant-a/site-1"],"entities":["entity1","entity2"]}]}`, groupID, moduleDigest, commandName)
}

type testCredentials struct {
	caFile            string
	crlFile           string
	coreServerCert    string
	coreServerKey     string
	coreClientCert    string
	coreClientKey     string
	runtimeServerCert string
	runtimeServerKey  string
	runtimeClientCert string
	runtimeClientKey  string
	runtimePeerCert   string
	runtimePeerKey    string
	callerPeerCert    string
	callerPeerKey     string
	outsiderPeerCert  string
	outsiderPeerKey   string
}

func issueTestCredentials(directory string) (testCredentials, error) {
	now := time.Now().UTC()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCredentials{}, errScenario
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "runtime-invoke-authority"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true, IsCA: true, SubjectKeyId: []byte{1, 2, 3, 4},
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return testCredentials{}, errScenario
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return testCredentials{}, errScenario
	}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	paths := testCredentials{
		caFile: filepath.Join(directory, "invoke-ca.pem"), crlFile: filepath.Join(directory, "invoke-revocations.pem"),
		coreServerCert: filepath.Join(directory, "core-server.pem"), coreServerKey: filepath.Join(directory, "core-server-key.pem"),
		coreClientCert: filepath.Join(directory, "core-client.pem"), coreClientKey: filepath.Join(directory, "core-client-key.pem"),
		runtimeServerCert: filepath.Join(directory, "runtime-server.pem"), runtimeServerKey: filepath.Join(directory, "runtime-server-key.pem"),
		runtimeClientCert: filepath.Join(directory, "runtime-client.pem"), runtimeClientKey: filepath.Join(directory, "runtime-client-key.pem"),
		runtimePeerCert: filepath.Join(directory, "runtime-peer.pem"), runtimePeerKey: filepath.Join(directory, "runtime-peer-key.pem"),
		callerPeerCert: filepath.Join(directory, "caller-peer.pem"), callerPeerKey: filepath.Join(directory, "caller-peer-key.pem"),
		outsiderPeerCert: filepath.Join(directory, "outsider-peer.pem"), outsiderPeerKey: filepath.Join(directory, "outsider-peer-key.pem"),
	}
	if err := writeSecure(paths.caFile, rootPEM); err != nil {
		return testCredentials{}, errScenario
	}
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(12 * time.Hour),
	}, root, rootKey)
	if err != nil {
		return testCredentials{}, errScenario
	}
	if err := writeSecure(paths.crlFile, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER})); err != nil {
		return testCredentials{}, errScenario
	}
	for index, pair := range []struct{ cn, cert, key string }{
		{coreCN, paths.coreServerCert, paths.coreServerKey},
		{coreCN, paths.coreClientCert, paths.coreClientKey},
		{runtimeCN, paths.runtimeServerCert, paths.runtimeServerKey},
		{runtimeCN, paths.runtimeClientCert, paths.runtimeClientKey},
	} {
		if err := issueLeaf(now, root, rootKey, int64(index+2), pair.cn, pair.cert, pair.key); err != nil {
			return testCredentials{}, errScenario
		}
	}
	for index, pair := range []struct{ cn, uri, cert, key string }{
		{runtimeCN, runtimePeerURI, paths.runtimePeerCert, paths.runtimePeerKey},
		{callerCN, callerPeerURI, paths.callerPeerCert, paths.callerPeerKey},
		{outsiderCN, outsiderURI, paths.outsiderPeerCert, paths.outsiderPeerKey},
	} {
		if err := issuePeerLeaf(now, root, rootKey, int64(index+10), pair.cn, pair.uri, pair.cert, pair.key); err != nil {
			return testCredentials{}, errScenario
		}
	}
	return paths, nil
}

func issueLeaf(now time.Time, authority *x509.Certificate, authorityKey *ecdsa.PrivateKey, serial int64, commonName, certificatePath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errScenario
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:    []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, authorityKey)
	if err != nil {
		return errScenario
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return errScenario
	}
	if err := writeSecure(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})); err != nil {
		return errScenario
	}
	return writeSecure(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func issuePeerLeaf(now time.Time, authority *x509.Certificate, authorityKey *ecdsa.PrivateKey, serial int64, commonName, uri, certificatePath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errScenario
	}
	peerURI, err := url.Parse(uri)
	if err != nil {
		return errScenario
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:    []string{"localhost"},
		URIs:        []*url.URL{peerURI},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, authority, &key.PublicKey, authorityKey)
	if err != nil {
		return errScenario
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return errScenario
	}
	if err := writeSecure(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})); err != nil {
		return errScenario
	}
	return writeSecure(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func writeSecure(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0600)
}

func repositoryRoot() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	return workingDirectory
}

// checkCleanup accepts idempotent teardown outcomes and fails on unexpected resource errors.
func checkCleanup(err error) {
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, net.ErrClosed) {
		os.Exit(1)
	}
}
