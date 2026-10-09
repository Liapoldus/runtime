package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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

	"github.com/Liapoldus/plugin-sdk/application"
	"github.com/Liapoldus/plugin-sdk/domain/interfaces"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	"github.com/Liapoldus/plugin-sdk/infrastructure"
	"github.com/Liapoldus/plugin-sdk/presentation"
	protocolpeer "github.com/Liapoldus/pluginprotocol/v2/presentation/peer"
	"github.com/Liapoldus/runtime/contracts"
	runtimeconfig "github.com/Liapoldus/runtime/internal/application/config"
	runtimemodule "github.com/Liapoldus/runtime/internal/application/module"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
	"github.com/Liapoldus/runtime/internal/presentation/admin"
	"github.com/Liapoldus/runtime/internal/presentation/peerplugin"
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
}

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
	metadata, err := contracts.PluginDocuments()
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
	runtimeInvoker := runtimemodule.NewInvoker(store)
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
	peerHandler, err := peerplugin.New(runtimeInvoker, callerAuthorizer{uri: configuration.peerCaller})
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
	if err := writeReady(stdout, listener.Addr().String(), peerServer.Addr()); err != nil {
		return errors.Join(errInvalidBootstrap, listener.Close(), peerServer.Close())
	}
	select {
	case <-serveContext.Done():
	case err := <-serveResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return errInvalidBootstrap
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Duration(contract.Deadlines.PluginShutdownGraceSeconds)*time.Second)
	defer cancel()
	runtimeInvoker.BeginDrain()
	closeErr := peerServer.Close()
	drainResults := make(chan error, 2)
	go func() { drainResults <- server.GracefulShutdown(shutdownContext) }()
	go func() { drainResults <- runtimeInvoker.WaitForDrain(shutdownContext) }()
	drainErr := errors.Join(<-drainResults, <-drainResults)
	client.CloseIdleConnections()
	credentials.Zeroize()
	return errors.Join(closeErr, drainErr)
}

