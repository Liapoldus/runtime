package peercaller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	sdkinterfaces "github.com/Liapoldus/service-sdk/v2/domain/interfaces"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/service-sdk/v2/infrastructure"
	protocolpeer "github.com/Liapoldus/wool/v3/presentation/peer"
)

func TestCallResolvesAuthorizedCoreLinkAndDoesNotRetry(t *testing.T) {
	now := time.Now().UTC()
	local := sdkmodels.PeerReplicaID{InstanceID: "runtime-main", ReplicaID: "runtime-1", IncarnationID: "inc-runtime", PlacementID: "node-a"}
	target := sdkmodels.PeerReplicaID{InstanceID: "domain-main", ReplicaID: "domain-1", IncarnationID: "inc-domain", PlacementID: "node-b"}
	eligibleUntil := now.Add(8 * time.Second)
	directory := sdkmodels.PeerDirectory{
		ContractVersion: sdkmodels.PeerDirectoryContractVersion,
		Generation:      "directory-1",
		IssuedAt:        now.Add(-time.Second),
		ExpiresAt:       now.Add(9 * time.Second),
		Caller:          local,
		Links: []sdkmodels.PeerLink{{
			LinkID: "link-1", TargetInstanceID: target.InstanceID,
			PlacementRule: sdkmodels.PeerPlacementRemote, Carrier: sdkmodels.PeerCarrierTCP,
			SecurityProfile:       sdkmodels.PeerSecurityMTLS,
			RequiredPeerContracts: []sdkmodels.ContractRange{},
			Replicas: []sdkmodels.PeerDirectoryReplica{{
				Identity: target, Endpoint: "127.0.0.1:9000", Eligibility: sdkmodels.PeerEligibilityReady,
				EligibleUntil: &eligibleUntil, Weight: 1, PeerContracts: []sdkmodels.ContractVersion{},
			}},
		}},
	}
	if err := directory.Validate(); err != nil {
		t.Fatalf("test directory invalid: %v", err)
	}
	remoteURI := "spiffe://liapoldus/service/domain-main/domain-1/inc-domain"
	session := &fakeSession{peer: protocolpeer.PeerIdentity{URI: remoteURI}, result: protocolpeer.Result{Payload: []byte(`{"ok":true,"data":{"id":"x"}}`)}}
	dialer := &fakeDialer{session: session}
	var pollContract sdkinfrastructure.PeerDirectoryPollContract
	pollContract.Identity.URITemplate = "spiffe://liapoldus/service/{instanceId}/{replicaId}/{incarnationId}"
	caller := &Caller{
		directory: directoryStub{directory: directory},
		contract:  pollContract,
		identity:  local, localURI: "spiffe://liapoldus/service/runtime-main/runtime-1/inc-runtime",
		certificate: tls.Certificate{Certificate: [][]byte{{1}}}, roots: x509.NewCertPool(), dialer: dialer,
		logger: testLogger{},
	}
	response, err := caller.Call(context.Background(), target.InstanceID, "domain.get", json.RawMessage(`{"entity":"Entry","id":"x"}`))
	if err != nil || string(response) != string(session.result.Payload) {
		t.Fatalf("call result=%s err=%v", response, err)
	}
	if dialer.calls != 1 || session.calls != 1 || session.closed != 1 {
		t.Fatalf("dial/call/close counts = %d/%d/%d, want 1/1/1", dialer.calls, session.calls, session.closed)
	}
	if dialer.configuration.Security.PeerIdentity != remoteURI || dialer.configuration.Network.Endpoint != "127.0.0.1:9000" {
		t.Fatalf("dial did not pin resolved target identity/endpoint: %#v", dialer.configuration)
	}

	session.waitForCancel = true
	callContext, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := caller.Call(callContext, target.InstanceID, "domain.get", json.RawMessage(`{"entity":"Entry","id":"x"}`)); err == nil {
		t.Fatal("cancelled service call unexpectedly succeeded")
	}
	if dialer.calls != 2 || session.calls != 2 || session.closed != 2 {
		t.Fatalf("cancelled call was retried or session leaked: dial/call/close=%d/%d/%d", dialer.calls, session.calls, session.closed)
	}
}

func TestCallFailsClosedWithoutCoreAuthorizedTarget(t *testing.T) {
	caller := &Caller{directory: directoryStub{directory: sdkmodels.PeerDirectory{}}, dialer: &fakeDialer{}, logger: testLogger{}}
	if _, err := caller.Call(context.Background(), "not-linked", "domain.get", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unlinked target unexpectedly called")
	}
}

