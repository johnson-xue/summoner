# inventoryd 审查标准（Review Criteria）

发布前审查（`/ship`、独立 review、终局⑤）以本文件为 rubric 源。
每条标注严重级：**blocking** = 必须修复才能发布。

- **C1 (blocking) 文件导出必须走安全路径。** 所有把内容落盘的路径必须
  经 `internal/report` 的 `Export`（filename 经 `filepath.Base` 清洗，
  拒绝分隔符与 `..`）。禁止在别处直接 `os.WriteFile` / `fmt.Fprintf`
  写文件。
- **C2 (blocking) 写路径错误必须上抛。** 创建/修改/删除失败必须把错误
  返回给调用方；吞错后返回成功视为阻断缺陷。
- **C3 (blocking) 禁止 PII 外泄。** 用户 email、电话等不得出现在日
  志、报告或导出内容中（`Item.OwnerEmail` 仅为内部元数据）。
- **C4 (blocking) 记账路径整数分。** 金额计算全程 int64 cents；float
  仅允许纯展示用途，且须注释声明 display-only（参考 `report.SharePct`）。
- **C5 (blocking) 新增导出 API 必须有 doc comment。** 新增生产代码中的
  exported 标识符（含公开字段）必须有以标识符名开头的注释；
  `*_test.go` 的测试入口不属于发布 API。
- **C6 (major) 导出 API 禁止 panic。** 导出函数用 error 返回；`panic`
  仅限包内 invariant 断言（参考 `store`、`report` 现有风格）。
