// validate-manifest — Go 重写的 summoner.yaml 校验器。
// 用 yaml.v3 结构化解析（消灭 grep fallback 的脆弱子串匹配），新增 chain→phase 引用校验（缺陷 7）。
package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Version string `yaml:"version"`
	Project struct {
		Name string `yaml:"name"`
	} `yaml:"project"`
	Phases map[string]struct {
		Skill string `yaml:"skill"`
	} `yaml:"phases"`
	Workflows map[string]struct {
		Chain       []string `yaml:"chain"`
		Checkpoints string   `yaml:"checkpoints"`
	} `yaml:"workflows"`
}

// Validate 解析 manifest 并校验。返回错误列表与是否通过。
func Validate(path string) (errors []string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("无法读取 %s: %v", path, err)}, false
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return []string{fmt.Sprintf("YAML 解析失败: %v", err)}, false
	}
	// 基本字段
	if m.Version != "1" {
		errors = append(errors, fmt.Sprintf("version 必须为 \"1\", 得到 %q", m.Version))
	}
	if m.Project.Name == "" {
		errors = append(errors, "project.name 不能为空")
	}
	if len(m.Phases) == 0 {
		errors = append(errors, "phases 至少声明一个")
	}
	// 缺陷 7 核心：chain 引用的 phase 必须在 phases 声明
	for wfName, wf := range m.Workflows {
		for _, p := range wf.Chain {
			if _, declared := m.Phases[p]; !declared {
				errors = append(errors, fmt.Sprintf("workflow %q 的 chain 引用未声明的 phase %q（summoner 会静默 fallback 到 superpowers 默认，丢项目上下文）", wfName, p))
			}
		}
		if wf.Checkpoints == "" {
			errors = append(errors, fmt.Sprintf("workflow %q 缺少 checkpoints 字段", wfName))
		}
	}
	return errors, len(errors) == 0
}

func main() {
	path := "summoner.yaml"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s 不存在\n", path)
		os.Exit(1)
	}
	errs, ok := Validate(path)
	if !ok {
		fmt.Fprintf(os.Stderr, "✗ %s 校验失败:\n", path)
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}
	// 重新解析用于输出摘要
	var m Manifest
	if data, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(data, &m)
	}
	fmt.Printf("✓ %s 校验通过 (project: %s, phases: %d, workflows: %d)\n",
		path, m.Project.Name, len(m.Phases), len(m.Workflows))
}
