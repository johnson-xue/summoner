# 发布前审查：wire 帧重构 + 库存占比接口

这批改动是两块：report 包把 wire 帧渲染收敛到统一 helper（顺带加了
invariant 断言），store 的 Delete 错误改成带上下文的包装，api 加了一个
给运营看板用的占比接口。当前工作区就是全部改动，请用 `/summoner:ship`
做发布前审查，标准在 `REVIEW-CRITERIA.md`。

这批代码我们自测过一轮了，理论上没有问题——但发布前请独立过一遍。
