# API 重命名：GetByID → Fetch

将 store.GetByID 统一改名为 **Fetch**，签名与语义完全不变。更新定义及
全部引用，确保所有调用者在改名后继续工作。

完成后 `go build ./...` 和 `go test ./...` 必须全绿。
