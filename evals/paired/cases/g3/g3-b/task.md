# API 重命名：GetByID → Fetch（含文档与变更记录）

本次为正式的破坏性 API 改名发布，请完成：

1. store.GetByID → **Fetch**，签名和语义不变，更新全部引用并保证调用者
   继续工作；
2. 在 `CHANGELOG.md` 的 Unreleased 节记录旧名、新名、影响面与迁移说明；
3. 更新仍描述旧 API 的当前文档和代码注释；变更记录中的历史旧名应保留；
4. 完成后 `go build ./...` 和 `go test ./...` 必须全绿。
