// validate-manifest — thin CLI wrapper for memory-validator library.
// 校验逻辑全部在 github.com/johnson-xue/memory-validator 库内，本文件只做调用。
//
// skills_check：默认 skipped（运行时兼容——项目 manifest 里 `superpowers:xxx`
// 这类插件限定 skill 无法在单一 skills 目录下做存在性判定）。传入
// --skills-dir <dir> 时点亮存在性校验（CI / 本地项目自检用）；插件限定名
// （含 ":"）的 skill_not_found 会被过滤——它们经插件市场解析，不走文件系统。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/johnson-xue/memory-validator"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run parses args and validates; returns the process exit code. Extracted for
// table-testing the CLI surface (review found flag parsing was unguarded).
func run(args []string) int {
	path := "summoner.yaml"
	skillsDir := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--skills-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "✗ --skills-dir requires a value")
				return 2
			}
			i++
			skillsDir = args[i]
		case strings.HasPrefix(args[i], "-"):
			// Unknown flags are usage errors, not manifest paths (review:
			// --help used to be treated as a path → misleading file_not_found).
			fmt.Fprintf(os.Stderr, "✗ unknown flag: %s (usage: validate-manifest [path] [--skills-dir <dir>])\n", args[i])
			return 2
		default:
			path = args[i]
		}
	}

	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "✗ INVALID path=%s\n  - error: file_not_found detail=%v\n", path, err)
		return 1
	}

	var opts []memoryvalidator.Option
	if skillsDir != "" {
		opts = append(opts, memoryvalidator.WithSkillsDir(skillsDir))
	}
	errs, m, ok := memoryvalidator.Validate(path, opts...)
	if skillsDir != "" {
		errs, ok = filterPluginQualified(errs)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "✗ INVALID path=%s\n", path)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e.Error())
		}
		return 1
	}
	check := "skipped"
	if skillsDir != "" {
		check = "on dir=" + skillsDir
	}
	fmt.Printf("✓ VALID path=%s project=%s phases=%d workflows=%d skills_check=%s\n",
		path, m.Project.Name, len(m.Phases), len(m.Workflows), check)
	return 0
}

// filterPluginQualified 过滤插件限定 skill（"plugin:skill" 形态）的
// skill_not_found——这类 skill 经 Claude Code 插件运行时解析，不以
// <skillsDir>/<skill> 路径存在，判 FAIL 是误报。本地 skill 名不含 ":"。
func filterPluginQualified(errs []memoryvalidator.ValidationError) ([]memoryvalidator.ValidationError, bool) {
	out := make([]memoryvalidator.ValidationError, 0, len(errs))
	for _, e := range errs {
		if e.Code == "skill_not_found" && strings.Contains(e.Fields["skill"], ":") {
			continue
		}
		out = append(out, e)
	}
	return out, len(out) == 0
}
