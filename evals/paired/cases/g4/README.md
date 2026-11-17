# 组④说明：膨胀案例（隔离 worker vs 折叠）

两个 case 各自带一个**生成式** connector 仓库（`repo/`，go 1.16 纯标准
库），由 `../generate.py` 确定性生成（纯索引算术，无随机、无时钟、无环
境读取），`SHA256SUMS` 钉住全部文件——重生成必须逐字节一致。

对照假设：基线臂可把逐文件阅读分派给隔离 worker（子代理承载、只回传结
论），候选臂把材料折叠进主循环上下文逐文件读。spec §7 要求**必须实测到
退化**才判隔离必要：若折叠臂召回与时长不劣化，隔离 worker 的存在理由
不成立。

契约（`repo/CONTRACT.md`）：K1 重试预算 ≤2（blocking）、K2 退避严格递
增（blocking）、K3 错误署名（major）。违规分布由生成规则固定：
- K1 违规 = `i % 4 == 1` 的文件（g4-a 4 个 / g4-b 10 个）
- K2 实现 bug = 仅 `conn_02`（退避递减，任务要求修复的唯一文件）

参数分布刻意做了非平凡化（base delay 按 `i%7`、zone 按 `i%5`、owner
按 `i%3` 轮转），避免“grep 一个常量就出答案”。

`registry.go` 通过构造器（`NewConnXX()`）装配全部连接器并显式给出
配置默认值，`registry_smoke_test.go` 含 `TestRegistryDefaultsAreConfigured`
（budget≥1 且 Backoff(1)>0）——零值空壳注册表不再能掩盖植入的违规分布。
实测规模：g4-a 19 文件 / 25,415 UTF-8 字节；g4-b 44 文件 / 64,109
UTF-8 字节（`meta.yaml` 的 est_context_tokens 仅为 bytes/4 规划提示）。
