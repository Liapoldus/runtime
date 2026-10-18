package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/runtime/contracts"
	runtimeconfig "github.com/Liapoldus/runtime/internal/application/config"
	runtimemodule "github.com/Liapoldus/runtime/internal/application/module"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
	"github.com/Liapoldus/runtime/internal/infrastructure/peercaller"
	"github.com/Liapoldus/runtime/internal/presentation/admin"
	"github.com/Liapoldus/runtime/internal/presentation/peerservice"
	"github.com/Liapoldus/service-sdk/v2/application"
	"github.com/Liapoldus/service-sdk/v2/domain/interfaces"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
	"github.com/Liapoldus/service-sdk/v2/infrastructure"
	"github.com/Liapoldus/service-sdk/v2/presentation"
	protocolpeer "github.com/Liapoldus/wool/v3/presentation/peer"
)

const (
	messageInvalidBootstrap string = "invalid Runtime bootstrap options"
)

var errInvalidBootstrap = errors.New(messageInvalidBootstrap)

type options struct {
	listenAddress string
	coreURL       string
	coreServer    string
	instanceID    string
	replicaID     string
	placementID   string
	incarnationID string
	corePeerCN    string
	caFile        string
	serverCert    string
	serverKey     string
	clientCert    string
	clientKey     string
	crlFile       string
	artifactDir   string
	peerListen    string
	peerIdentity  string
	peerCaller    string
	peerCAFile    string
	peerCert      string
	peerKey       string
	peerCarrier   string
	restAdvertise string
	peerAdvertise string
}

