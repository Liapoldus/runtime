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
	"sync/atomic"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	"github.com/Liapoldus/plugin-sdk/v2/infrastructure"
	protocolpeer "github.com/Liapoldus/pluginprotocol/v3/presentation/peer"
	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
	"github.com/Liapoldus/runtime/tests/fixtures/lifecyclecore"
)

var errScenario = errors.New("runtime invoke integration scenario failed")

const (
	instanceID           = "runtime-invoke-e2e"
	replicaID            = "runtime-invoke-e2e-1"
	coreCN               = "runtime-invoke-core"
	runtimeCN            = "runtime-invoke-plugin"
	callerCN             = "runtime-invoke-caller"
	outsiderCN           = "runtime-invoke-outsider"
	runtimePeerURI       = "spiffe://liapoldus/plugin/runtime-invoke-e2e/runtime-invoke-e2e-1/inc-runtime"
	placementID          = "node-runtime"
	incarnationID        = "inc-runtime"
	callerPeerURI        = "spiffe://liapoldus.test/server"
	outsiderURI          = "spiffe://liapoldus.test/outsider"
	metricsInstanceID    = "metrics-main"
	metricsReplicaID     = "metrics-main-1"
	metricsIncarnationID = "inc-metrics"
	metricsPlacementID   = "node-metrics"
	metricsPeerURI       = "spiffe://liapoldus/plugin/metrics-main/metrics-main-1/inc-metrics"
	schemaVersion        = "2"
)

type storedGeneration struct {
	bytes  []byte
	digest string
}