func parseOptions(arguments []string) (options, error) {
	var result options
	flags := flag.NewFlagSet("runtime", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&result.listenAddress, "listen", "", "Plugin SDK HTTPS bind address")
	flags.StringVar(&result.coreURL, "core-url", "", "Core control-plane HTTPS origin")
	flags.StringVar(&result.coreServer, "core-server-name", "", "Core TLS server name")
	flags.StringVar(&result.instanceID, "instance-id", "", "registered Runtime instance identity")
	flags.StringVar(&result.replicaID, "replica-id", "", "registered Runtime replica identity")
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
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 ||
		result.listenAddress == "" || result.coreURL == "" || result.coreServer == "" ||
		result.instanceID == "" || result.replicaID == "" || result.corePeerCN == "" ||
		result.caFile == "" || result.serverCert == "" || result.serverKey == "" ||
		result.clientCert == "" || result.clientKey == "" || result.crlFile == "" || !filepath.IsAbs(result.artifactDir) ||
		result.peerListen == "" || result.peerIdentity == "" || result.peerCaller == "" ||
		result.peerCAFile == "" || result.peerCert == "" || result.peerKey == "" ||
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
	routes := make(map[string]presentation.Endpoint, len(contract.Plugin.Endpoints))
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
	plugin := contract.Plugin
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
		ConfigSchemaEndpoint: routes["configSchema"], HealthEndpoint: routes["health"],
		ReadyEndpoint: routes["ready"], ReloadEndpoint: routes["reload"],
		ArtifactStreamEndpoint: routes["artifactStream"], AdminSurfaceEndpoint: routes["adminSurface"],
		AdminActionEndpoint: routes["adminAction"], MetricsEndpoint: routes["metrics"],
		HealthStatus: plugin.Responses.Health.Status, HealthBody: plugin.Responses.Health.Body,
		ContentTypes:          presentation.ContentTypes{JSON: plugin.Responses.ContentTypes.JSON, Metrics: plugin.Responses.ContentTypes.Metrics},
		ReloadRequest:         mapDocument(plugin.ReloadRequest),
		ReloadAcknowledgement: mapDocument(plugin.ReloadAcknowledgement),
		Readiness:             mapDocument(plugin.Readiness), Manifest: mapDocument(plugin.Manifest),
		ConfigurationSchema: mapDocument(plugin.ConfigurationSchema),
		AdminSurface:        mapDocument(plugin.AdminSurface),
		Registration: mapDocument(infrastructure.DocumentContract{
			MediaType:    contract.Identity.Registration.MediaType,
			MaximumBytes: contract.Identity.Registration.MaximumBytes,
			Required:     contract.Identity.Registration.Required,
		}),
		ArtifactStream: presentation.ArtifactStreamContract{
			MediaType: plugin.ArtifactStream.MediaType, MetadataMediaType: plugin.ArtifactStream.MetadataMediaType,
			Parts: plugin.ArtifactStream.Parts, PartOrder: plugin.ArtifactStream.PartOrder,
			MaximumArtifactBytes:          plugin.ArtifactStream.MaximumArtifactBytes,
			MinimumArtifactBytes:          plugin.ArtifactStream.MinimumArtifactBytes,
			MaximumMetadataBytes:          plugin.ArtifactStream.MaximumMetadataBytes,
			MaximumMultipartOverheadBytes: plugin.ArtifactStream.MaximumMultipartOverheadBytes,
			MaximumRequestBytes:           plugin.ArtifactStream.MaximumRequestBytes,
			MaximumReceiptBytes:           plugin.ArtifactStream.MaximumReceiptBytes,
			AcceptedStatus:                plugin.ArtifactStream.AcceptedStatus,
			FilenameForwarded:             plugin.ArtifactStream.FilenameForwarded,
			Deadline:                      seconds(contract.Deadlines.ArtifactStreamSeconds),
			InvocationContext: presentation.ArtifactInvocationContract{
				MaximumBytes: plugin.ArtifactStream.InvocationContext.MaximumBytes,
				Required:     plugin.ArtifactStream.InvocationContext.Required,
				Optional:     plugin.ArtifactStream.InvocationContext.Optional,
				Headers:      plugin.ArtifactStream.InvocationContext.Headers,
			},
		},
		AdminAction: presentation.AdminActionContract{
			MediaType:            plugin.AdminAction.MediaType,
			MaximumRequestBytes:  plugin.AdminAction.MaximumRequestBytes,
			MaximumResponseBytes: plugin.AdminAction.MaximumResponseBytes,
			MaximumPageIDBytes:   plugin.AdminAction.MaximumPageIDBytes,
			MaximumActionIDBytes: plugin.AdminAction.MaximumActionIDBytes,
			PathSegmentPattern:   plugin.AdminAction.PathSegmentPattern,
			ResponseStatus: presentation.StatusRangeContract{
				Minimum: plugin.AdminAction.ResponseStatus.Minimum,
				Maximum: plugin.AdminAction.ResponseStatus.Maximum,
			},
			Deadline: seconds(plugin.AdminAction.DeadlineSeconds),
			InvocationContext: presentation.AdminInvocationContract{
				MaximumBytes:        plugin.AdminAction.InvocationContext.MaximumBytes,
				UnknownHeaderPrefix: plugin.AdminAction.InvocationContext.UnknownHeaderPrefix,
				Required:            plugin.AdminAction.InvocationContext.Required,
				Optional:            plugin.AdminAction.InvocationContext.Optional,
				Headers:             plugin.AdminAction.InvocationContext.Headers,
			},
		},
		MaximumMetadataBytes: plugin.MaximumMetadataBytes,
		ReadinessDeadline:    seconds(contract.Deadlines.PluginReadinessSeconds),
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
	module, err := applier.modules.Get(ctx, settings.Module.SHA256)
	if err != nil {
		return err
	}
	defer clear(module)
	return applier.store.Activate(ctx, configuration.Generation, configuration.SchemaVersion, configuration.SHA256, configuration.Bytes(), module)
}

type registrationProvider struct{ lifecycle *application.Lifecycle }

func (provider registrationProvider) Registration(contractVersion string) (sdkmodels.Registration, error) {
	return provider.lifecycle.Registration(contractVersion)
}

type metadataProvider struct{ documents contracts.PluginMetadata }

func (provider metadataProvider) Manifest(context.Context) ([]byte, error) {
	return append([]byte(nil), provider.documents.Manifest...), nil
}

func (provider metadataProvider) ConfigurationSchema(context.Context) ([]byte, error) {
	return append([]byte(nil), provider.documents.ConfigurationSchema...), nil
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
var _ interfaces.PluginMetadata = metadataProvider{}
var _ presentation.AdminSurfaceProvider = metadataProvider{}
