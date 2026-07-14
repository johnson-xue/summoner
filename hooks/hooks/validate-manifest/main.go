// validate-manifest — thin CLI wrapper for memory-validator library.
// 校验逻辑全部在 github.com/johnson-xue/memory-validator 库内，本文件只做调用。
package main

import (
	"fmt"
	"os"

	"github.com/johnson-xue/memory-validator"
)

func main() {
	path := "summoner.yaml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "✗ INVALID path=%s\n  - error: file_not_found detail=%v\n", path, err)
		os.Exit(1)
	}
	// summoner 调用不传 --skills-dir（保现有行为：不查 skill 存在性，skills_check=skipped）
	errs, m, ok := memoryvalidator.Validate(path)
	if !ok {
		fmt.Fprintf(os.Stderr, "✗ INVALID path=%s\n", path)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e.Error())
		}
		os.Exit(1)
	}
	fmt.Printf("✓ VALID path=%s project=%s phases=%d workflows=%d skills_check=skipped\n",
		path, m.Project.Name, len(m.Phases), len(m.Workflows))
}
