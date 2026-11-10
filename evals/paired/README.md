# Paired Behavioral Evals — Case Set (spec §7 放行门)

Frozen case set for the fold-and-measure gate that decides whether the
single-mode rewrite ships. **Cases live here; the harness
(`scripts/run-paired-eval.sh`) and tolerances
(`evals/paired/tolerances.yaml.example`) are Task 15 harness work and are
NOT part of this case set.** Actual model runs happen only after the
budget checkpoint — the user approves budget and tolerances first.

## Arms

| Arm | Checkout | Orchestration |
|-----|----------|---------------|
| `baseline` | git tag `v0.3.0` | per-node ⑤ + 岗位 persona 编排, `/ship` multi-persona fan-out, walker/graph mode |
| `candidate` | rewrite branch (this tree, ≥360f1cd) | 主会话单循环 + 终局⑤ + 检查点接受门, 单 rubric reviewer |

Both arms run **the same frozen case input**: identical repo state,
identical task prompt, identical declared criteria. Only the
orchestration differs. Model/platform pins and budget are harness
concerns (see Task 15 Step 4 checkpoint).

## Five comparison groups (spec §7)

| Group | Cases | Baseline behavior | Candidate behavior | Primary metrics |
|-------|-------|-------------------|--------------------|-----------------|
| ① 工作流审查 | `g1-*` | 逐节点⑤ + 岗位编排 during `/summoner:fix` | 主循环实现+测试, 终局⑤, 检查点接受门 | 阻断缺陷召回（修复完整性）、误报（FP-fix）、重复搜索、时长 |
| ② 发布前审查 | `g2-*` | `/ship` 多 persona fan-out + merge | 单 rubric reviewer（REVIEW-CRITERIA.md 为 rubric 源） | 阻断缺陷召回、误报、标准遗漏、重复搜索 |
| ③ 分片策略 | `g3-*` | 真独立分片并行 | 顺序执行 | 分片覆盖（漏改点）、质量（merge 后 build/test）、协调量、时长（不预设更快） |
| ④ 膨胀案例 | `g4-*` | 隔离 worker（子代理承载阅读） | 折叠进主循环上下文 | 违规清单召回、质量退化（必须实测到才判隔离必要）、时长 |
| ⑤ 独立审查 | `g5-*` | 独立⑤（新证据源） | 自审（同一上下文） | 质量（缺陷召回/误报）与独立性（verdict 是否引用 producer 报告之外的新证据）**分列报告** |

## Case anatomy

```
evals/paired/cases/
  frozen_base/                 # shared green Go repo "inventoryd" (go 1.16, stdlib only, offline)
    …                          # tests PASS at base; defects arrive only via setup.patch
  g1/g1-a/
    task.md                    # frozen user intent (the only prompt; never leaks the answer)
    setup.patch                # unified diff (a/repo b/repo) applied onto a copy of frozen_base
    truth.json                 # ground truth: defects, FP traps, criteria, scoring keys
    meta.yaml                  # group, split, difficulty, est. context size
  g4/…                         # group 4 cases carry their own generated repo/ (see g4/README.md)
```

**Harness contract for `setup.patch`:** copy `frozen_base` → `$RUN/repo`,
then `cd $RUN && patch -p1 < cases/gN/<id>/setup.patch` (skip the patch
step when the case declares `setup: none`). Paths in patches are
`a/repo/…` / `b/repo/…`. For ship/review cases (g2, g5) the harness
should `git init` + commit the pristine copy BEFORE applying the patch,
so the feature diff is visible as working-tree changes to both arms.
Cases declaring `repo: self` (g4) ignore `frozen_base` and use their own
committed tree (`repo/`, pinned by `SHA256SUMS`).

**Green-but-wrong invariant:** after applying `setup.patch`,
`go build ./... && go test ./...` in the case repo is **green** for
every g1/g2/g5 case. Every planted defect ships with a captured,
blinded, or merely absent test — exactly the failure mode the review
layers exist to catch. (g3 has no defect patch; the task itself is the
work under measurement.)

## truth.json schema

