# 项目总览层级

## 范围

工作区总览展示一套可检查的层级：

```text
项目 → 子项目 → 任务 / 运行
```

第一版以 `integ.life` 为示例，包含 Knowledge visual、Knowledge graph、Lingulet、Deployment & traffic 四个子项目。每个子项目展示状态、最近活动、阻塞、下一步，以及至少一个任务或运行。

这一版是项目工作区的 UI 投影，不写入或修改 event 数据。Records、Files、State、Connect、Plugins 仍然是项目数据的事实来源。

## 验收

- 登录后项目视图默认打开总览。
- `integ.life` 示例显示四个指定子项目及任务/运行状态。
- 其他项目仍显示一个可用的 Context workspace 子项目。
- 窄屏移动端可读，状态信息可被文本辅助技术读取。
- 总览可打开已有 Records 视图继续处理。
- 每个子项目都可以展开或收起，控件会向辅助技术公布当前状态。
- 总览筛选器支持全部、已阻塞、运行中和排队中，并同步更新可见数量。

_Codex 开发：2026-10-28。_
