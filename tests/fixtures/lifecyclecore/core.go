// Package lifecyclecore provides a small mTLS fixture endpoint for the SDK
// replica lifecycle contract.
package lifecyclecore

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Liapoldus/plugin-sdk/v2/domain/models"
	"github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

// Core is a small mTLS fixture endpoint for the SDK replica lifecycle contract.
type Core struct {
	contract  infrastructure.ReplicaLifecycleContract
	mu        sync.Mutex
	active    map[models.PeerReplicaID]models.ReplicaRegistrationRequest
	failRenew bool
}

func New(contract infrastructure.ReplicaLifecycleContract) *Core {
	return &Core{contract: contract, active: make(map[models.PeerReplicaID]models.ReplicaRegistrationRequest)}
}

// RefuseRenewals simulates a Core lifecycle refusal after a replica has started.
func (core *Core) RefuseRenewals() {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.failRenew = true
}

func (core *Core) Registered(identity models.PeerReplicaID) bool {
	core.mu.Lock()
	defer core.mu.Unlock()
	_, exists := core.active[identity]
	return exists
}

func (core *Core) Register(mux *http.ServeMux) {
	for operation, endpoint := range core.contract.Endpoints {
		mux.HandleFunc(endpoint.Method+" "+endpoint.Path, core.handle(operation))
	}
}

func (core *Core) handle(operation string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", core.contract.Requests[operation].MediaType)
		writer.Header().Set("Cache-Control", "no-store")
		switch operation {
		case "register":
			var registration models.ReplicaRegistrationRequest
			if !decode(request, &registration) || registration.Validate() != nil || !core.authenticated(request, registration.Identity) {
				http.Error(writer, "invalid registration", http.StatusBadRequest)
				return
			}
			core.mu.Lock()
			core.active[registration.Identity] = registration
			core.mu.Unlock()
			now := time.Now().UTC()
			writeJSON(writer, core.contract.Responses[operation].Status, models.ReplicaRegistrationResponse{
				ContractVersion: core.contract.ContractVersion, Identity: registration.Identity,
				ServerTime: now, LeaseExpiresAt: now.Add(time.Duration(core.contract.Lease.TTLSeconds) * time.Second),
			})
		case "renew":
			var renewal models.ReplicaRenewalRequest
			if !decode(request, &renewal) || renewal.Validate() != nil || !core.authenticated(request, renewal.Identity) {
				http.Error(writer, "invalid renewal", http.StatusBadRequest)
				return
			}
			core.mu.Lock()
			if core.failRenew {
				core.mu.Unlock()
				problem := core.contract.Problems["unavailable"]
				writer.Header().Set("Content-Type", core.contract.ProblemMediaType)
				writeJSON(writer, problem.Status, map[string]string{"code": problem.Code})
				return
			}
			registration, exists := core.active[renewal.Identity]
			if exists {
				registration.AppliedGeneration, registration.Ready = renewal.AppliedGeneration, renewal.Ready
				core.active[renewal.Identity] = registration
			}
			core.mu.Unlock()
			if !exists {
				http.NotFound(writer, request)
				return
			}
			writeLease(writer, core.contract, operation, renewal.Identity)
		case "deregister":
			var deregistration models.ReplicaDeregistrationRequest
			if !decode(request, &deregistration) || deregistration.Validate() != nil || !core.authenticated(request, deregistration.Identity) {
				http.Error(writer, "invalid deregistration", http.StatusBadRequest)
				return
			}
			core.mu.Lock()
			_, exists := core.active[deregistration.Identity]
			delete(core.active, deregistration.Identity)
			core.mu.Unlock()
			if !exists {
				http.NotFound(writer, request)
				return
			}
			writeJSON(writer, core.contract.Responses[operation].Status, models.ReplicaDeregistrationResponse{
				ContractVersion: core.contract.ContractVersion, Identity: deregistration.Identity, Deregistered: true,
			})
		}
	}
}

func (core *Core) authenticated(request *http.Request, identity models.PeerReplicaID) bool {
	if request.TLS == nil || len(request.TLS.PeerCertificates) != 1 {
		return false
	}
	want, err := core.contract.ReplicaIdentityURI(identity)
	if err != nil {
		return false
	}
	for _, uri := range request.TLS.PeerCertificates[0].URIs {
		if uri.String() == want {
			return true
		}
	}
	return false
}

func writeLease(writer http.ResponseWriter, contract infrastructure.ReplicaLifecycleContract, operation string, identity models.PeerReplicaID) {
	now := time.Now().UTC()
	writeJSON(writer, contract.Responses[operation].Status, models.ReplicaLease{
		ContractVersion: contract.ContractVersion, Identity: identity,
		ServerTime: now, LeaseExpiresAt: now.Add(time.Duration(contract.Lease.TTLSeconds) * time.Second),
	})
}

func decode(request *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		return false
	}
	var extra any
	return decoder.Decode(&extra) != nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, "fixture response encoding failed", http.StatusInternalServerError)
		return
	}
	writer.WriteHeader(status)
	if _, err := writer.Write(append(body, '\n')); err != nil {
		return
	}
}