```jsonc
{
  "case_id": "g2-a",
  "group": 2,
  "clean_negative": false,          // true ⇒ correct outcome reports ZERO blocking findings
  "defects": [{
    "id": "D1",
    "file": "internal/report/report.go",
    "line_anchor": "func Export",   // stable anchor; lines drift with patch fuzz
    "class": "security",            // security|correctness|concurrency|criteria|test-gap
    "severity": "blocking",
    "description": "…",
    "evidence": "how an honest reviewer verifies it (repro, invariant, test)",
    "minimal_fix": "what the smallest correct fix must preserve",
    "detectable_by": ["g2"]         // which groups' metrics consume this defect
  }],
  "fp_traps": [{
    "id": "T1",
    "file": "…", "line_anchor": "…",
    "looks_like": "off-by-one / float money / swallowed error …",
    "why_clean": "the documented invariant that makes it correct (with pointer)"
  }],
  "criteria": [{ "id": "C3", "source": "REVIEW-CRITERIA.md", "statement": "…",
                 "violated_by": "D3" }],
  "scoring": {
    "recall_defect_ids": ["D1","D2","D3"],   // blocking-recall numerator
    "fp_trap_ids": ["T1","T2"],              // any T* cited as a defect ⇒ FP event
    "criteria_omission_ids": ["C3"],         // declared criterion missed ⇒ omission event
    "notes": "case-specific judge guidance"
  }
}
```

A reported finding that matches neither a defect id nor a trap id is
**not** automatically an FP — the judge (harness) weighs it against the
repo; genuinely novel true findings are recorded as `novel_true` and do
not count against recall. Fabricated or trap-cited findings count as FP.

## Case index

| Case | Group | Split | Kind | Blocking defects | Traps | Clean neg |
|------|-------|-------|------|------------------|-------|-----------|
| g1-a | ① | train | fix: rune→byte regression + captured test | 2 (D1 impl, D2 test-capture) | 1 | – |
| g1-b | ① | train | fix: swallowed batch errors | 1 | 1 | – |
| g1-c | ① | holdout | fix: lazy cache data race (RLock path writes) | 1 | 1 | – |
| g1-neg | ① | train | fix: intended behavior reported as bug | 0 | 1 | ✅ |
| g2-a | ② | train | ship review: traversal + swallowed err + PII log | 3 | 2 | – |
| g2-b | ② | train | ship review: scary-but-correct diff | 0 | 3 | ✅ |
| g2-c | ② | holdout | ship review: authz gap + goroutine leak + undocumented API | 3 | 2 | – |
| g3-a | ③ | train | rename migration, hidden FuncMap ref | 1 | 0 | – |
| g3-b | ③ | holdout | rename migration, FuncMap + reflection refs | 2 | 0 | – |
| g4-a | ④ | train | connector 契约审计，15 文件（76KB） | 1 (K2 退避 bug) | 4 K1 违规 | – |
| g4-b | ④ | holdout | connector 契约审计，40 文件（176KB） | 1 (K2 退避 bug) | 10 K1 违规 | – |
| g5-a | ⑤ | train | finished feature, vacuous boundary test | 1 | 1 | – |
| g5-neg | ⑤ | train | finished feature, correct + honest report | 0 | 1 | ✅ |
| g5-b | ⑤ | holdout | finished feature, unmet criterion + confident report | 1 | 0 | – |

Train/holdout policy: **holdout cases are never used to tune rubrics,
prompts, or tolerances.** The first full run trains on `train`, the
adjudication run includes `holdout`. Any post-freeze correction to a
truth file must be called out in its commit message with the reason
(measured-wrong-expectation vs authoring error).

## Frozen-input rules

1. **Deterministic:** no network, no wall-clock dependence (clocks
   injected or fixed), no random seeds. `go test ./...` is the only
   execution oracle; g1-c additionally requires `go test -race`.
2. **Prompts never leak answers:** task.md states the *symptom* in user
   voice, never the file, mechanism, or fix.
3. **Defects are behavior-observable:** every `D*` has an `evidence`
   a reviewer could execute or check without trusting the producer.
4. **Traps are documented-clean:** every `T*` cites the invariant that
   makes it correct, inside the repo itself (doc comment or
   REVIEW-CRITERIA.md) — flagging it means the reviewer skipped the
   contract, which is precisely what FP measures.
5. **Stdlib only, go 1.16** (CI toolchain parity).

## Provenance

- Case set authored on branch `t15-eval-cases` (base 360f1cd).
- g4 repos are generated by `g4/generate.py` (fixed index arithmetic, no
  RNG, no clock, no environment reads); committed outputs are pinned by
  `g4/*/SHA256SUMS`. Regenerating must reproduce identical checksums
  (verified once at authoring time).
