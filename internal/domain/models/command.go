package models

type Command struct {
	Name        string   `json:"name"`
	Export      string   `json:"export"`
	TenantSites []string `json:"tenantSites"`
	Entities    []string `json:"entities"`
}
