// Package models defines Runtime configuration and artifact values.
package models

type Command struct {
	Name     string         `json:"name"`
	ModuleID string         `json:"moduleId"`
	Export   string         `json:"export"`
	Entities []string       `json:"entities"`
	Calls    []ServiceCall `json:"calls,omitempty"`
}

type ServiceCall struct {
	Service string `json:"service"`
	Method  string `json:"method"`
}
