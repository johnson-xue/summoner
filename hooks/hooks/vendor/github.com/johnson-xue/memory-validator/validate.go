// Package memoryvalidator 校验 summoner.yaml manifest。
// 两段式解析：Stage1 用 yaml.Node 查重名（值类型形式，勿用 *yaml.Node 零值），
// Stage2 decode 到 Manifest struct 走其余校验。
package memoryvalidator

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	Version   string `yaml:"version"`
	Project   struct{ Name string `yaml:"name"` } `yaml:"project"`
	Phases    map[string]Phase    `yaml:"phases"`
	Workflows map[string]Workflow `yaml:"workflows"`
}

type Phase struct {
	Skill string `yaml:"skill"`
}

type Workflow struct {
	Chain       []string `yaml:"chain"`
	Checkpoints string   `yaml:"checkpoints"`
}

type Option func(*config)

type config struct {
	skillsDir string
}

func WithSkillsDir(dir string) Option {
	return func(c *config) { c.skillsDir = dir }
}

type ValidationError struct {
	Code   string
	Line   int
	Column int
	Fields map[string]string
}

// Error 返回 AI 友好键值串："<code> <k>=<v> ... [line=N [column=M]]"
func (e ValidationError) Error() string {
	var b strings.Builder
	b.WriteString(e.Code)
	// 字段按 key 字典序输出（稳定契约，map 无序故显式排序）
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(" ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(e.Fields[k])
	}
	if e.Line > 0 {
		fmt.Fprintf(&b, " line=%d", e.Line)
		if e.Column > 0 {
			fmt.Fprintf(&b, " column=%d", e.Column)
		}
	}
	return b.String()
}

// Validate 解析 manifest 并校验。返回错误列表、解析成功的 Manifest、是否通过。
func Validate(path string, opts ...Option) (errors []ValidationError, m Manifest, ok bool) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []ValidationError{{Code: "parse_error", Line: 0, Fields: map[string]string{"detail": err.Error()}}}, Manifest{}, false
	}
	// Stage 1: 值类型 yaml.Node（勿用 *yaml.Node 零值——nil 指针传 &root 得 Kind=0 空节点）
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return []ValidationError{{Code: "parse_error", Line: 0, Fields: map[string]string{"detail": err.Error()}}}, Manifest{}, false
	}
	// 重名检测：Stage1 检出则直接返回，跳过 Stage2（避免 Decode 重复报错）
	if dupErrs := detectDuplicateKeys(&root); len(dupErrs) > 0 {
		return dupErrs, Manifest{}, false
	}
	// Stage 2: decode 到 struct
	if err := root.Decode(&m); err != nil {
		return []ValidationError{{Code: "parse_error", Line: 0, Fields: map[string]string{"detail": err.Error()}}}, Manifest{}, false
	}
	// 基本字段 + workflow 枚举/非空 + chain 引用 + skill 存在性
	errors = validateFields(&m, &root, cfg)
	return errors, m, len(errors) == 0
}

// detectDuplicateKeys 递归遍历所有 MappingNode 检测重复键。
// root 是 DocumentNode，root.Content[0] 是顶层 MappingNode。
// path 是当前 MappingNode 的点分路径（如 "phases.fix"）；顶层为 ""。
// kind 保留策略：path 第一段为 phases/workflows 时设 kind=该段名（backward-compat）。
func detectDuplicateKeys(root *yaml.Node) []ValidationError {
	var errs []ValidationError
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return errs
	}
	detectDupMapping(root.Content[0], "", &errs)
	return errs
}

// detectDupMapping 递归扫 MappingNode，对每个 Mapping 的键做重复检测。
func detectDupMapping(n *yaml.Node, path string, errs *[]ValidationError) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	seen := map[string]int{} // key -> first line
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if prev, exists := seen[k.Value]; exists {
			fields := map[string]string{
				"key":       k.Value,
				"prev_line": fmt.Sprintf("%d", prev),
			}
			if path != "" {
				fields["path"] = path
			}
			// kind 保留：path 第一段是 phases/workflows 时设
			if seg := topSegment(path); seg == "phases" || seg == "workflows" {
				fields["kind"] = seg
			}
			*errs = append(*errs, ValidationError{
				Code:   "duplicate_key",
				Line:   k.Line,
				Column: k.Column,
				Fields: fields,
			})
		} else {
			seen[k.Value] = k.Line
		}
		// 递归进子节点（val）
		childPath := path
		if childPath != "" {
			childPath += "."
		}
		childPath += k.Value
		detectDupMapping(v, childPath, errs)
	}
}

