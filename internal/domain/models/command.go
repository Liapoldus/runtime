// Package models defines Runtime configuration and artifact values.
package models

type Command struct {
	Name     string         `json:"name"`
	ModuleID string         `json:"moduleId"`
	Export   string         `json:"export"`
	Entities []string       `json:"entities"`
	Calls    []PluginMethod `json:"calls,omitempty"`
}

type PluginMethod struct {
	Plugin string `json:"plugin"`
	Method string `json:"method"`
}
