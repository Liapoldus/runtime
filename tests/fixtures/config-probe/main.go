package main

import (
	"encoding/json"
	"os"

	"github.com/Liapoldus/runtime/internal/application"
	"github.com/Liapoldus/runtime/internal/domain/models"
)

func main() {
	var config models.Configuration
	if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
		os.Exit(2)
	}
	code := application.ValidateConfiguration(config)
	response := struct {
		Valid bool   `json:"valid"`
		Error string `json:"error,omitempty"`
	}{Valid: code == "", Error: code}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(2)
	}
}
