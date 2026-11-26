// Command rbacdoc regenerates docs/RBAC.md from the embedded RBAC matrix.
//
//	go run ./tools/rbacdoc -out ../docs/RBAC.md
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yopsa/pamoja/backend/internal/rbac"
)

func main() {
	out := flag.String("out", filepath.Join("..", "docs", "RBAC.md"), "output path for the generated document")
	flag.Parse()

	matrix, err := rbac.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rbacdoc: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "rbacdoc: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(matrix.GenerateMarkdown()), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "rbacdoc: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d roles, %d grants, %d separation rules)\n",
		*out, len(matrix.Roles), len(matrix.Grants), len(matrix.SoD))
}
