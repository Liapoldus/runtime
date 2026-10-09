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
	"sync/atomic"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	"github.com/Liapoldus/plugin-sdk/infrastructure"
	"github.com/Liapoldus/runtime/contracts"
	"github.com/Liapoldus/runtime/internal/domain/models"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
)

var errScenario = errors.New("runtime restart/reconnect integration scenario failed")

const (
	instanceID     = "runtime-restart-e2e"
	replicaID      = "runtime-restart-e2e-1"
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
	down        atomic.Bool
}

type readinessResult struct {
	Generation        string `json:"generation"`
	PendingGeneration string `json:"pendingGeneration"`
	Ready             bool   `json:"ready"`
}

type reloadResult struct {
	Applied    bool   `json:"applied"`
	Outcome    string `json:"outcome"`
	Classified string `json:"classified,omitempty"`
}

type catalogResult struct {
	Status int `json:"status"`
	Count  int `json:"count"`
}

type runtimeProcess struct {
	address struct {
		ListenAddress     string `json:"listenAddress"`
		PeerListenAddress string `json:"peerListenAddress"`
	}
	command *exec.Cmd
	wait    <-chan error
}

type catalogView struct {
	StatusCode int
	Items      []models.ModuleArtifact
}

type result struct {
	Phase1 map[string]any `json:"phase1"`
	Phase2 map[string]any `json:"phase2"`
}

