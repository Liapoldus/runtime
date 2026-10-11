// Package peercaller resolves Core-authorized plugin links and makes one
// authenticated pluginprotocol call without retrying the operation.
package peercaller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	sdkapplication "github.com/Liapoldus/plugin-sdk/v2/application"
	sdkinterfaces "github.com/Liapoldus/plugin-sdk/v2/domain/interfaces"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
	protocolmodel "github.com/Liapoldus/pluginprotocol/v3/domain/peer"
	protocolpeer "github.com/Liapoldus/pluginprotocol/v3/presentation/peer"
)

var ErrCallUnavailable = errors.New("configured plugin call is unavailable")

type directory interface {
	Poll(context.Context, string, int) (sdkinfrastructure.PeerDirectoryPollResult, error)
}

type peerSession interface {
	Peer() protocolpeer.PeerIdentity
	Call(context.Context, protocolpeer.Method, []byte) (protocolpeer.Result, error)
	Close() error
}

type sessionDialer interface {
	Dial(context.Context, protocolpeer.ClientConfig) (peerSession, error)
}

type Caller struct {
	directory   directory
	contract    sdkinfrastructure.PeerDirectoryPollContract
	identity    sdkmodels.PeerReplicaID
	localURI    string
	certificate tls.Certificate
	roots       *x509.CertPool
	logger      sdkinterfaces.Logger
	ordinal     atomic.Uint64
	dialer      sessionDialer
}

func New(
	client *sdkinfrastructure.PeerDirectoryClient,
	contract sdkinfrastructure.PeerDirectoryPollContract,
	identity sdkmodels.PeerReplicaID,
	localURI string,
	certificate tls.Certificate,
	roots *x509.CertPool,
	logger sdkinterfaces.Logger,
) (*Caller, error) {
	if client == nil || contract.Validate() != nil || !identity.Valid() || localURI == "" || len(certificate.Certificate) == 0 || roots == nil || logger == nil {
		return nil, ErrCallUnavailable
	}
	return &Caller{directory: client, contract: contract, identity: identity, localURI: localURI, certificate: certificate, roots: roots, logger: logger, dialer: protocolDialer{}}, nil
}

func (caller *Caller) Call(ctx context.Context, target, method string, payload json.RawMessage) (json.RawMessage, error) {
	if caller == nil || ctx == nil || target == "" || method == "" || !json.Valid(payload) {
		return nil, ErrCallUnavailable
	}
	directoryResult, err := caller.directory.Poll(ctx, "", 0)
	if err != nil || directoryResult.NotModified {
		return nil, caller.fail(ctx, "directory_poll")
	}
	var linkID string
	for _, link := range directoryResult.Directory.Links {
		if link.TargetInstanceID != target {
			continue
		}
		if linkID != "" {
			return nil, caller.fail(ctx, "ambiguous_link")
		}
		linkID = link.LinkID
	}
	if linkID == "" {
		return nil, caller.fail(ctx, "link_missing")
	}
	ordinal := caller.ordinal.Add(1) - 1
	resolved, err := sdkapplication.ResolvePeer(directoryResult.Directory, sdkmodels.PeerResolution{
		LinkID: linkID, Now: time.Now().UTC(), SelectionOrdinal: &ordinal,
	})
	if err != nil {
		stage := peerResolutionFailureStage(err)
		if errors.Is(err, sdkmodels.ErrNoEligiblePeer) {
			stage = noEligiblePeerFailureStage(directoryResult.Directory, linkID, time.Now().UTC())
		}
		return nil, caller.fail(ctx, stage)
	}
	if !time.Now().Before(resolved.EligibleUntil) {
		return nil, caller.fail(ctx, "peer_expired")
	}
	remoteURI, err := peerIdentityURI(caller.contract.Identity.URITemplate, sdkmodels.PeerReplicaID{
		InstanceID: resolved.TargetInstanceID, ReplicaID: resolved.ReplicaID,
		IncarnationID: resolved.IncarnationID, PlacementID: resolved.PlacementID,
	})
	if err != nil {
		return nil, caller.fail(ctx, "peer_identity")
	}
	session, err := caller.dialer.Dial(ctx, protocolpeer.ClientConfig{
		Network: protocolpeer.NetworkConfig{Carrier: protocolpeer.Carrier(resolved.Carrier), Endpoint: resolved.Endpoint},
		Security: protocolpeer.SecurityConfig{
			Identity: caller.localURI, Certificate: caller.certificate, Roots: caller.roots, PeerIdentity: remoteURI,
		},
		Handler: denyHandler{},
		Limits:  protocolpeer.Limits{MaxMessageBytes: 256 << 10, MaxConcurrentCalls: 1},
	})
	if err != nil {
		return nil, caller.fail(ctx, "peer_dial")
	}
	if session.Peer().URI != remoteURI || !time.Now().Before(resolved.EligibleUntil) {
		if err := session.Close(); err != nil {
			return nil, caller.fail(ctx, "peer_close")
		}
		return nil, caller.fail(ctx, "peer_identity_or_expiry")
	}
	result, err := session.Call(ctx, protocolpeer.Method(method), payload)
	closeErr := session.Close()
	if err != nil || closeErr != nil || len(result.Payload) == 0 || !json.Valid(result.Payload) {
		return nil, caller.fail(ctx, "peer_call_or_response")
	}
	return append(json.RawMessage(nil), result.Payload...), nil
}

