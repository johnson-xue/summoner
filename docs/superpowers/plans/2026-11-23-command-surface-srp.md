# Command/Skill 单一职责重构 — 实施计划（0.6.0）

- 设计: `../specs/2026-11-23-command-surface-srp-design.md` rev2（已批准）
- 分支: command-surface-srp（基线 8525cfc）
- 执行模式: 本会话串行 + 里程碑全电池（同 0.5.1-0.5.3）；scripts/ 改动
  lane CI 先于任何 tag（0.5.2 纪律）
- 顺序原则: **先立新权威、先改全部引用，最后删目录** — 任何中间提交不留下
  悬空指针

## 任务

### T1 创建 references/single-loop.md（新权威）
- 内容按设计 D1: 六步序列骨架（每步一行+指针，3/4/5 步权威留原地）、
  套件路由表（fix/debug 消歧、未命中即纯问答）、九个摘要+指针节。
- 验证: `wc -l` ≤120；对六份现有 reference 逐节核对零迁移。

### T2 八个 command 自足化（D2）
- 逐个重写: description 意图短语 / allowed-tools / Route / 能力映射 /
  套件规则 / Loop 行五要素（唯一通道+逐字粘贴+nonce+keyword 标签+两轮，
  指针 `${CLAUDE_PLUGIN_ROOT}/references/single-loop.md`）。
- 验证: 验收 4 五要素逐条 grep ×8；AGENTS.md 50-80 行预算。

### T3 session-start 指针重指向（D8 开洞，test-first）
- 先改 main_test.go:56 断言新指针 → 观察 RED → 改 main.go:219 注入串 +
  :185 注释 → GREEN。
- 验证: `go test ./hooks/session-start`；`make -C hooks build`。

### T4 适配面 + 评测侧迁移（D3 前半）
- .gemini 8 toml 第 1 步 + GEMINI.md:7,25；.opencode/INSTALL.md:7；
  docs/codex-setup.md:10；evals/README.md:7；run-routing-evals.sh:7；
  routing.json:14 note；validate-repo.sh:515-516 规则目标 + :175 注释措辞。
- 验证: `scripts/validate-repo.sh` 全绿；routing evals 通过。

### T5 setup 合一（D4）
- summoner-setup 吸收 auto 触发纪律；删 skills/auto-setup-summoner/；
  7 引用点迁移（两 plugin.json 数组、no-manifest-flow:32、
  capability-map:73、validate-repo:7、install-smoke-test:140、
  summoner-setup:43）。
- 验证: 验收 2 grep。

### T6 release 瘦身（D5）
- 抽 scripts/release-plan.sh（fence :30,:53）/ release-changelog.sh（:97）/
  release-publish.sh（:149,:171,:249，含三清单版本写入）；
  去交互化 4 处 read -p（决策归还检查点）；SKILL.md 重写为调用契约。
- 验证: `bash -n` ×3；本地 `--dry-run` 场景（无变更）；SKILL.md ≤90 行
  零 fence；shellcheck 走 lane CI。

### T7 roster 去方法论化（D6）
- 9 个方法论 default → none；:5 注释去 superpowers。
- 验证: 验收 6 grep。

### T8 数组同步 + 冒烟断言（D7）
- 两 plugin.json 数组去 `./skills/summoner`；install-smoke-test:85 比对
  对象改 single-loop.md、名单/文案 8→6、新增归一化数组断言
  （name/path 对象 vs ./skills/x 字符串先归一）。
- 验证: `scripts/install-smoke-test.sh` 绿。

### T9 删除 god-skill + 文档收尾（D3 后半）
- `git rm -r skills/summoner/`；AGENTS.md:10、根 summoner.md、CLAUDE.md
  结构图改述。
- 验证: 验收 1 全仓 `git grep -nE 'skills/summoner($|/)'` + 排除表
  （CHANGELOG.md、docs/）零命中。

### T10 全电池 + 版本 0.6.0
- `make -C hooks build release` → `go test ./...`（contract 前先 build，"
  0.5.1 教训）→ validate-repo → evals → install-smoke → push 分支过
  lane CI（shellcheck 唯一 shellcheck）。
- CHANGELOG 0.6.0 条目；三清单版本 0.6.0。
- 发布动作（tag/gh release/master FF）待 ship 三轮审查 GO 后单独执行。

## 里程碑电池
- M1（T1-T4 后）: validate-repo + go test ./hooks/... + routing evals。
- M2（T5-T8 后）: + install-smoke-test + bash -n + dry-run 场景。
- M3（T9-T10）: 全电池 + lane CI。

## 回滚
- 分支未合入前任意 `git reset` 到基线；无迁移脚本、无状态格式变化、
  hooks 机制零改动 — 用户侧回滚 = 降级插件版本。
