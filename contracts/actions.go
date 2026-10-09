package contracts

type AdminAction struct {
	Kind           string `json:"kind"`
	Capability     string `json:"-"`
	SurfaceBinding struct {
		PageID    string `json:"page"`
		SectionID string `json:"section"`
		ActionID  string `json:"action,omitempty"`
	} `json:"surfaceBinding"`
	HTTPStatus         int `json:"httpStatus"`
	AcceptedHTTPStatus int `json:"acceptedHttpStatus"`
	ArchiveLimits      struct {
		MediaType     string `json:"mediaType"`
		ArtifactBytes int64  `json:"artifactBytes"`
		MetadataBytes int64  `json:"metadataBytes"`
	} `json:"archiveLimits"`
	Errors map[string]AdminActionProblem `json:"errors"`
}

type AdminActionProblem struct {
	HTTP int    `json:"http"`
	Code string `json:"code"`
}

type ModuleAdminActions struct {
	List    AdminAction
	Publish AdminAction
}

const (
	ModuleListCapability    string = "runtime.modules.list"
	ModulePublishCapability string = "runtime.modules.publish"
	moduleQueryKind         string = "query"
	moduleArtifactKind      string = "artifact-action"
	modulePageID            string = "modules"
	moduleSectionID         string = "modules"
	modulePublishActionID   string = "publish"
	moduleMediaType         string = "application/wasm"
	moduleArtifactBytes     int64  = 16777216
	moduleMetadataBytes     int64  = 65536
)

// ModuleAdminActionDefinitions constructs independent runtime definitions.
// The public action document is generated from these runtime definitions.
func ModuleAdminActionDefinitions() ModuleAdminActions {
	result := ModuleAdminActions{}
	result.List.Kind = moduleQueryKind
	result.List.Capability = ModuleListCapability
	result.List.SurfaceBinding.PageID = modulePageID
	result.List.SurfaceBinding.SectionID = moduleSectionID
	result.List.HTTPStatus = 200
	result.List.Errors = map[string]AdminActionProblem{
		CodeInvalidInput: {HTTP: 400, Code: CodeInvalidInput},
		CodeUnavailable:  {HTTP: 503, Code: CodeUnavailable},
	}
	result.Publish.Kind = moduleArtifactKind
	result.Publish.Capability = ModulePublishCapability
	result.Publish.SurfaceBinding.PageID = modulePageID
	result.Publish.SurfaceBinding.SectionID = moduleSectionID
	result.Publish.SurfaceBinding.ActionID = modulePublishActionID
	result.Publish.AcceptedHTTPStatus = 202
	result.Publish.ArchiveLimits.MediaType = moduleMediaType
	result.Publish.ArchiveLimits.ArtifactBytes = moduleArtifactBytes
	result.Publish.ArchiveLimits.MetadataBytes = moduleMetadataBytes
	result.Publish.Errors = map[string]AdminActionProblem{
		CodeInvalidInput:     {HTTP: 400, Code: CodeInvalidInput},
		CodeDigestMismatch:   {HTTP: 409, Code: CodeDigestMismatch},
		CodeInvalidModule:    {HTTP: 422, Code: CodeInvalidModule},
		CodeArtifactTooLarge: {HTTP: 413, Code: CodeArtifactTooLarge},
		CodeUnavailable:      {HTTP: 503, Code: CodeUnavailable},
	}
	return result
}
