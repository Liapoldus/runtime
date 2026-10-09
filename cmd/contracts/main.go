// Command contracts generates or verifies the complete public contract artifact set.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/Liapoldus/runtime/contracts"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	check := flag.Bool("check", false, "verify committed artifacts")
	flag.Parse()
	if err := run(*check); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
}
func run(check bool) error {
	names := make([]string, 0, len(contracts.Documents()))
	for name := range contracts.Documents() {
		names = append(names, name)
	}
	sort.Strings(names)
	entries, err := os.ReadDir("contracts/v1")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, ok := contracts.Documents()[entry.Name()]; !ok {
			return fmt.Errorf("unowned contract artifact: %s", entry.Name())
		}
	}
	for _, name := range names {
		data, err := contracts.Document(name)
		if err != nil {
			return err
		}
		path := filepath.Join("contracts/v1", name)
		if check {
			//nolint:gosec // G304: name comes exclusively from the code-owned contract set.
			old, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(data, old) {
				return fmt.Errorf("stale contract: %s", path)
			}
		} else if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
	}
	return nil
}
