# 线上问题：并发导出偶发崩溃 / -race 告警

压测团队反馈：并发跑库存导出（多 goroutine 同时调 List）时**偶发**
`concurrent map writes` panic，`go test -race` 在 store 包稳定报
`WARNING: DATA RACE`。串行跑一切正常，问题只在高并发下出现。

上周的变更：给 List 加了惰性缓存（读多写少场景优化）。

请用 `/summoner:fix` 排查并修复。修复后 `go test -race ./internal/store/`
必须干净，且缓存优化不能直接退化成“删掉缓存”。