type scenario struct {
	contract          infrastructure.HTTPContract
	directoryContract infrastructure.PeerDirectoryPollContract
	runtimeIdentity   sdkmodels.PeerReplicaID
	metricsAddress    string
	generations       map[string]storedGeneration
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

// hostCallWasm makes one real versioned Runtime host call. The host-call JSON
// lives in the guest memory data segment so cancellation is exercised across
// the child process, Core directory poll, mTLS protocol session and handler.
func hostCallWasm(payload []byte) []byte {
	uleb := func(value int) []byte {
		encoded := make([]byte, 0, 5)
		for value >= 0x80 {
			encoded = append(encoded, byte(value&0x7f)|0x80)
			value >>= 7
		}
		return append(encoded, byte(value&0x7f))
	}
	name := func(value string) []byte {
		encoded := append([]byte(nil), uleb(len(value))...)
		return append(encoded, value...)
	}
	section := func(module []byte, id byte, body []byte) []byte {
		module = append(module, id)
		module = append(module, uleb(len(body))...)
		return append(module, body...)
	}
	vector := func(body, item []byte) []byte {
		body = append(body, uleb(len(item))...)
		return append(body, item...)
	}
	i32const := func(body []byte, value int64) []byte {
		body = append(body, 0x41)
		remaining := value
		for {
			current := byte(remaining & 0x7f)
			remaining >>= 7
			more := true
			if remaining == 0 && current&0x40 == 0 || remaining == -1 && current&0x40 != 0 {
				more = false
			}
			if more {
				current |= 0x80
			}
			body = append(body, current)
			if !more {
				return body
			}
		}
	}

	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	module = section(module, 1, []byte{2, 0x60, 1, 0x7f, 1, 0x7f, 0x60, 2, 0x7f, 0x7f, 1, 0x7e})
	imports := []byte{1}
	imports = append(imports, name("liapoldus.plugin.v2")...)
	imports = append(imports, name("call")...)
	imports = append(imports, 0, 1)
	module = section(module, 2, imports)
	module = section(module, 3, []byte{2, 0, 1})
	module = section(module, 5, []byte{1, 0, 1})
	exports := []byte{3}
	exports = append(exports, name("memory")...)
	exports = append(exports, 2, 0)
	exports = append(exports, name("alloc")...)
	exports = append(exports, 0, 1)
	exports = append(exports, name("invoke")...)
	exports = append(exports, 0, 2)
	module = section(module, 7, exports)
	allocate := i32const([]byte{0}, 4096)
	allocate = append(allocate, 0x0b)
	invoke := i32const([]byte{0}, 0)
	invoke = i32const(invoke, int64(len(payload)))
	invoke = append(invoke, 0x10, 0, 0x0b)
	codes := []byte{2}
	codes = vector(codes, allocate)
	codes = vector(codes, invoke)
	module = section(module, 10, codes)
	data := []byte{1, 0, 0x41, 0, 0x0b}
	data = vector(data, payload)
	return section(module, 11, data)
}

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
	UnknownCommand    outcome `json:"unknownCommand"`
	InvalidRequest    outcome `json:"invalidRequest"`
	PayloadTooLarge   outcome `json:"payloadTooLarge"`
	UnknownMethod     outcome `json:"unknownMethod"`
	Unauthorized      outcome `json:"unauthorized"`
	Cancelled         outcome `json:"cancelled"`
	Timeout           outcome `json:"timeout"`
	Envelope          outcome `json:"envelope"`
	HostCallCancelled struct {
		Call                        outcome `json:"call"`
		RemoteHandlerObservedCancel bool    `json:"remoteHandlerObservedCancel"`
		DeliveryCount               int64   `json:"deliveryCount"`
	} `json:"hostCallCancelled"`
	ShutdownDrain struct {
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
		if _, err := fmt.Fprintf(os.Stderr, "Runtime invoke integration failed: %v\n", err); err != nil {
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
	hostPayload, err := json.Marshal(map[string]any{
		"plugin": metricsInstanceID, "method": "metrics.aggregate", "payload": map[string]string{"window": "daily"},
	})
	if err != nil {
		return errScenario
	}
	drainHostPayload, err := json.Marshal(map[string]any{
		"plugin": metricsInstanceID, "method": "metrics.aggregate", "payload": map[string]string{"window": "drain"},
	})
	if err != nil {
		return errScenario
	}
	hostModule := hostCallWasm(hostPayload)
	drainHostModule := hostCallWasm(drainHostPayload)
	echoDigest, slowDigest := sdkmodels.Digest(echo), sdkmodels.Digest(slow)
	hostDigest, drainHostDigest := sdkmodels.Digest(hostModule), sdkmodels.Digest(drainHostModule)
	metadata, err := contracts.PluginDocuments()
	if err != nil {
		return errScenario
	}
	adminActions := contracts.ModuleAdminActionDefinitions()
	core := &scenario{contract: contract, generations: map[string]storedGeneration{
		"runtime-invoke-slow":  publish(configurationDocument("runtimeinvokegroup", slowDigest, "spin")),
		"runtime-invoke-drain": publish(drainHostConfigurationDocument(drainHostDigest)),
		"runtime-invoke-echo":  publish(configurationDocument("runtimeinvokegroup", echoDigest, "echo")),
		"runtime-invoke-host":  publish(hostCallConfigurationDocument(hostDigest)),
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
	metricsCertificate, err := tls.LoadX509KeyPair(credentials.metricsPeerCert, credentials.metricsPeerKey)
	if err != nil {
		return errScenario
	}
	remoteStarted := make(chan struct{}, 1)
	remoteCancelled := make(chan struct{}, 1)
	drainCallStarted := make(chan struct{}, 1)
	drainCallRelease := make(chan struct{})
	var remoteDeliveries atomic.Int64
	remoteHandler, err := protocolpeer.NewRegistry().RegisterCall("metrics.aggregate", func(ctx context.Context, call protocolpeer.Call) (protocolpeer.Result, error) {
		remoteDeliveries.Add(1)
		var request struct {
			Window string `json:"window"`
		}
		if json.Unmarshal(call.Payload, &request) == nil && request.Window == "drain" {
			drainCallStarted <- struct{}{}
			select {
			case <-drainCallRelease:
				return protocolpeer.Result{Payload: []byte(`{"ok":true}`)}, nil
			case <-ctx.Done():
				return protocolpeer.Result{}, ctx.Err()
			}
		}
		remoteStarted <- struct{}{}
		<-ctx.Done()
		remoteCancelled <- struct{}{}
		return protocolpeer.Result{}, ctx.Err()
	}).Build()
	if err != nil {
		return errScenario
	}
	metricsServer, err := protocolpeer.Listen(protocolpeer.ServerConfig{
		Network:  protocolpeer.NetworkConfig{Carrier: protocolpeer.CarrierTCP, Endpoint: "127.0.0.1:0"},
		Security: protocolpeer.SecurityConfig{Identity: metricsPeerURI, Certificate: metricsCertificate, Roots: roots},
		Handler:  remoteHandler,
	})
	if err != nil {
		return errScenario
	}
	metricsContext, stopMetrics := context.WithCancel(context.Background())
	metricsDone := make(chan error, 1)
	go func() { metricsDone <- metricsServer.Sessions(metricsContext) }()
	defer func() {
		stopMetrics()
		checkCleanup(metricsServer.Close())
		<-metricsDone
	}()
	runtimePeerCertificate, err := tls.LoadX509KeyPair(credentials.runtimePeerCert, credentials.runtimePeerKey)
	if err != nil {
		return errScenario
	}
	probeHandler, err := protocolpeer.NewRegistry().Build()
	if err != nil {
		return errScenario
	}
	probe, err := protocolpeer.Dial(context.Background(), protocolpeer.ClientConfig{
		Network:  protocolpeer.NetworkConfig{Carrier: protocolpeer.CarrierTCP, Endpoint: metricsServer.Addr()},
		Security: protocolpeer.SecurityConfig{Identity: runtimePeerURI, Certificate: runtimePeerCertificate, Roots: roots, PeerIdentity: metricsPeerURI},
		Handler:  probeHandler, Limits: protocolpeer.DefaultLimits(),
	})
	if err != nil {
		return fmt.Errorf("mock plugin mTLS probe failed: %w", err)
	}
	if err := probe.Close(); err != nil {
		return errScenario
	}
	runtimeIdentity := sdkmodels.PeerReplicaID{
		InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: incarnationID, PlacementID: placementID,
	}
	directoryContract, err := infrastructure.LoadPeerDirectoryPollContract()
	if err != nil {
		return errScenario
	}
	core.runtimeIdentity = runtimeIdentity
	core.directoryContract = directoryContract
	core.metricsAddress = metricsServer.Addr()
	coreCertificate, err := tls.LoadX509KeyPair(credentials.coreServerCert, credentials.coreServerKey)
	if err != nil {
		return errScenario
	}
	coreHandler, err := core.handler()
	if err != nil {
		return errScenario
	}
	coreServer := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           coreHandler,
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
		"--placement-id", placementID,
		"--incarnation-id", incarnationID,
		"--tls-ca-file", credentials.caFile,
		"--tls-server-cert-file", credentials.runtimeServerCert,
		"--tls-server-key-file", credentials.runtimeServerKey,
		"--tls-client-cert-file", credentials.runtimeClientCert,
		"--tls-client-key-file", credentials.runtimeClientKey,
		"--tls-crl-file", credentials.crlFile,
		"--artifact-dir", artifactStore.Root,
		"--peer-listen", "127.0.0.1:0",
		"--rest-advertise-url", "https://127.0.0.1:443",
		"--peer-advertise-endpoint", "127.0.0.1:443",
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

	notReady := sut.call(runParent, request("echo", nil, map[string]any{"message": "hello"}))
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
	if upload(echoDigest, echo, "echo-module-upload") != nil || upload(slowDigest, slow, "slow-module-upload") != nil ||
		upload(hostDigest, hostModule, "host-module-upload") != nil ||
		upload(drainHostDigest, drainHostModule, "drain-host-module-upload") != nil {
		return errScenario
	}
	ack, err := client.Reload(ctx, sdkmodels.Reload{Generation: "runtime-invoke-slow", SHA256: core.generations["runtime-invoke-slow"].digest, SchemaVersion: schemaVersion})
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
		request("spin", nil, map[string]any{}))
	late.Timeout = sut.callRaw(runParent, protocolpeer.Method("runtime.invoke"),
		request("spin", nil, map[string]any{}))

	echoAck, err := client.Reload(ctx, sdkmodels.Reload{Generation: "runtime-invoke-echo", SHA256: core.generations["runtime-invoke-echo"].digest, SchemaVersion: schemaVersion})
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

	late.Success = sut.call(runParent, request("echo", nil, map[string]any{"message": "hello"}))
	late.UnknownCommand = sut.call(runParent, request("missing", nil, map[string]any{}))
	late.InvalidRequest = sut.call(runParent, requestWithField("echo", map[string]any{"bogus": true}))
	late.PayloadTooLarge = sut.call(runParent, request("echo", nil, map[string]any{"blob": strings.Repeat("x", 1<<20+256)}))
	late.UnknownMethod = sut.callRaw(runParent, protocolpeer.Method("runtime.noop"), []byte(`{}`))
	late.Unauthorized = sut.callAs(outsiderURI, credentials, runParent, protocolpeer.Method("runtime.invoke"),
		request("echo", nil, map[string]any{"message": "hello"}))
	late.Envelope = sut.call(runParent, envelopePayload(
		request("echo", nil, map[string]any{"message": "wrapped"})))
	if _, err := client.Reload(ctx, sdkmodels.Reload{
		Generation: "runtime-invoke-host", SHA256: core.generations["runtime-invoke-host"].digest, SchemaVersion: schemaVersion,
	}); err != nil {
		return errScenario
	}
	callContext, cancelHostCall := context.WithCancel(context.Background())
	hostCallDone := make(chan outcome, 1)
	go func() {
		hostCallDone <- sut.call(callContext, request("hostcall", nil, map[string]any{}))
	}()
	select {
	case <-remoteStarted:
	case <-time.After(5 * time.Second):
		cancelHostCall()
		return errors.New("runtime child process did not reach the mock plugin host handler")
	}
	cancelHostCall()
	select {
	case late.HostCallCancelled.Call = <-hostCallDone:
	case <-time.After(5 * time.Second):
		return errors.New("cancelling Runtime child invocation did not complete the caller request")
	}
	select {
	case <-remoteCancelled:
		late.HostCallCancelled.RemoteHandlerObservedCancel = true
	case <-time.After(5 * time.Second):
		return errors.New("runtime child cancellation did not reach the remote plugin handler")
	}
	late.HostCallCancelled.DeliveryCount = remoteDeliveries.Load()

	// Hold a real outbound host call until the Runtime has admitted the invoke and
	// shutdown has fenced new calls. This barrier avoids relying on CPU speed to
	// make a finite WASM loop overlap the shutdown signal.
	if _, err := client.Reload(ctx, sdkmodels.Reload{
		Generation: "runtime-invoke-drain", SHA256: core.generations["runtime-invoke-drain"].digest,
		SchemaVersion: schemaVersion,
	}); err != nil {
		return errScenario
	}
	longCall := make(chan outcome, 1)
	go func() {
		callContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		longCall <- sut.call(callContext, request("drain", nil, map[string]any{}))
	}()
	select {
	case <-drainCallStarted:
	case early := <-longCall:
		return fmt.Errorf("drain host call completed before shutdown admission: %+v", early)
	case <-time.After(5 * time.Second):
		return errors.New("runtime invocation did not reach the drain host-call barrier")
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		return errScenario
	}
	time.Sleep(100 * time.Millisecond)
	refusalContext, cancelRefusal := context.WithTimeout(context.Background(), 2*time.Second)
	refusedAfterDrain := sut.call(refusalContext, request("drain", nil, map[string]any{}))
	cancelRefusal()
	close(drainCallRelease)
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

func request(command string, entities []string, input map[string]any) []byte {
	document := map[string]any{"command": command, "input": input}
	if entities != nil {
		document["entities"] = entities
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return []byte(`{}`)
	}
	return payload
}

func requestWithField(command string, fields map[string]any) []byte {
	document := map[string]any{"command": command, "input": map[string]any{}}
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

func (core *scenario) handler() (http.Handler, error) {
	mux := http.NewServeMux()
	lifecycleContract, err := infrastructure.LoadReplicaLifecycleContract()
	if err != nil {
		return nil, err
	}
	lifecyclecore.New(lifecycleContract).Register(mux)
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
		writer.Header().Set(headers["schemaVersion"], schemaVersion)
		writer.Header().Set(headers["generationState"], "active")
		writer.Header().Set("Content-Type", pull.ResponseMediaType)
		writer.WriteHeader(http.StatusOK)
		if _, err := writer.Write(generation.bytes); err != nil {
			return
		}
	})
	directoryEndpoint := core.directoryContract.Endpoint
	mux.HandleFunc(directoryEndpoint.Method+" "+directoryEndpoint.Path, func(writer http.ResponseWriter, request *http.Request) {
		if !core.runtimePeerAuthenticated(request) {
			http.Error(writer, "peer identity mismatch", http.StatusForbidden)
			return
		}
		query := request.URL.Query()
		if len(query) != 1 || query.Get(core.directoryContract.Poll.WaitParameter) != "0" {
			http.Error(writer, "invalid directory request", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		expires := now.Add(20 * time.Second)
		eligibleUntil := expires.Add(-time.Second)
		directory := sdkmodels.PeerDirectory{
			ContractVersion: sdkmodels.PeerDirectoryContractVersion,
			Generation:      "directory-1",
			IssuedAt:        now,
			ExpiresAt:       expires,
			Caller:          core.runtimeIdentity,
			Links: []sdkmodels.PeerLink{{
				LinkID: "metrics-link", TargetInstanceID: metricsInstanceID,
				PlacementRule: sdkmodels.PeerPlacementRemote, Carrier: sdkmodels.PeerCarrierTCP,
				SecurityProfile: sdkmodels.PeerSecurityMTLS, RequiredPeerContracts: []sdkmodels.ContractRange{},
				Replicas: []sdkmodels.PeerDirectoryReplica{{
					Identity: sdkmodels.PeerReplicaID{
						InstanceID: metricsInstanceID, ReplicaID: metricsReplicaID,
						IncarnationID: metricsIncarnationID, PlacementID: metricsPlacementID,
					},
					Endpoint: core.metricsAddress, Eligibility: sdkmodels.PeerEligibilityReady,
					EligibleUntil: &eligibleUntil, Weight: 1, PeerContracts: []sdkmodels.ContractVersion{},
				}},
			}},
		}
		if directory.Validate() != nil {
			http.Error(writer, "invalid fixture directory", http.StatusInternalServerError)
			return
		}
		body, err := json.Marshal(directory)
		if err != nil {
			http.Error(writer, "directory encoding failed", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", core.directoryContract.DirectoryMediaType)
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("ETag", `"directory-1"`)
		writer.WriteHeader(core.directoryContract.Responses.Directory.Status)
		if _, err := writer.Write(body); err != nil {
			return
		}
	})
	return mux, nil
}

func (core *scenario) runtimePeerAuthenticated(request *http.Request) bool {
	if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 {
		return false
	}
	for _, uri := range request.TLS.PeerCertificates[0].URIs {
		if uri.String() == runtimePeerURI {
			return true
		}
	}
	return false
}

func publish(document string) storedGeneration {
	bytes := []byte(document)
	return storedGeneration{bytes: bytes, digest: sdkmodels.Digest(bytes)}
}

func configurationDocument(_ string, moduleDigest, commandName string) string {
	return fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"app","sha256":%q}],"commands":[{"name":%q,"moduleId":"app","export":"invoke","entities":["entity1","entity2"]}]}`, moduleDigest, commandName)
}

func hostCallConfigurationDocument(moduleDigest string) string {
	return fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"app","sha256":%q}],"commands":[{"name":"hostcall","moduleId":"app","export":"invoke","entities":["entity1"],"calls":[{"plugin":%q,"method":"metrics.aggregate"}]}]}`, moduleDigest, metricsInstanceID)
}

func drainHostConfigurationDocument(moduleDigest string) string {
	return fmt.Sprintf(`{"schemaVersion":"2","modules":[{"moduleId":"app","sha256":%q}],"commands":[{"name":"drain","moduleId":"app","export":"invoke","entities":["entity1"],"calls":[{"plugin":%q,"method":"metrics.aggregate"}]}]}`, moduleDigest, metricsInstanceID)
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
	metricsPeerCert   string
	metricsPeerKey    string
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
		metricsPeerCert: filepath.Join(directory, "metrics-peer.pem"), metricsPeerKey: filepath.Join(directory, "metrics-peer-key.pem"),
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
		{"metrics-plugin", metricsPeerURI, paths.metricsPeerCert, paths.metricsPeerKey},
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
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
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