func TestCallCancelsInFlightAuthenticatedWoolRequestWithoutRetry(t *testing.T) {
	now := time.Now().UTC()
	local := sdkmodels.PeerReplicaID{InstanceID: "runtime-main", ReplicaID: "runtime-1", IncarnationID: "inc-runtime", PlacementID: "node-a"}
	target := sdkmodels.PeerReplicaID{InstanceID: "metrics-main", ReplicaID: "metrics-1", IncarnationID: "inc-metrics", PlacementID: "node-b"}
	localURI := "spiffe://liapoldus.test/services/runtime/replica/runtime-1/incarnation/inc-runtime"
	targetURI := "spiffe://liapoldus.test/services/metrics-main/replica/metrics-1/incarnation/inc-metrics"
	localCertificate, targetCertificate, roots := testPeerCertificates(t, localURI, targetURI)
	started := make(chan struct{})
	serverCancelled := make(chan struct{})
	var handled atomic.Int64
	serverHandler, err := protocolpeer.NewRegistry().RegisterCall("metrics.aggregate", func(ctx context.Context, _ protocolpeer.Call) (protocolpeer.Result, error) {
		handled.Add(1)
		close(started)
		<-ctx.Done()
		close(serverCancelled)
		return protocolpeer.Result{}, ctx.Err()
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	server, err := protocolpeer.Listen(protocolpeer.ServerConfig{
		Network:  protocolpeer.NetworkConfig{Carrier: protocolpeer.CarrierTCP, Endpoint: "127.0.0.1:0"},
		Security: protocolpeer.SecurityConfig{Identity: targetURI, Certificate: targetCertificate, Roots: roots},
		Handler:  serverHandler,
	})
	if err != nil {
		t.Fatal(err)
	}
	serveContext, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Sessions(serveContext) }()
	t.Cleanup(func() {
		stopServing()
		if err := server.Close(); err != nil {
			t.Errorf("close mock service server: %v", err)
		}
		<-serveDone
	})
	clientHandler, err := protocolpeer.NewRegistry().Build()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := protocolpeer.Dial(context.Background(), protocolpeer.ClientConfig{
		Network:  protocolpeer.NetworkConfig{Carrier: protocolpeer.CarrierTCP, Endpoint: server.Addr()},
		Security: protocolpeer.SecurityConfig{Identity: localURI, Certificate: localCertificate, Roots: roots, PeerIdentity: targetURI},
		Handler:  clientHandler, Limits: protocolpeer.Limits{MaxMessageBytes: 256 << 10, MaxConcurrentCalls: 1},
	})
	if err != nil {
		t.Fatalf("direct authenticated mock service dial failed: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}

	eligibleUntil := now.Add(8 * time.Second)
	directory := sdkmodels.PeerDirectory{
		ContractVersion: sdkmodels.PeerDirectoryContractVersion,
		Generation:      "directory-cancel",
		IssuedAt:        now.Add(-time.Second),
		ExpiresAt:       now.Add(9 * time.Second),
		Caller:          local,
		Links: []sdkmodels.PeerLink{{
			LinkID: "metrics-link", TargetInstanceID: target.InstanceID,
			PlacementRule: sdkmodels.PeerPlacementRemote, Carrier: sdkmodels.PeerCarrierTCP,
			SecurityProfile: sdkmodels.PeerSecurityMTLS, RequiredPeerContracts: []sdkmodels.ContractRange{},
			Replicas: []sdkmodels.PeerDirectoryReplica{{
				Identity: target, Endpoint: server.Addr(), Eligibility: sdkmodels.PeerEligibilityReady,
				EligibleUntil: &eligibleUntil, Weight: 1, PeerContracts: []sdkmodels.ContractVersion{},
			}},
		}},
	}
	if err := directory.Validate(); err != nil {
		t.Fatalf("mock Core directory is invalid: %v", err)
	}
	var contract sdkinfrastructure.PeerDirectoryPollContract
	contract.Identity.URITemplate = "spiffe://liapoldus.test/services/{instanceId}/replica/{replicaId}/incarnation/{incarnationId}"
	logger := &capturedLogger{}
	caller := &Caller{
		directory: directoryStub{directory: directory}, contract: contract, identity: local, localURI: localURI,
		certificate: localCertificate, roots: roots, dialer: protocolDialer{}, logger: logger,
	}
	callContext, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, callErr := caller.Call(callContext, target.InstanceID, "metrics.aggregate", json.RawMessage(`{"window":"day"}`))
		callDone <- callErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatalf("mock service did not admit the authenticated host call (stage: %#v)", logger.fields)
	}
	cancel()
	select {
	case callErr := <-callDone:
		if callErr == nil {
			t.Fatal("in-flight mock service call succeeded after its caller cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller cancellation did not interrupt the in-flight peer session")
	}
	select {
	case <-serverCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach the mock service handler")
	}
	if got := handled.Load(); got != 1 {
		t.Fatalf("side-effecting service method was replayed: handled %d times", got)
	}
}

func testPeerCertificates(t *testing.T, clientURI, serverURI string) (tls.Certificate, tls.Certificate, *x509.CertPool) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "peercaller-test-root"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(commonName, identityURI string) tls.Certificate {
		t.Helper()
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		parsedURI, parseErr := url.Parse(identityURI)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		leafTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(int64(len(commonName) + 2)), Subject: pkix.Name{CommonName: commonName},
			NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
			KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			URIs:        []*url.URL{parsedURI}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		}
		der, createErr := x509.CreateCertificate(rand.Reader, leafTemplate, root, &key.PublicKey, rootKey)
		if createErr != nil {
			t.Fatal(createErr)
		}
		certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyDER, keyErr := x509.MarshalPKCS8PrivateKey(key)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		certificate, parseErr := tls.X509KeyPair(certificatePEM, keyPEM)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return certificate
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	return issue("runtime-client", clientURI), issue("metrics-service", serverURI), roots
}

func TestCallLogsOnlyAClosedFailureStage(t *testing.T) {
	logger := &capturedLogger{}
	caller := &Caller{directory: directoryStub{directory: sdkmodels.PeerDirectory{}}, logger: logger}
	if _, err := caller.Call(context.Background(), "not-linked", "domain.get", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unlinked target unexpectedly called")
	}
	if logger.message != "service host call unavailable" || len(logger.fields) != 1 || logger.fields[0] != (sdkinterfaces.Field{Key: "stage", Value: "link_missing"}) {
		t.Fatalf("failure log leaked details or omitted stage: message=%q fields=%#v", logger.message, logger.fields)
	}
}

func TestPeerResolutionFailureStagesAreClosed(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "invalid directory", err: sdkmodels.ErrInvalidPeerDirectory, want: "peer_directory_invalid"},
		{name: "expired directory", err: sdkmodels.ErrPeerDirectoryExpired, want: "peer_directory_expired"},
		{name: "not yet valid", err: sdkmodels.ErrPeerDirectoryNotYetValid, want: "peer_directory_not_yet_valid"},
		{name: "missing link", err: sdkmodels.ErrPeerLinkNotFound, want: "peer_link_not_found"},
		{name: "no eligible replica", err: sdkmodels.ErrNoEligiblePeer, want: "no_eligible_peer"},
		{name: "unknown", err: context.DeadlineExceeded, want: "peer_resolution_failed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := peerResolutionFailureStage(test.err); got != test.want {
				t.Fatalf("failure stage = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNoEligiblePeerStageDistinguishesReadinessAndContractMismatch(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	directory := sdkmodels.PeerDirectory{Links: []sdkmodels.PeerLink{{
		LinkID: "domain-link", RequiredPeerContracts: []sdkmodels.ContractRange{{
			ContractID: "service.domain", MinimumVersion: "2.0.0", MaximumVersionExclusive: "3.0.0",
		}}, Replicas: []sdkmodels.PeerDirectoryReplica{{
			Eligibility: sdkmodels.PeerEligibilityNotReady,
		}},
	}}}
	if got := noEligiblePeerFailureStage(directory, "domain-link", now); got != "no_ready_peer" {
		t.Fatalf("not-ready peer failure stage = %q", got)
	}
	directory.Links[0].Replicas[0].Eligibility = sdkmodels.PeerEligibilityReady
	directory.Links[0].Replicas[0].EligibleUntil = &expires
	if got := noEligiblePeerFailureStage(directory, "domain-link", now); got != "peer_contract_mismatch" {
		t.Fatalf("contract mismatch failure stage = %q", got)
	}
}

