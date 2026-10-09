package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkpresentation "github.com/Liapoldus/plugin-sdk/presentation"
	"github.com/Liapoldus/runtime/contracts"
	runtimemodule "github.com/Liapoldus/runtime/internal/application/module"
	"github.com/Liapoldus/runtime/internal/infrastructure/artifacts"
	"github.com/Liapoldus/runtime/internal/presentation/admin"
)

type result struct {
	Accepted struct {
		Status int            `json:"status"`
		Body   map[string]any `json:"body"`
	} `json:"accepted"`
	Listed struct {
		Status int            `json:"status"`
		Body   map[string]any `json:"body"`
	} `json:"listed"`
}

func main() {
	if err := run(); err != nil {
		if _, err := fmt.Fprintln(os.Stderr, "module artifact action fixture failed"); err != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("invalid fixture arguments")
	}
	module, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	actions := contracts.ModuleAdminActionDefinitions()
	store := artifacts.Store{Root: os.Args[1]}
	adapter := admin.NewModuleActions(runtimemodule.NewModuleArtifacts(store), actions)
	invocation := sdkmodels.ArtifactInvocation{
		CallerID: "fixture-caller", InstanceID: "runtime-fixture", PageID: actions.Publish.SurfaceBinding.PageID,
		ActionID: actions.Publish.SurfaceBinding.ActionID, SurfaceDigest: "fixture-surface-digest",
		IdempotencyKey: "fixture-operation", RequestID: "fixture-request",
	}
	metadata, err := json.Marshal(map[string]string{"sha256": os.Args[2]})
	if err != nil {
		return err
	}
	accepted, err := adapter.AcceptArtifact(context.Background(), sdkpresentation.ArtifactInput{
		Invocation: invocation, Metadata: metadata, ContentType: actions.Publish.ArchiveLimits.MediaType, Body: bytes.NewReader(module),
	})
	if err != nil {
		return err
	}
	var acceptedBody map[string]any
	if err := json.Unmarshal(accepted.Body, &acceptedBody); err != nil {
		return err
	}
	admin := sdkmodels.AdminActionInvocation{
		CallerID: "fixture-caller", InstanceID: "runtime-fixture", PageID: actions.List.SurfaceBinding.PageID,
		ActionID: actions.List.Capability, SurfaceDigest: "fixture-surface-digest", RequestID: "fixture-request",
	}
	listed, err := adapter.HandleAdminAction(context.Background(), sdkpresentation.AdminActionInput{Invocation: admin, Body: []byte(`{}`)})
	if err != nil {
		return err
	}
	var listedBody map[string]any
	if err := json.Unmarshal(listed.Body, &listedBody); err != nil {
		return err
	}
	output := result{}
	output.Accepted.Status = accepted.StatusCode
	output.Accepted.Body = acceptedBody
	output.Listed.Status = listed.StatusCode
	output.Listed.Body = listedBody
	return json.NewEncoder(os.Stdout).Encode(output)
}
