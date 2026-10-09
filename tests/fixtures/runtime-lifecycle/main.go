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
	"sort"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	"github.com/Liapoldus/plugin-sdk/infrastructure"
	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

var errScenario = errors.New("runtime Reload integration scenario failed")

const (
	instanceID     = "runtime-e2e"
	replicaID      = "runtime-e2e-1"
	coreCN         = "runtime-test-core"
	runtimeCN      = "runtime-test-plugin"
	callerCN       = "runtime-test-caller"
	runtimePeerURI = "spiffe://liapoldus.test/runtime"
	callerPeerURI  = "spiffe://liapoldus.test/server"
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

type readinessResult struct {
	Generation        string `json:"generation"`
	PendingGeneration string `json:"pendingGeneration"`
	Ready             bool   `json:"ready"`
}

type result struct {
	Initial       readinessResult `json:"initial"`
	Invalid       map[string]any  `json:"invalid"`
	AfterInvalid  readinessResult `json:"afterInvalid"`
	Valid         map[string]any  `json:"valid"`
	AfterValid    readinessResult `json:"afterValid"`
	Artifacts     map[string]any  `json:"artifacts"`
	ModuleCatalog map[string]any  `json:"moduleCatalog"`
}

func main() {
	if err := run(); err != nil {
		if _, err := fmt.Fprintln(os.Stderr, "Runtime Reload integration failed"); err != nil {
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
	work, err := os.MkdirTemp("", "runtime-reload-e2e-")
	if err != nil {
		return errScenario
	}
	defer func() { checkCleanup(os.RemoveAll(work)) }()
	credentials, err := issueTestCredentials(work)
	if err != nil {
		return errScenario
	}
	artifactStore := artifacts.Store{Root: filepath.Join(work, "artifacts")}
	moduleA, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		return errScenario
	}
	moduleB := append(append([]byte(nil), moduleA...), 0, 3, 1, 'x', 0)
	moduleADigest, moduleBDigest := sdkmodels.Digest(moduleA), sdkmodels.Digest(moduleB)
	metadata, err := contracts.PluginDocuments()
	if err != nil {
		return errScenario
	}
	adminActions := contracts.ModuleAdminActionDefinitions()
	generations := map[string]storedGeneration{
		"runtime-generation-1": publish(configurationDocument("initial", moduleADigest, false)),
		"runtime-generation-2": publish(configurationDocument("invalid", moduleADigest, true)),
		"runtime-generation-3": publish(configurationDocument("replacement", moduleBDigest, false)),
	}
	core := &scenario{contract: contract, generations: generations}
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
	serveResult := make(chan error, 1)
	go func() { serveResult <- coreServer.ServeTLS(listener, "", "") }()
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
	if err := build.Run(); err != nil {
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
	defer func() {
		if command.Process != nil {
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
			CallerID: "runtime-test-core", InstanceID: instanceID,
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
	if upload(moduleADigest, moduleA, "module-upload-a") != nil || upload(moduleBDigest, moduleB, "module-upload-b") != nil {
		return errScenario
	}
	listInvocation := sdkmodels.AdminActionInvocation{
		CallerID: "runtime-test-core", InstanceID: instanceID,
		PageID: adminActions.List.SurfaceBinding.PageID, ActionID: adminActions.List.Capability,
		SurfaceDigest: surfaceDigest, RequestID: "module-catalog-read",
	}
	catalog, err := client.AdminAction(ctx, listInvocation, []byte(`{}`))
	if err != nil || catalog.StatusCode != adminActions.List.HTTPStatus {
		return errScenario
	}
	var catalogBody struct {
		Items []models.ModuleArtifact `json:"items"`
	}
	wantDigests := []string{moduleADigest, moduleBDigest}
	sort.Strings(wantDigests)
	if json.Unmarshal(catalog.Body, &catalogBody) != nil || len(catalogBody.Items) != 2 ||
		catalogBody.Items[0].SHA256 != wantDigests[0] || catalogBody.Items[1].SHA256 != wantDigests[1] {
		return errScenario
	}

	initialAck, err := client.Reload(ctx, descriptor("runtime-generation-1", generations["runtime-generation-1"]))
	if err != nil || !initialAck.Applied {
		return errScenario
	}
	initial, err := client.Readiness(ctx)
	if err != nil || initial.Generation != "runtime-generation-1" || !initial.Ready {
		return errScenario
	}
	invalidAck, invalidErr := client.Reload(ctx, descriptor("runtime-generation-2", generations["runtime-generation-2"]))
	if invalidErr == nil || client.ClassifyOutcome(invalidErr) != sdkmodels.OutcomeApplyRejected || invalidAck.Applied {
		return errScenario
	}
	afterInvalid, err := client.Readiness(ctx)
	if err != nil || afterInvalid.Generation != "runtime-generation-1" ||
		afterInvalid.PendingGeneration != "runtime-generation-2" || afterInvalid.Ready {
		return errScenario
	}
	validAck, err := client.Reload(ctx, descriptor("runtime-generation-3", generations["runtime-generation-3"]))
	if err != nil || !validAck.Applied {
		return errScenario
	}
	afterValid, err := client.Readiness(ctx)
	if err != nil || afterValid.Generation != "runtime-generation-3" || afterValid.PendingGeneration != "" || !afterValid.Ready {
		return errScenario
	}
	output := result{
		Initial:       readinessResult{Generation: initial.Generation, Ready: initial.Ready},
		Invalid:       map[string]any{"applied": invalidAck.Applied, "outcome": string(client.ClassifyOutcome(invalidErr))},
		AfterInvalid:  readinessResult{Generation: afterInvalid.Generation, PendingGeneration: afterInvalid.PendingGeneration, Ready: afterInvalid.Ready},
		Valid:         map[string]any{"applied": validAck.Applied, "outcome": string(validAck.Outcome)},
		AfterValid:    readinessResult{Generation: afterValid.Generation, PendingGeneration: afterValid.PendingGeneration, Ready: afterValid.Ready},
		Artifacts:     map[string]any{"accepted": 2, "status": adminActions.Publish.AcceptedHTTPStatus},
		ModuleCatalog: map[string]any{"status": catalog.StatusCode, "count": len(catalogBody.Items)},
	}
	return json.NewEncoder(os.Stdout).Encode(output)
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

func configurationDocument(groupID, moduleDigest string, invalid bool) string {
	unknown := ""
	if invalid {
		unknown = `,"unknown":true`
	}
	return fmt.Sprintf(`{"schemaVersion":"1","groupId":%q,"modelInstanceId":"domain","module":{"sha256":%q},"commands":[{"name":"submit","export":"invoke","tenantSites":["tenant/site"],"entities":["entries"]}]%s}`, groupID, moduleDigest, unknown)
}

func descriptor(generation string, stored storedGeneration) sdkmodels.Reload {
	return sdkmodels.Reload{Generation: generation, SHA256: stored.digest, SchemaVersion: schemaV1}
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
}

func issueTestCredentials(directory string) (testCredentials, error) {
	now := time.Now().UTC()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCredentials{}, errScenario
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "runtime-test-authority"},
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
		caFile: filepath.Join(directory, "ca.pem"), crlFile: filepath.Join(directory, "revocations.pem"),
		coreServerCert: filepath.Join(directory, "core-server.pem"), coreServerKey: filepath.Join(directory, "core-server-key.pem"),
		coreClientCert: filepath.Join(directory, "core-client.pem"), coreClientKey: filepath.Join(directory, "core-client-key.pem"),
		runtimeServerCert: filepath.Join(directory, "runtime-server.pem"), runtimeServerKey: filepath.Join(directory, "runtime-server-key.pem"),
		runtimeClientCert: filepath.Join(directory, "runtime-client.pem"), runtimeClientKey: filepath.Join(directory, "runtime-client-key.pem"),
		runtimePeerCert: filepath.Join(directory, "runtime-peer.pem"), runtimePeerKey: filepath.Join(directory, "runtime-peer-key.pem"),
		callerPeerCert: filepath.Join(directory, "caller-peer.pem"), callerPeerKey: filepath.Join(directory, "caller-peer-key.pem"),
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
