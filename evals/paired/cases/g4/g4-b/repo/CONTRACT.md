# connector 契约（审计以本文件为准）

`internal/connector/conn_*.go` 每个文件实现一个上游槽位的连接器，全部
必须满足：

- **K1 (blocking) 重试预算**：`MaxRetries() <= 2`（含首次在内总尝试次数
  不超过 3）。预算 3 及以上视为违规。
- **K2 (blocking) 退避严格递增**：`Backoff(1) < Backoff(2) < ... <
  Backoff(MaxRetries())`。任何非严格递增（持平或递减）视为违规。
- **K3 (major) 错误署名**：返回的 error 必须携带 connector 名
  （`fmt.Errorf` 前缀或 `%w` 包装均可）。

K1/K2 违规进入审计清单（blocking-listed）；K3 记 major。