func noEligiblePeerFailureStage(directory sdkmodels.PeerDirectory, linkID string, now time.Time) string {
	for _, link := range directory.Links {
		if link.LinkID != linkID {
			continue
		}
		ready := 0
		for _, replica := range link.Replicas {
			if replica.Eligibility != sdkmodels.PeerEligibilityReady || replica.EligibleUntil == nil || !replica.EligibleUntil.After(now) {
				continue
			}
			ready++
			compatible := true
			for _, required := range link.RequiredPeerContracts {
				found := false
				for _, advertised := range replica.PeerContracts {
					if advertised.ContractID == required.ContractID && required.Contains(advertised.Version) {
						found = true
						break
					}
				}
				if !found {
					compatible = false
					break
				}
			}
			if compatible {
				return "peer_resolution_inconsistent"
			}
		}
		if ready == 0 {
			return "no_ready_peer"
		}
		return "peer_contract_mismatch"
	}
	return "peer_link_not_found"
}

func peerResolutionFailureStage(err error) string {
	switch {
	case errors.Is(err, sdkmodels.ErrInvalidPeerDirectory):
		return "peer_directory_invalid"
	case errors.Is(err, sdkmodels.ErrInvalidPeerResolution):
		return "peer_resolution_invalid"
	case errors.Is(err, sdkmodels.ErrPeerDirectoryExpired):
		return "peer_directory_expired"
	case errors.Is(err, sdkmodels.ErrPeerDirectoryNotYetValid):
		return "peer_directory_not_yet_valid"
	case errors.Is(err, sdkmodels.ErrPeerLinkNotFound):
		return "peer_link_not_found"
	case errors.Is(err, sdkmodels.ErrNoEligiblePeer):
		return "no_eligible_peer"
	default:
		return "peer_resolution_failed"
	}
}

// fail emits only a closed, non-sensitive failure stage. Peer addresses,
// payloads, identities, and transport error strings are deliberately omitted.
func (caller *Caller) fail(ctx context.Context, stage string) error {
	if caller != nil && caller.logger != nil {
		caller.logger.Warn(ctx, "plugin host call unavailable", sdkinterfaces.Field{Key: "stage", Value: stage})
	}
	return ErrCallUnavailable
}

type protocolDialer struct{}

func (protocolDialer) Dial(ctx context.Context, configuration protocolpeer.ClientConfig) (peerSession, error) {
	return protocolpeer.Dial(ctx, configuration)
}

func peerIdentityURI(template string, identity sdkmodels.PeerReplicaID) (string, error) {
	if !identity.Valid() || !strings.Contains(template, "{instanceId}") || !strings.Contains(template, "{replicaId}") || !strings.Contains(template, "{incarnationId}") {
		return "", ErrCallUnavailable
	}
	value := strings.NewReplacer(
		"{instanceId}", url.PathEscape(identity.InstanceID),
		"{replicaId}", url.PathEscape(identity.ReplicaID),
		"{incarnationId}", url.PathEscape(identity.IncarnationID),
	).Replace(template)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || strings.Contains(value, "{") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrCallUnavailable
	}
	return value, nil
}

type denyHandler struct{}

func (denyHandler) PrepareCall(context.Context, protocolmodel.PeerIdentity, protocolmodel.Call) (protocolmodel.CallHandler, error) {
	return nil, protocolpeer.ErrUnauthorized
}

func (denyHandler) PrepareStream(protocolmodel.Stream) (protocolmodel.StreamHandler, error) {
	return nil, protocolpeer.ErrUnauthorized
}

var _ interface {
	Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
} = (*Caller)(nil)
