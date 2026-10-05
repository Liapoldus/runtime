package models

type Configuration struct {
	SchemaVersion   string    `json:"schemaVersion"`
	GroupID         string    `json:"groupId"`
	ModelInstanceID string    `json:"modelInstanceId"`
	Module          Module    `json:"module"`
	Commands        []Command `json:"commands"`
}