type testLogger struct{}

func (testLogger) Debug(context.Context, string, ...sdkinterfaces.Field) {}
func (testLogger) Info(context.Context, string, ...sdkinterfaces.Field)  {}
func (testLogger) Warn(context.Context, string, ...sdkinterfaces.Field)  {}
func (testLogger) Error(context.Context, string, ...sdkinterfaces.Field) {}

type capturedLogger struct {
	message string
	fields  []sdkinterfaces.Field
}

func (*capturedLogger) Debug(context.Context, string, ...sdkinterfaces.Field) {}
func (*capturedLogger) Info(context.Context, string, ...sdkinterfaces.Field)  {}
func (logger *capturedLogger) Warn(_ context.Context, message string, fields ...sdkinterfaces.Field) {
	logger.message = message
	logger.fields = append([]sdkinterfaces.Field(nil), fields...)
}
func (*capturedLogger) Error(context.Context, string, ...sdkinterfaces.Field) {}

type directoryStub struct{ directory sdkmodels.PeerDirectory }

func (stub directoryStub) Poll(context.Context, string, int) (sdkinfrastructure.PeerDirectoryPollResult, error) {
	return sdkinfrastructure.PeerDirectoryPollResult{Directory: stub.directory}, nil
}

type fakeDialer struct {
	calls         int
	configuration protocolpeer.ClientConfig
	session       *fakeSession
}

func (dialer *fakeDialer) Dial(_ context.Context, configuration protocolpeer.ClientConfig) (peerSession, error) {
	dialer.calls++
	dialer.configuration = configuration
	return dialer.session, nil
}

type fakeSession struct {
	peer          protocolpeer.PeerIdentity
	result        protocolpeer.Result
	calls         int
	closed        int
	waitForCancel bool
}

func (session *fakeSession) Peer() protocolpeer.PeerIdentity { return session.peer }
func (session *fakeSession) Call(ctx context.Context, _ protocolpeer.Method, _ []byte) (protocolpeer.Result, error) {
	session.calls++
	if session.waitForCancel {
		<-ctx.Done()
		return protocolpeer.Result{}, ctx.Err()
	}
	return session.result, nil
}
func (session *fakeSession) Close() error { session.closed++; return nil }
