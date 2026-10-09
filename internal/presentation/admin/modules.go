// Package admin adapts Runtime module administration to the Plugin SDK.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	sdkpresentation "github.com/Liapoldus/plugin-sdk/presentation"
	"github.com/Liapoldus/runtime/contracts"
	runtimemodule "github.com/Liapoldus/runtime/internal/application/module"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

var errInvalidContract = errors.New("invalid admin contract")

type moduleArtifactMetadata struct {
	SHA256 string `json:"sha256"`
}

type moduleListResponse struct {
	Items      []models.ModuleArtifact `json:"items"`
	NextCursor *string                 `json:"nextCursor"`
}

type actionProblem struct {
	Code string `json:"code"`
}

type ModuleActions struct {
	service  runtimemodule.ModuleArtifacts
	contract contracts.ModuleAdminActions
}

func NewModuleActions(service runtimemodule.ModuleArtifacts, contract contracts.ModuleAdminActions) ModuleActions {
	return ModuleActions{service: service, contract: contract}
}

func (adapter ModuleActions) AcceptArtifact(ctx context.Context, input sdkpresentation.ArtifactInput) (sdkpresentation.ArtifactResponse, error) {
	action := adapter.contract.Publish
	if input.Invocation.Validate() != nil || input.Invocation.PageID != action.SurfaceBinding.PageID ||
		input.Invocation.ActionID != action.SurfaceBinding.ActionID || input.Body == nil ||
		input.ContentType != action.ArchiveLimits.MediaType || int64(len(input.Metadata)) > action.ArchiveLimits.MetadataBytes {
		return adapter.problem(action, contracts.CodeInvalidInput)
	}
	var metadata moduleArtifactMetadata
	decoder := json.NewDecoder(bytes.NewReader(input.Metadata))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&metadata) != nil || decoder.Decode(new(any)) != io.EOF || len(metadata.SHA256) != 64 {
		return adapter.problem(action, contracts.CodeInvalidInput)
	}
	if err := adapter.service.Publish(ctx, metadata.SHA256, input.Body); err != nil {
		switch {
		case errors.Is(err, models.ErrInvalidArtifactDigest):
			return adapter.problem(action, contracts.CodeInvalidInput)
		case errors.Is(err, models.ErrArtifactDigestMismatch):
			return adapter.problem(action, contracts.CodeDigestMismatch)
		case errors.Is(err, models.ErrInvalidArtifactModule):
			return adapter.problem(action, contracts.CodeInvalidModule)
		case errors.Is(err, models.ErrArtifactTooLarge):
			return adapter.problem(action, contracts.CodeArtifactTooLarge)
		default:
			return adapter.problem(action, contracts.CodeUnavailable)
		}
	}
	body, err := json.Marshal(moduleArtifactMetadata{SHA256: metadata.SHA256})
	if err != nil {
		return sdkpresentation.ArtifactResponse{}, err
	}
	return sdkpresentation.ArtifactResponse{StatusCode: action.AcceptedHTTPStatus, Body: body}, nil
}

func (adapter ModuleActions) HandleAdminAction(ctx context.Context, input sdkpresentation.AdminActionInput) (sdkpresentation.AdminActionResponse, error) {
	action := adapter.contract.List
	if input.Invocation.Validate() != nil || input.Invocation.PageID != action.SurfaceBinding.PageID ||
		input.Invocation.ActionID != action.Capability ||
		len(input.Body) != 0 && !bytes.Equal(bytes.TrimSpace(input.Body), []byte("{}")) {
		return adapter.adminProblem(action, contracts.CodeInvalidInput)
	}
	items, err := adapter.service.List(ctx)
	if err != nil {
		return adapter.adminProblem(action, contracts.CodeUnavailable)
	}
	body, err := json.Marshal(moduleListResponse{Items: items})
	if err != nil {
		return sdkpresentation.AdminActionResponse{}, err
	}
	return sdkpresentation.AdminActionResponse{StatusCode: action.HTTPStatus, Body: body}, nil
}

func (adapter ModuleActions) problem(action contracts.AdminAction, category string) (sdkpresentation.ArtifactResponse, error) {
	problem, ok := action.Errors[category]
	if !ok {
		return sdkpresentation.ArtifactResponse{}, errInvalidContract
	}
	body, err := json.Marshal(actionProblem{Code: problem.Code})
	if err != nil {
		return sdkpresentation.ArtifactResponse{}, err
	}
	return sdkpresentation.ArtifactResponse{StatusCode: problem.HTTP, Body: body}, nil
}

func (adapter ModuleActions) adminProblem(action contracts.AdminAction, category string) (sdkpresentation.AdminActionResponse, error) {
	problem, ok := action.Errors[category]
	if !ok {
		return sdkpresentation.AdminActionResponse{}, errInvalidContract
	}
	body, err := json.Marshal(actionProblem{Code: problem.Code})
	if err != nil {
		return sdkpresentation.AdminActionResponse{}, err
	}
	return sdkpresentation.AdminActionResponse{StatusCode: problem.HTTP, Body: body}, nil
}

var _ sdkpresentation.ArtifactAcceptor = ModuleActions{}
var _ sdkpresentation.AdminActionHandler = ModuleActions{}