// releaseVersion is replaced by release builds using -ldflags.
var releaseVersion = "0.0.0-dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if _, err := fmt.Fprintln(os.Stderr, "Runtime process stopped"); err != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	configuration, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	contract, err := infrastructure.LoadHTTPContract()
	if err != nil {
		return errInvalidBootstrap
	}
	metadata, err := contracts.RuntimeDocuments()
	if err != nil {
		return errInvalidBootstrap
	}
	identity, err := sdkmodels.NewReplicaIdentity(configuration.instanceID, configuration.replicaID)
	if err != nil {
		return errInvalidBootstrap
	}
	peer, err := sdkmodels.NewPeerIdentity(configuration.corePeerCN, "")
	if err != nil {
		return errInvalidBootstrap
	}
	credentials, err := infrastructure.LoadCredentials(contract, infrastructure.CredentialsMaterial{
		CAFile:                configuration.caFile,
		ServerCertificateFile: configuration.serverCert,
		ServerKeyFile:         configuration.serverKey,
		ClientCertificateFile: configuration.clientCert,
		ClientKeyFile:         configuration.clientKey,
	})
	if err != nil {
		return infrastructure.ErrInvalidCredentials
	}
	provider, err := infrastructure.NewStaticCredentialsProvider(credentials)
	if err != nil {
		return infrastructure.ErrInvalidCredentials
	}
	revocation, err := infrastructure.NewRevocation(infrastructure.RevocationConfiguration{
		Authorities: credentials.TrustAuthorities(),
		Files:       []string{configuration.crlFile},
	})
	if err != nil {
		return infrastructure.ErrInvalidRevocationSet
	}
	client, err := infrastructure.NewMutualTLSClient(contract, provider, infrastructure.MutualTLSClientConfig{
		Peer:       peer,
		ServerName: configuration.coreServer,
		Revocation: revocation,
	})
	if err != nil {
		return infrastructure.ErrInvalidClientTLS
	}
	source, err := infrastructure.NewCoreConfigurationSource(contract, configuration.coreURL, client)
	if err != nil {
		return infrastructure.ErrInvalidControlCall
	}
	collector, err := infrastructure.NewObserverPrometheusCollector(contract)
	if err != nil {
		return infrastructure.ErrInvalidObserver
	}
	logger, err := infrastructure.NewJSONLogger(contract, stderr)
	if err != nil {
		return infrastructure.ErrInvalidLogger
	}
	maximumKindLength := len(string(application.KindReload))
	for _, kind := range []application.Kind{application.KindSecretGrant, application.KindSecretRedemption} {
		if len(string(kind)) > maximumKindLength {
			maximumKindLength = len(string(kind))
		}
	}
	recorder, err := application.NewRecorder(application.RecorderConfiguration{
		Sink: collectorMetricsSink{collector: collector},
		AllowedKinds: []application.Kind{
			application.KindReload,
			application.KindSecretGrant,
			application.KindSecretRedemption,
		},
		MaximumKindLength: maximumKindLength,
	})
	if err != nil {
		return errInvalidBootstrap
	}
	loggingObserver, err := application.NewLoggingObserver(application.LoggingObserverConfiguration{
		Logger:              logger,
		RedactedPlaceholder: contract.Logging.RedactedPlaceholder,
		MaximumFields:       contract.Logging.MaximumFields,
		MaximumKeyLength:    contract.Logging.MaximumKeyLength,
		MaximumValueLength:  contract.Logging.MaximumValueLength,
	})
	if err != nil {
		return errInvalidBootstrap
	}
	observer := application.Observers{recorder, loggingObserver}
	store := &runtimeconfig.ConfigurationStore{}
	moduleStore := artifacts.Store{Root: configuration.artifactDir}
	moduleActionsContract := contracts.ModuleAdminActionDefinitions()
	moduleActions := admin.NewModuleActions(runtimemodule.NewModuleArtifacts(moduleStore), moduleActionsContract)
	lifecycle, err := application.NewLifecycle(application.LifecycleConfiguration{
		Source:   source,
		Applier:  configurationApplier{store: store, modules: moduleStore},
		Identity: identity,
		Observer: observer,
	})
	if err != nil {
		return errInvalidBootstrap
	}
	peerCertificate, err := tls.LoadX509KeyPair(configuration.peerCert, configuration.peerKey)
	if err != nil {
		return errInvalidBootstrap
	}
	peerRootsPEM, err := os.ReadFile(configuration.peerCAFile)
	if err != nil {
		return errInvalidBootstrap
	}
	peerRoots := x509.NewCertPool()
	if !peerRoots.AppendCertsFromPEM(peerRootsPEM) {
		return errInvalidBootstrap
	}
	directoryContract, err := infrastructure.LoadPeerDirectoryPollContract()
	if err != nil {
		return errInvalidBootstrap
	}
	peerCertificatePEM, err := os.ReadFile(configuration.peerCert)
	if err != nil {
		return errInvalidBootstrap
	}
	defer clear(peerCertificatePEM)
	peerKeyPEM, err := os.ReadFile(configuration.peerKey)
	if err != nil {
		return errInvalidBootstrap
	}
	defer clear(peerKeyPEM)
	directoryCredentials, err := infrastructure.LoadCredentials(contract, infrastructure.CredentialsMaterial{
		CAFile:                configuration.caFile,
		ServerCertificateFile: configuration.serverCert,
		ServerKeyFile:         configuration.serverKey,
		ClientCertificatePEM:  peerCertificatePEM,
		ClientKeyPEM:          peerKeyPEM,
	})
	if err != nil {
		return infrastructure.ErrInvalidCredentials
	}
	directoryProvider, err := infrastructure.NewStaticCredentialsProvider(directoryCredentials)
	if err != nil {
		return infrastructure.ErrInvalidCredentials
	}
	defer directoryProvider.Zeroize()
	directoryTransport, err := infrastructure.NewMutualTLSClient(contract, directoryProvider, infrastructure.MutualTLSClientConfig{
		Peer: peer, ServerName: configuration.coreServer, Revocation: revocation,
	})
	if err != nil {
		return infrastructure.ErrInvalidClientTLS
	}
	directoryIdentity := sdkmodels.PeerReplicaID{
		InstanceID: configuration.instanceID, ReplicaID: configuration.replicaID,
		IncarnationID: configuration.incarnationID, PlacementID: configuration.placementID,
	}
	replicaContract, err := infrastructure.LoadReplicaLifecycleContract()
	if err != nil {
		return errInvalidBootstrap
	}
	replicaClient, err := infrastructure.NewReplicaLifecycleClient(replicaContract, configuration.coreURL, directoryTransport, directoryIdentity)
	if err != nil {
		return errInvalidBootstrap
	}
	directoryClient, err := infrastructure.NewPeerDirectoryClient(directoryContract, configuration.coreURL, directoryTransport, directoryIdentity)
	if err != nil {
		return errInvalidBootstrap
	}
	outboundCaller, err := peercaller.New(directoryClient, directoryContract, directoryIdentity, configuration.peerIdentity, peerCertificate, peerRoots, logger)
	if err != nil {
		return errInvalidBootstrap
	}
	runtimeInvoker := runtimemodule.NewInvokerWithHostCaller(store, outboundCaller)
	presentationContract, err := mapPresentationContract(contract)
	if err != nil {
		return errInvalidBootstrap
	}
	handlers, err := presentation.NewHandlerSet(presentation.HandlerConfiguration{
		Contracts: presentationContract,
		Lifecycle: lifecycle,
		Readiness: presentation.ReadinessProviderFunc(func(context.Context) sdkmodels.Readiness {
			readiness := lifecycle.Readiness()
			if !runtimeInvoker.Accepting() {
				readiness.Ready = false
			}
			return readiness
		}),
		Registration: registrationProvider{lifecycle: lifecycle},
		Metadata:     metadataProvider{documents: metadata},
		Metrics:      collector,
		Artifacts:    moduleActions,
		AdminSurface: metadataProvider{documents: metadata},
		AdminActions: moduleActions,
	})
	if err != nil {
		return errInvalidBootstrap
	}
	server, err := infrastructure.NewMutualTLSServer(contract, infrastructure.MutualTLSServerConfig{
		Handler:    handlers.Handler(),
		Provider:   provider,
		Peer:       peer,
		Revocation: revocation,
		ErrorLog:   log.New(io.Discard, "", 0),
	})
	if err != nil {
		return infrastructure.ErrInvalidServerTLS
	}
	peerHandler, err := peerservice.New(runtimeInvoker, callerAuthorizer{uri: configuration.peerCaller})
	if err != nil {
		return errInvalidBootstrap
	}
	peerServer, err := protocolpeer.Listen(protocolpeer.ServerConfig{
		Network: protocolpeer.NetworkConfig{
			Carrier: protocolpeer.Carrier(configuration.peerCarrier), Endpoint: configuration.peerListen,
		},
		Security: protocolpeer.SecurityConfig{
			Identity: configuration.peerIdentity, Certificate: peerCertificate, Roots: peerRoots,
		},
		Handler: peerHandler,
	})
	if err != nil {
		return errInvalidBootstrap
	}
	listener, err := new(net.ListenConfig).Listen(context.Background(), "tcp", configuration.listenAddress)
	if err != nil {
		return errors.Join(errInvalidBootstrap, peerServer.Close())
	}
	serveContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serveResult := make(chan error, 2)
	go func() { serveResult <- server.Serve(listener) }()
	go func() { serveResult <- peerServer.Sessions(serveContext) }()
	releaseDigest, err := executableDigest()
	if err != nil {
		return errors.Join(errInvalidBootstrap, listener.Close(), peerServer.Close())
	}
	readiness := lifecycle.Readiness()
	registration := sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: replicaContract.ContractVersion,
		Identity:        directoryIdentity,
		RestEndpoint:    configuration.restAdvertise,
		PeerEndpoints: []sdkmodels.ReplicaPeerEndpoint{{
			Carrier: sdkmodels.PeerCarrier(configuration.peerCarrier), Endpoint: configuration.peerAdvertise,
			SecurityProfile: sdkmodels.PeerSecurityMTLS,
		}},
		Release:             sdkmodels.ReplicaRelease{Version: releaseVersion, SHA256: releaseDigest},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   readiness.Generation,
		Ready:               readiness.Ready && readiness.Generation != "" && runtimeInvoker.Accepting(),
	}
	leaseResult := make(chan error, 1)
	go func() {
		leaseResult <- infrastructure.MaintainReplicaLease(serveContext, infrastructure.ReplicaLeaseMaintenance{
			Client: replicaClient, Registration: registration, Readiness: func() sdkmodels.Readiness {
				current := lifecycle.Readiness()
				current.Ready = current.Ready && current.Generation != "" && runtimeInvoker.Accepting()
				return current
			},
			RenewInterval: time.Duration(replicaContract.Lease.RenewIntervalSeconds) * time.Second,
		})
	}()
	if err := writeReady(stdout, listener.Addr().String(), peerServer.Addr()); err != nil {
		return errors.Join(errInvalidBootstrap, listener.Close(), peerServer.Close())
	}
	var leaseErr error
	var runtimeErr error
	select {
	case <-serveContext.Done():
	case err := <-serveResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			runtimeErr = errInvalidBootstrap
			stop()
		}
	case err := <-leaseResult:
		if err != nil {
			leaseErr = errInvalidBootstrap
			runtimeInvoker.BeginDrain()
			stop()
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Duration(contract.Deadlines.ServiceShutdownGraceSeconds)*time.Second)
	defer cancel()
	runtimeInvoker.BeginDrain()
	closeErr := peerServer.Close()
	drainResults := make(chan error, 2)
	go func() { drainResults <- server.GracefulShutdown(shutdownContext) }()
	go func() { drainResults <- runtimeInvoker.WaitForDrain(shutdownContext) }()
	drainErr := errors.Join(<-drainResults, <-drainResults)
	deregisterContext, deregisterCancel := context.WithTimeout(context.Background(), time.Duration(replicaContract.Deadlines.DeregisterSeconds)*time.Second)
	_, deregisterErr := replicaClient.Deregister(deregisterContext, sdkmodels.ReplicaDeregistrationRequest{
		ContractVersion: replicaContract.ContractVersion, Identity: directoryIdentity,
	})
	deregisterCancel()
	client.CloseIdleConnections()
	directoryTransport.CloseIdleConnections()
	directoryProvider.Zeroize()
	credentials.Zeroize()
	return errors.Join(closeErr, drainErr, deregisterErr, leaseErr, runtimeErr)
}

