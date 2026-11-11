# inventoryd

小型库存服务（Go 1.16，仅标准库）。CLI + 报表 + 老式货架设备 wire 协议。

## 行为契约（实现与审查都以这些为准）

- `store.List` **按 ID 升序**返回；插入顺序不构成契约（map 迭代序未
  定义），依赖顺序的消费方必须自行排序。
- `textutil.Truncate` 按 **rune** 截断（展示文本，CJK 安全）；
  `textutil.TruncateBytes` 按 **byte** 截断（wire 帧预算）。切半 rune
  是 wire 路径的**契约内行为**——调用方用 `strings.ToValidUTF8` 再编
  码；不要把它“修”成 rune 语义，货架设备对超长帧直接断连。
- 金额计算全程 int64 分；float 仅允许纯展示（见 `report.SharePct`
  与 REVIEW-CRITERIA.md C4）。
- 发布前审查标准见 `REVIEW-CRITERIA.md`（blocking 级共 5 条）。

## 运行

```sh
go build ./... && go test ./...
go run ./cmd/inventoryd
```
