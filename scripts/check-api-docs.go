// scripts/check-api-docs.go
// Lint: fail if docs/api-reference.md is out of sync with cmd/gateway/main.go.
//
//go:build ignore
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func projectRoot() string {
	if dir := os.Getenv("PROJECT_ROOT"); dir != "" {
		return dir
	}
	exeDir := filepath.Dir(os.Args[0])
	if strings.Contains(exeDir, "go-build") || strings.Contains(exeDir, "/tmp/") {
		wd, err := os.Getwd()
		if err == nil {
			return wd
		}
	}
	root, err := filepath.Abs(filepath.Join(exeDir, ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: finding project root: %v\n", err)
		os.Exit(1)
	}
	return root
}

func main() {
	projectRoot := projectRoot()
	apiDoc := filepath.Join(projectRoot, "docs", "api-reference.md")
	backup := apiDoc + ".bak"

	// Read original content
	original, err := os.ReadFile(apiDoc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: reading %s: %v\n", apiDoc, err)
		os.Exit(1)
	}

	// Write backup
	if err := os.WriteFile(backup, original, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: writing backup: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(backup)

	// Run generator
	genPath := filepath.Join(projectRoot, "scripts", "generate-api-docs.go")
	cmd := exec.Command("go", "run", genPath)
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: running generator: %s\n", string(out))
		os.Exit(1)
	}

	// Read generated content
	generated, err := os.ReadFile(apiDoc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: reading generated %s: %v\n", apiDoc, err)
		os.Exit(1)
	}

	// Restore original
	if err := os.WriteFile(apiDoc, original, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: restoring original: %v\n", err)
		os.Exit(1)
	}

	// Compare
	if string(original) != string(generated) {
		fmt.Fprintf(os.Stderr, "ERROR: %s is out of sync with cmd/gateway/main.go\n", apiDoc)
		fmt.Fprintf(os.Stderr, "Run 'make docs-generate' to regenerate.\n")
		// Show diff
		diff := exec.Command("diff", "-u", backup, apiDoc+".gen")
		os.WriteFile(apiDoc+".gen", generated, 0644)
		defer os.Remove(apiDoc + ".gen")
		out, _ := diff.CombinedOutput()
		fmt.Fprintf(os.Stderr, "%s\n", string(out))
		os.Exit(1)
	}

	fmt.Println("docs/api-reference.md is up to date.")
}