func executableDigest() (resultDigest string, resultErr error) {
	path, err := os.Executable()
	if err != nil {
		return "", errInvalidBootstrap
	}
	// #nosec G304 -- os.Executable supplies the path of this running process.
	file, err := os.Open(path)
	if err != nil {
		return "", errInvalidBootstrap
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, errInvalidBootstrap)
		}
	}()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", errInvalidBootstrap
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func parseOptions(arguments []string) (options, error) {
	var result options
	flags := flag.NewFlagSet("runtime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&result.listenAddress, "listen", "", "Service SDK HTTPS bind address")
	flags.StringVar(&result.coreURL, "core-url", "", "Core control-plane HTTPS origin")
	flags.StringVar(&result.coreServer, "core-server-name", "", "Core TLS server name")
	flags.StringVar(&result.instanceID, "instance-id", "", "registered Runtime instance identity")
	flags.StringVar(&result.replicaID, "replica-id", "", "registered Runtime replica identity")
	flags.StringVar(&result.placementID, "placement-id", "", "registered Runtime placement identity")
	flags.StringVar(&result.incarnationID, "incarnation-id", "", "current Runtime process incarnation identity")
	flags.StringVar(&result.corePeerCN, "core-peer-cn", "", "pinned Core certificate common name")
	flags.StringVar(&result.caFile, "tls-ca-file", "", "operator trust roots PEM file")
	flags.StringVar(&result.serverCert, "tls-server-cert-file", "", "Runtime HTTPS certificate PEM file")
	flags.StringVar(&result.serverKey, "tls-server-key-file", "", "Runtime HTTPS private key PEM file")
	flags.StringVar(&result.clientCert, "tls-client-cert-file", "", "Runtime Core-client certificate PEM file")
	flags.StringVar(&result.clientKey, "tls-client-key-file", "", "Runtime Core-client private key PEM file")
	flags.StringVar(&result.crlFile, "tls-crl-file", "", "operator-signed CRL bundle PEM file")
	flags.StringVar(&result.artifactDir, "artifact-dir", "", "absolute directory containing content-addressed Runtime modules")
	flags.StringVar(&result.peerListen, "peer-listen", "", "Peer protocol bind address")
	flags.StringVar(&result.peerIdentity, "peer-identity", "", "URI SAN the Runtime authenticates as")
	flags.StringVar(&result.peerCaller, "peer-allowed-caller", "", "URI SAN the single allowed invoker must present")
	flags.StringVar(&result.peerCAFile, "peer-ca-file", "", "Peer trust roots PEM file")
	flags.StringVar(&result.peerCert, "peer-cert", "", "Peer certificate PEM file")
	flags.StringVar(&result.peerKey, "peer-key", "", "Peer private key PEM file")
	flags.StringVar(&result.peerCarrier, "peer-carrier", "tcp", "Peer carrier tcp or quic")
	flags.StringVar(&result.restAdvertise, "rest-advertise-url", "", "Core-visible HTTPS endpoint for this Runtime replica")
	flags.StringVar(&result.peerAdvertise, "peer-advertise-endpoint", "", "Core-visible peer carrier host:port for this Runtime replica")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		result.listenAddress == "" || result.coreURL == "" || result.coreServer == "" ||
		result.instanceID == "" || result.replicaID == "" || result.placementID == "" || result.incarnationID == "" || result.corePeerCN == "" ||
		result.caFile == "" || result.serverCert == "" || result.serverKey == "" ||
		result.clientCert == "" || result.clientKey == "" || result.crlFile == "" || !filepath.IsAbs(result.artifactDir) ||
		result.peerListen == "" || result.peerIdentity == "" || result.peerCaller == "" ||
		result.peerCAFile == "" || result.peerCert == "" || result.peerKey == "" ||
		result.restAdvertise == "" || result.peerAdvertise == "" ||
		(result.peerCarrier != "tcp" && result.peerCarrier != "quic") {
		return options{}, errInvalidBootstrap
	}
	return result, nil
}

func writeReady(writer io.Writer, address, peerAddress string) error {
	if strings.TrimSpace(address) == "" || strings.TrimSpace(peerAddress) == "" {
		return errInvalidBootstrap
	}
	_, err := fmt.Fprintf(writer, "{\"listenAddress\":%q,\"peerListenAddress\":%q}\n", address, peerAddress)
	return err
}

func mapPresentationContract(contract infrastructure.HTTPContract) (presentation.Contracts, error) {
	routes := make(map[string]presentation.Endpoint, len(contract.Service.Endpoints))
	for _, name := range contract.EndpointNames() {
		endpoint, err := contract.Endpoint(name)
		if err != nil {
			return presentation.Contracts{}, errInvalidBootstrap
		}
		routes[name] = presentation.Endpoint{Method: endpoint.Method, Path: endpoint.Path}
	}
	problems := make(map[string]presentation.Problem, len(contract.Problems))
	for key, problem := range contract.Problems {
		problems[key] = presentation.Problem{Status: problem.Status, Code: problem.Code}
	}
	errorsByKey := make(map[string]presentation.Problem, len(contract.Errors))
	for key, problem := range contract.Errors {
		errorsByKey[key] = presentation.Problem{Status: problem.Status, Code: problem.Code}
	}
	service := contract.Service
	mapDocument := func(document infrastructure.DocumentContract) presentation.DocumentContract {
		return presentation.DocumentContract{
			MediaType: document.MediaType, MaximumBytes: document.MaximumBytes,
			Required: document.Required, DigestAlgorithm: document.DigestAlgorithm,
		}
	}
	seconds := func(value int) time.Duration { return time.Duration(value) * time.Second }
	result := presentation.Contracts{
		ContractVersion:  contract.ContractVersion,
		IdentityEndpoint: routes["identity"], ManifestEndpoint: routes["manifest"],
		ConfigSchemaEndpoint: routes["configSchema"], ConfigValidationEndpoint: routes["configValidate"], HealthEndpoint: routes["health"],
		ConfigurationValidationGenerationHeader: contract.Service.ConfigurationValidationGenerationHeader,
		ReadyEndpoint:                           routes["ready"], ReloadEndpoint: routes["reload"],
		ArtifactStreamEndpoint: routes["artifactStream"], AdminSurfaceEndpoint: routes["adminSurface"],
		AdminActionEndpoint: routes["adminAction"], MetricsEndpoint: routes["metrics"],
		HealthStatus: service.Responses.Health.Status, HealthBody: service.Responses.Health.Body,
		ContentTypes:          presentation.ContentTypes{JSON: service.Responses.ContentTypes.JSON, Metrics: service.Responses.ContentTypes.Metrics},
		ReloadRequest:         mapDocument(service.ReloadRequest),
		ReloadAcknowledgement: mapDocument(service.ReloadAcknowledgement),
		Readiness:             mapDocument(service.Readiness), Manifest: mapDocument(service.Manifest),
		ConfigurationSchema: mapDocument(service.ConfigurationSchema),
		AdminSurface:        mapDocument(service.AdminSurface),
		Registration: mapDocument(infrastructure.DocumentContract{
			MediaType:    contract.Identity.Registration.MediaType,
			MaximumBytes: contract.Identity.Registration.MaximumBytes,
			Required:     contract.Identity.Registration.Required,
		}),
		ArtifactStream: presentation.ArtifactStreamContract{
			MediaType: service.ArtifactStream.MediaType, MetadataMediaType: service.ArtifactStream.MetadataMediaType,
			Parts: service.ArtifactStream.Parts, PartOrder: service.ArtifactStream.PartOrder,
			MaximumArtifactBytes:          service.ArtifactStream.MaximumArtifactBytes,
			MinimumArtifactBytes:          service.ArtifactStream.MinimumArtifactBytes,
			MaximumMetadataBytes:          service.ArtifactStream.MaximumMetadataBytes,
			MaximumMultipartOverheadBytes: service.ArtifactStream.MaximumMultipartOverheadBytes,
			MaximumRequestBytes:           service.ArtifactStream.MaximumRequestBytes,
			MaximumReceiptBytes:           service.ArtifactStream.MaximumReceiptBytes,
			AcceptedStatus:                service.ArtifactStream.AcceptedStatus,
			FilenameForwarded:             service.ArtifactStream.FilenameForwarded,
			Deadline:                      seconds(contract.Deadlines.ArtifactStreamSeconds),
			InvocationContext: presentation.ArtifactInvocationContract{
				MaximumBytes: service.ArtifactStream.InvocationContext.MaximumBytes,
				Required:     service.ArtifactStream.InvocationContext.Required,
				Optional:     service.ArtifactStream.InvocationContext.Optional,
				Headers:      service.ArtifactStream.InvocationContext.Headers,
			},
		},
		AdminAction: presentation.AdminActionContract{
			MediaType:            service.AdminAction.MediaType,
			MaximumRequestBytes:  service.AdminAction.MaximumRequestBytes,
			MaximumResponseBytes: service.AdminAction.MaximumResponseBytes,
			MaximumPageIDBytes:   service.AdminAction.MaximumPageIDBytes,
			MaximumActionIDBytes: service.AdminAction.MaximumActionIDBytes,
			PathSegmentPattern:   service.AdminAction.PathSegmentPattern,
			ResponseStatus: presentation.StatusRangeContract{
				Minimum: service.AdminAction.ResponseStatus.Minimum,
				Maximum: service.AdminAction.ResponseStatus.Maximum,
			},
			Deadline: seconds(service.AdminAction.DeadlineSeconds),
			InvocationContext: presentation.AdminInvocationContract{
				MaximumBytes:        service.AdminAction.InvocationContext.MaximumBytes,
				UnknownHeaderPrefix: service.AdminAction.InvocationContext.UnknownHeaderPrefix,
				Required:            service.AdminAction.InvocationContext.Required,
				Optional:            service.AdminAction.InvocationContext.Optional,
				Headers:             service.AdminAction.InvocationContext.Headers,
			},
		},
		MaximumMetadataBytes: service.MaximumMetadataBytes,
		ReadinessDeadline:    seconds(contract.Deadlines.ServiceReadinessSeconds),
		Problems:             problems, Errors: errorsByKey,
		OutcomeProblems: contract.OutcomeProblems, SuccessOutcomes: contract.SuccessOutcomes,
	}
	if err := result.Validate(); err != nil {
		return presentation.Contracts{}, errInvalidBootstrap
	}
	return result, nil
}

type configurationApplier struct {
	store   *runtimeconfig.ConfigurationStore
	modules artifacts.Store
}

func (applier configurationApplier) Apply(ctx context.Context, configuration sdkmodels.Configuration) error {
	settings, code := runtimeconfig.DecodeConfiguration(configuration.Bytes())
	if code != "" {
		return runtimeconfig.ErrConfigurationRejected
	}
	modules := make(map[string][]byte, len(settings.Modules))
	exportsByModule := make(map[string][]string, len(settings.Modules))
	for _, command := range settings.Commands {
		exportsByModule[command.ModuleID] = append(exportsByModule[command.ModuleID], command.Export)
	}
	var loadedBytes int64
	defer func() {
		for _, module := range modules {
			clear(module)
		}
	}()
	for _, declaration := range settings.Modules {
		module, err := applier.modules.GetWithExports(ctx, declaration.SHA256, exportsByModule[declaration.ID])
		if err != nil {
			return err
		}
		loadedBytes += int64(len(module))
		if loadedBytes > int64(contracts.WasmABIDefinition().Limits.MaxGenerationArtifactBytes) {
			clear(module)
			return runtimeconfig.ErrConfigurationRejected
		}
		modules[declaration.ID] = module
	}
	return applier.store.Activate(ctx, configuration.Generation, configuration.SchemaVersion, configuration.SHA256, configuration.Bytes(), modules)
}

type registrationProvider struct{ lifecycle *application.Lifecycle }

func (provider registrationProvider) Registration(contractVersion string) (sdkmodels.Registration, error) {
	return provider.lifecycle.Registration(contractVersion)
}

type metadataProvider struct{ documents contracts.RuntimeMetadata }

func (provider metadataProvider) Manifest(context.Context) ([]byte, error) {
	return append([]byte(nil), provider.documents.Manifest...), nil
}

func (provider metadataProvider) ConfigurationSchema(context.Context) ([]byte, error) {
	return append([]byte(nil), provider.documents.ConfigurationSchema...), nil
}

func (provider metadataProvider) ValidateConfiguration(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, code := runtimeconfig.DecodeConfiguration(raw); code != "" {
		return runtimeconfig.ErrConfigurationRejected
	}
	return nil
}

func (provider metadataProvider) AdminSurface(context.Context) ([]byte, error) {
	return append([]byte(nil), provider.documents.AdminSurface...), nil
}

type collectorMetricsSink struct {
	collector *infrastructure.ObserverPrometheusCollector
}

func (sink collectorMetricsSink) Lifecycle(kind string, outcome sdkmodels.Outcome) {
	sink.collector.Observe(context.Background(), kind, outcome)
}

func (sink collectorMetricsSink) PullFailure(sdkmodels.Outcome) {
	sink.collector.RecordConfigPullFailure()
}

func (sink collectorMetricsSink) SetReady(ready bool) { sink.collector.SetReady(ready) }

// callerAuthorizer pins the peer protocol to exactly one authenticated invoker.
// Stream capability is closed at the transport so the Runtime only ever serves
// bounded unary calls.
type callerAuthorizer struct{ uri string }

func (authorizer callerAuthorizer) AuthorizeCall(from protocolpeer.PeerIdentity, _ protocolpeer.Method) error {
	if from.URI != authorizer.uri {
		return protocolpeer.ErrUnauthorized
	}
	return nil
}

func (callerAuthorizer) AuthorizeStream(protocolpeer.PeerIdentity, protocolpeer.Method) error {
	return protocolpeer.ErrUnauthorized
}

var _ interfaces.ConfigurationApplier = configurationApplier{}
var _ interfaces.ServiceMetadata = metadataProvider{}
var _ presentation.AdminSurfaceProvider = metadataProvider{}