// topSegment 返回点分路径的第一段（"" 表示顶层）。
func topSegment(path string) string {
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			return path[:i]
		}
	}
	return path
}

// validateFields 走 struct 级校验（重名已在 Stage1 查过，这里不重复）。
func validateFields(m *Manifest, root *yaml.Node, cfg *config) []ValidationError {
	var errs []ValidationError
	if m.Version != "1" {
		errs = append(errs, ValidationError{Code: "invalid_version", Fields: map[string]string{"expected": "1", "actual": m.Version}})
	}
	if m.Project.Name == "" {
		errs = append(errs, ValidationError{Code: "missing_field", Fields: map[string]string{"field": "project.name"}})
	}
	if len(m.Phases) == 0 {
		errs = append(errs, ValidationError{Code: "missing_field", Fields: map[string]string{"field": "phases"}})
	}
	// workflow 枚举/非空 + chain 引用（行号/列号/index 从 root 的 chain SequenceNode 取）
	errs = append(errs, validateWorkflows(m, root)...)
	// skill 存在性（若设了 WithSkillsDir）
	if cfg.skillsDir != "" {
		errs = append(errs, validateSkills(m, cfg.skillsDir)...)
	}
	return errs
}

// validateWorkflows 校验 chain 非空、checkpoints 枚举、chain→phase 引用。
func validateWorkflows(m *Manifest, root *yaml.Node) []ValidationError {
	var errs []ValidationError
	// 定位 root 里 workflows 段的 chain SequenceNode 以取行号/列号
	wfNodes := locateWorkflowChains(root) // map[workflowName] -> chain SequenceNode
	for wfName, wf := range m.Workflows {
		if len(wf.Chain) == 0 {
			errs = append(errs, ValidationError{Code: "empty_chain", Fields: map[string]string{"workflow": wfName}})
			continue
		}
		if wf.Checkpoints == "" {
			errs = append(errs, ValidationError{Code: "missing_field", Fields: map[string]string{"field": "checkpoints", "workflow": wfName}})
		} else if wf.Checkpoints != "after_each" && wf.Checkpoints != "manual" && wf.Checkpoints != "none" {
			errs = append(errs, ValidationError{Code: "invalid_enum", Fields: map[string]string{"workflow": wfName, "field": "checkpoints", "actual": wf.Checkpoints, "expected": "after_each|manual|none"}})
		}
		chainNode := wfNodes[wfName]
		for idx, p := range wf.Chain {
			if _, declared := m.Phases[p]; !declared {
				line, col := 0, 0
				if chainNode != nil && idx < len(chainNode.Content) {
					scalar := chainNode.Content[idx]
					line, col = scalar.Line, scalar.Column
				}
				errs = append(errs, ValidationError{
					Code:   "undeclared_phase",
					Line:   line,
					Column: col,
					Fields: map[string]string{
						"workflow": wfName,
						"phase":    p,
						"index":    fmt.Sprintf("%d", idx),
					},
				})
			}
		}
	}
	return errs
}

// locateWorkflowChains 从 root 找 workflows.<name>.chain 的 SequenceNode。
func locateWorkflowChains(root *yaml.Node) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return out
	}
	topMap := root.Content[0]
	if topMap.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(topMap.Content); i += 2 {
		if topMap.Content[i].Value != "workflows" {
			continue
		}
		wfMap := topMap.Content[i+1]
		if wfMap.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(wfMap.Content); j += 2 {
			wfName := wfMap.Content[j].Value
			wfVal := wfMap.Content[j+1]
			if wfVal.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(wfVal.Content); k += 2 {
				if wfVal.Content[k].Value == "chain" && wfVal.Content[k+1].Kind == yaml.SequenceNode {
					out[wfName] = wfVal.Content[k+1]
				}
			}
		}
	}
	return out
}

// validateSkills 校验 phase 的 skill（非 none）在 skillsDir 下存在。
func validateSkills(m *Manifest, skillsDir string) []ValidationError {
	var errs []ValidationError
	for pName, p := range m.Phases {
		if p.Skill == "" || p.Skill == "none" {
			continue
		}
		path := skillsDir + string(os.PathSeparator) + p.Skill
		if _, err := os.Stat(path); err != nil {
			errs = append(errs, ValidationError{Code: "skill_not_found", Fields: map[string]string{"phase": pName, "skill": p.Skill}})
		}
	}
	return errs
}
