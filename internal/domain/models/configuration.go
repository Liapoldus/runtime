package models

type Configuration struct {
	SchemaVersion string    `json:"schemaVersion"`
	Modules       []Module  `json:"modules"`
	Commands      []Command `json:"commands"`
}