func main() {
	if err := run(); err != nil {
		if _, err := fmt.Fprintln(os.Stderr, "Runtime restart/reconnect integration failed"); err != nil {
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
	work, err := os.MkdirTemp("", "runtime-restart-e2e-")
	if err != nil {
		return errScenario
	}
	defer func() { checkCleanup(os.RemoveAll(work)) }()
	credentials, err := issueTestCredentials(work)
	if err != nil {
		return errScenario
	}
	artifactDir := filepath.Join(work, "artifacts")
	artifactStore := artifacts.Store{Root: artifactDir}
	moduleA, err := hex.DecodeString("0061736d01000000010c0260017f017f60027f7f017e03030200010503010001071b03066d656d6f7279020005616c6c6f63000006696e766f6b6500010a1302040041000b0c002001ad4220862000ad840b")
	if err != nil {
		return errScenario
	}
	moduleADigest := sdkmodels.Digest(moduleA)
	metadata, err := contracts.PluginDocuments()
	if err != nil {
		return errScenario
	}
	adminActions := contracts.ModuleAdminActionDefinitions()
	generations := map[string]storedGeneration{
		"runtime-restart-1": publish(configurationDocument("initial", moduleADigest, false)),
		"runtime-restart-2": publish(configurationDocument("replacement", moduleADigest, false)),
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
	if err := build.Run(); err != nil {
		return errScenario
	}
	coreURL := "https://" + listener.Addr().String()

	// Phase 1: healthy Core, first runtime replica applies the initial generation.
	first, err := startRuntime(runtimeBinary, artifactDir, credentials, coreURL)
	if err != nil {
		return errScenario
	}
	firstClient, err := newClient(contract, credentials, first.address.ListenAddress)
	if err != nil {
		stopRuntime(first)
		return errScenario
	}
	phase1, err := phaseOne(firstClient, artifactStore, adminActions, metadata, moduleA, moduleADigest, instanceID, generations)
	if err != nil {
		stopRuntime(first)
		return err
	}
	stopRuntime(first)

	// Phase 2: Core goes offline, runtime replica restarts, Core returns.
	core.down.Store(true)
	second, err := startRuntime(runtimeBinary, artifactDir, credentials, coreURL)
	if err != nil {
		return errScenario
	}
	defer stopRuntime(second)
	secondClient, err := newClient(contract, credentials, second.address.ListenAddress)
	if err != nil {
		return errScenario
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The replica is up but Core is unavailable: the exact-generation pull must
	// fail cleanly and the replica must stay alive serving empty readiness.
	pullDownAck, pullDownErr := secondClient.Reload(ctx, descriptor("runtime-restart-1", generations["runtime-restart-1"]))
	pullDownClassified := string(secondClient.ClassifyOutcome(pullDownErr))
	readinessDown, readinessErr := secondClient.Readiness(ctx)
	if readinessErr != nil {
		return errScenario
	}

	// Core returns; the resumed replica re-pulls the exact generation. The module
	// file survived the replica restart on disk, so no re-upload is needed.
	core.down.Store(false)
	resumeAck, err := secondClient.Reload(ctx, descriptor("runtime-restart-1", generations["runtime-restart-1"]))
	if err != nil || !resumeAck.Applied {
		return errScenario
	}
	resumed, err := secondClient.Readiness(ctx)
	if err != nil {
		return errScenario
	}
	catalogAfterRestart, err := listCatalog(ctx, secondClient, adminActions, instanceID, surfaceDigestOf(metadata))
	if err != nil {
		return errScenario
	}

	// A repeat announcement of the same exact generation must not churn the
	// active pair; it is reported as an idempotency conflict.
	repeatAck, repeatErr := secondClient.Reload(ctx, descriptor("runtime-restart-1", generations["runtime-restart-1"]))
	repeatClassified := string(secondClient.ClassifyOutcome(repeatErr))
	afterRepeat, err := secondClient.Readiness(ctx)
	if err != nil {
		return errScenario
	}

	// Reconnected replica accepts a following exact generation.
	nextAck, err := secondClient.Reload(ctx, descriptor("runtime-restart-2", generations["runtime-restart-2"]))
	if err != nil || !nextAck.Applied {
		return errScenario
	}
	afterNext, err := secondClient.Readiness(ctx)
	if err != nil {
		return errScenario
	}

	phase2 := map[string]any{
		"pullWhileCoreDown":      reloadResult{Applied: pullDownAck.Applied, Outcome: string(pullDownAck.Outcome), Classified: pullDownClassified},
		"readinessWhileCoreDown": readinessResult{Generation: readinessDown.Generation, PendingGeneration: readinessDown.PendingGeneration, Ready: readinessDown.Ready},
		"catalogAfterRestart":    catalogResult{Status: catalogAfterRestart.StatusCode, Count: len(catalogAfterRestart.Items)},
		"reloadResumed":          reloadResult{Applied: resumeAck.Applied, Outcome: string(resumeAck.Outcome)},
		"readinessResumed":       readinessResult{Generation: resumed.Generation, PendingGeneration: resumed.PendingGeneration, Ready: resumed.Ready},
		"repeatAnnounce":         reloadResult{Applied: repeatAck.Applied, Outcome: string(repeatAck.Outcome), Classified: repeatClassified},
		"readinessAfterRepeat":   readinessResult{Generation: afterRepeat.Generation, PendingGeneration: afterRepeat.PendingGeneration, Ready: afterRepeat.Ready},
		"reloadNext":             reloadResult{Applied: nextAck.Applied, Outcome: string(nextAck.Outcome)},
		"readinessNext":          readinessResult{Generation: afterNext.Generation, PendingGeneration: afterNext.PendingGeneration, Ready: afterNext.Ready},
	}
	return json.NewEncoder(os.Stdout).Encode(result{Phase1: phase1, Phase2: phase2})
}

func phaseOne(client *infrastructure.PluginClient, store artifacts.Store, adminActions contracts.ModuleAdminActions, metadata contracts.PluginMetadata, module []byte, moduleDigest, instanceID string, generations map[string]storedGeneration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	surfaceDigest := surfaceDigestOf(metadata)
	invocation := sdkmodels.ArtifactInvocation{
		CallerID: "runtime-test-core", InstanceID: instanceID,
		PageID:        adminActions.Publish.SurfaceBinding.PageID,
		ActionID:      adminActions.Publish.SurfaceBinding.ActionID,
		SurfaceDigest: surfaceDigest, IdempotencyKey: "module-upload-a", RequestID: "module-upload-a",
	}
	requestMetadata, err := json.Marshal(map[string]string{"sha256": moduleDigest})
	if err != nil {
		return nil, errScenario
	}
	response, err := client.ArtifactStream(ctx, invocation, requestMetadata, adminActions.Publish.ArchiveLimits.MediaType, io.NopCloser(bytes.NewReader(module)))
	if err != nil || response.StatusCode != adminActions.Publish.AcceptedHTTPStatus || !bytes.Equal(response.Body, requestMetadata) {
		return nil, errScenario
	}
	catalog, err := listCatalog(ctx, client, adminActions, instanceID, surfaceDigest)
	if err != nil {
		return nil, err
	}
	ack, err := client.Reload(ctx, descriptor("runtime-restart-1", generations["runtime-restart-1"]))
	if err != nil || !ack.Applied {
		return nil, errScenario
	}
	readiness, err := client.Readiness(ctx)
	if err != nil {
		return nil, errScenario
	}
	return map[string]any{
		"uploadAccepted": true,
		"catalog":        catalogResult{Status: catalog.StatusCode, Count: len(catalog.Items)},
		"reload":         reloadResult{Applied: ack.Applied, Outcome: string(ack.Outcome)},
		"readiness":      readinessResult{Generation: readiness.Generation, PendingGeneration: readiness.PendingGeneration, Ready: readiness.Ready},
	}, nil
}

func surfaceDigestOf(metadata contracts.PluginMetadata) string {
	surfaceHash := sha256.Sum256(metadata.AdminSurface)
	return "sha256:" + hex.EncodeToString(surfaceHash[:])
}

func listCatalog(ctx context.Context, client *infrastructure.PluginClient, adminActions contracts.ModuleAdminActions, instanceID, surfaceDigest string) (catalogView, error) {
	invocation := sdkmodels.AdminActionInvocation{
		CallerID: "runtime-test-core", InstanceID: instanceID,
		PageID: adminActions.List.SurfaceBinding.PageID, ActionID: adminActions.List.Capability,
		SurfaceDigest: surfaceDigest, RequestID: "module-catalog-read",
	}
	catalog, err := client.AdminAction(ctx, invocation, []byte(`{}`))
	if err != nil || catalog.StatusCode != adminActions.List.HTTPStatus {
		return catalogView{}, errScenario
	}
	var body struct {
		Items []models.ModuleArtifact `json:"items"`
	}
	if json.Unmarshal(catalog.Body, &body) != nil {
		return catalogView{}, errScenario
	}
	return catalogView{StatusCode: catalog.StatusCode, Items: body.Items}, nil
}

func startRuntime(binary, artifactDir string, credentials testCredentials, coreURL string) (*runtimeProcess, error) {
	//nolint:gosec // G204: runs a fixed Go command or the fixture-built Runtime binary, without a shell.
	command := exec.CommandContext(context.Background(), binary,
		"--listen", "127.0.0.1:0",
		"--core-url", coreURL,
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
		"--artifact-dir", artifactDir,
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
		return nil, errScenario
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, errScenario
	}
	process := &runtimeProcess{command: command}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	process.wait = wait
	readyLine, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		stopRuntime(process)
		return nil, errScenario
	}
	if json.Unmarshal(readyLine, &process.address) != nil || process.address.ListenAddress == "" || process.address.PeerListenAddress == "" {
		stopRuntime(process)
		return nil, errScenario
	}
	return process, nil
}

func stopRuntime(process *runtimeProcess) {
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	checkCleanup(process.command.Process.Signal(os.Interrupt))
	if process.wait != nil {
		select {
		case <-process.wait:
		case <-time.After(5 * time.Second):
			checkCleanup(process.command.Process.Kill())
			<-process.wait
		}
	}
}

func newClient(contract infrastructure.HTTPContract, credentials testCredentials, address string) (*infrastructure.PluginClient, error) {
	coreCredentials, err := infrastructure.LoadCredentials(contract, infrastructure.CredentialsMaterial{
		CAFile:                credentials.caFile,
		ServerCertificateFile: credentials.coreServerCert,
		ServerKeyFile:         credentials.coreServerKey,
		ClientCertificateFile: credentials.coreClientCert,
		ClientKeyFile:         credentials.coreClientKey,
	})
	if err != nil {
		return nil, errScenario
	}
	provider, err := infrastructure.NewStaticCredentialsProvider(coreCredentials)
	if err != nil {
		return nil, errScenario
	}
	revocation, err := infrastructure.NewRevocation(infrastructure.RevocationConfiguration{
		Authorities: coreCredentials.TrustAuthorities(),
		Files:       []string{credentials.crlFile},
	})
	if err != nil {
		return nil, errScenario
	}
	peer, err := sdkmodels.NewPeerIdentity(runtimeCN, "")
	if err != nil {
		return nil, errScenario
	}
	transport, err := infrastructure.NewMutualTLSClient(contract, provider, infrastructure.MutualTLSClientConfig{
		Peer:       peer,
		ServerName: "localhost",
		Revocation: revocation,
	})
	if err != nil {
		return nil, errScenario
	}
	client, err := infrastructure.NewPluginClient(contract, "https://"+address, transport, peer)
	if err != nil {
		return nil, errScenario
	}
	return client, nil
}

func (core *scenario) handler() http.Handler {
	mux := http.NewServeMux()
	pull := core.contract.Core.ConfigPull
	mux.HandleFunc(pull.Method+" "+pull.PathTemplate, func(writer http.ResponseWriter, request *http.Request) {
		if core.down.Load() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
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

func repositoryRoot() string {
	workingDirectory, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}
	return workingDirectory
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

// checkCleanup accepts idempotent teardown outcomes and fails on unexpected resource errors.
func checkCleanup(err error) {
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, net.ErrClosed) {
		os.Exit(1)
	}
}
