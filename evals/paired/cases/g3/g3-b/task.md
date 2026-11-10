# API 重命名：GetByID → Fetch（含文档与变更记录）

同 g3-a 的重命名任务，但这次是**正式的破坏性变更发布**，要求更完整：

1. store.GetByID → Fetch（签名/语义不变），更新全部调用点（含按名字
   解析的桥接/脚本入口，相关文件顶部注释写明了约定）；
2. **CHANGELOG.md** 的 Unreleased 节必须记录这次破坏性改名（旧名→新
   名、影响面、迁移说明）；
3. 仓库内文档中提到旧名的地方一并更新（README、包 doc comment 中如
   有）；
4. 完成后 `go build ./...` 和 `go test ./...` 必须全绿。
