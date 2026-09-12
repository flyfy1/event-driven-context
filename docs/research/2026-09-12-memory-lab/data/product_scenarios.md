# 产品专项场景与标准答案



全部输入均为新生成的合成数据；所有 UUID、人物与项目上下文只用于本次测试。完整机器可读输入、标准答案、期望状态分别在三个 JSON 文件中，构建器不读取 gold。



## 按时间排列的输入



| sequence | 原文与类型 | 来源/关系 |

|---|---|---|

| 1 | note: 海桥项目目标：在三月底前验证离线录音转文字闭环。 | 5b3dbe88-78a7-530e-badb-1b1f5bcb9b67; [] |

| 2 | note: 海桥项目预算确定为 50000 元。 | 73452f84-280c-57b0-8212-5ad8182dd1e8; [] |

| 3 | note: 海桥项目约束：所有原始录音只保存在本地设备，不上传第三方。 | 93457c14-bc05-57cf-b2f5-dd592e4e8cd3; [] |

| 4 | note: 海桥项目偏好：评审材料用中文，术语可保留英文。 | 259ae6c6-707f-59fe-8e29-0d152863215c; [] |

| 5 | log: 闲聊：最近在看一部科幻电影。 | 26693cc2-a8a3-5f94-9869-e28d72a7be46; [] |

| 6 | log: 测试日志：临时缓存被清理，天气晴朗。 | 851bb202-556d-54d6-bb2c-9f6fa8c4425b; [] |

| 7 | log: 午餐点了面条，周末准备读小说。 | 2347da57-2300-55a6-aceb-421f844a65c4; [] |

| 8 | log: 测试日志：临时缓存被清理，天气晴朗。 | dcbd5abd-e272-53b4-9e4d-c19434a5dd66; [] |

| 9 | log: 闲聊：最近在看一部科幻电影。 | d20f22b2-f803-547e-8591-21c79eb0fddb; [] |

| 10 | log: 今天咖啡很香，午后去散步。 | a31768c9-b945-504b-81e4-ee6204f12c13; [] |

| 11 | log: 今天咖啡很香，午后去散步。 | 97be4216-bec7-5f7a-a593-a2b062cbfc8b; [] |

| 12 | log: 今天咖啡很香，午后去散步。 | 927375a1-731c-577a-9d8c-8a382bcc1e84; [] |

| 13 | log: 测试日志：临时缓存被清理，天气晴朗。 | 5ff64d54-d86d-52f4-8424-f7c914d2334f; [] |

| 14 | log: 午餐点了面条，周末准备读小说。 | 3c7a4fc4-5dd9-5b1e-a02a-741f1e7a90eb; [] |

| 15 | log: 闲聊：最近在看一部科幻电影。 | 263276fa-bf47-5059-8e87-8ebcf602a699; [] |

| 16 | log: 今天咖啡很香，午后去散步。 | c0f8a61c-4d0d-5865-9169-922306a2d2d1; [] |

| 17 | log: 今天咖啡很香，午后去散步。 | cd213a93-10f0-5915-936b-e6973566209c; [] |

| 18 | log: 测试日志：临时缓存被清理，天气晴朗。 | a8c4c185-cdd4-5d92-a9cc-54e96e3647cb; [] |

| 19 | note: 海桥项目预算改为 30000 元，取代此前预算。 | 75afa1b7-9569-5b63-9980-54af3b564777; [{"id": "73452f84-280c-57b0-8212-5ad8182dd1e8", "rel": "supersedes"}] |

| 20 | note: 待办：由我在周五前提交海桥录音原型。 | e0ecfe1f-4a03-5930-be54-0c0fe94c1767; [] |

| 21 | note: 海桥录音原型已提交，这项待办完成。 | 11eb3d29-64e4-5a92-95c3-621dd1861b14; [{"id": "e0ecfe1f-4a03-5930-be54-0c0fe94c1767", "rel": "resolves"}] |

| 22 | note: 录音原型验收失败，重新打开提交录音原型待办。 | b788b78a-de94-5330-90e9-73492f15aa84; [{"id": "11eb3d29-64e4-5a92-95c3-621dd1861b14", "rel": "supersedes"}] |

| 23 | note: 待办：安排海桥印刷宣传册。 | 927db03f-d685-50f2-8d1e-ff7934017401; [] |

| 24 | note: 取消海桥印刷宣传册待办，不再需要宣传册。 | 2b48dc17-6025-5bb3-81fc-756590709e2c; [{"id": "927db03f-d685-50f2-8d1e-ff7934017401", "rel": "retracts"}] |

| 25 | note: 海桥上线日期定为 4 月 10 日。 | 6aae9390-02d0-539c-9ca2-a351abc3922e; [] |

| 26 | note: 海桥上线日期定为 4 月 20 日。 | b5531fcd-dd0e-53e7-9108-70e34ab3cc6d; [] |

| 27 | note: 我推测用户愿意为海桥购买付费云端转录服务，但尚未询问用户。 | 172b7eeb-0fc1-535b-be15-22ad006720cb; [] |

| 28 | note: 海桥目标设备数量为 12 台。 | d5919e15-980f-5522-a879-b1b75412efd9; [] |

| 29 | note: [合成音频占位：海桥交付负责人，原始字节不在此文本实验中。] | 82b19adf-b194-5af9-95ef-1798027d43b9; [] |

| 30 | derived: 海桥交付负责人是 [小林/小宁，听不清]。 | 2f0e0625-44d1-5c7b-8840-a497a2779a59; [{"id": "82b19adf-b194-5af9-95ef-1798027d43b9", "rel": "derived_from"}] |

| 31 | note: 我澄清录音：海桥交付负责人是小宁。 | 3c2e04f8-938f-5ae7-af59-1d96ce4d7bf0; [{"id": "2f0e0625-44d1-5c7b-8840-a497a2779a59", "rel": "supersedes"}] |

| 32 | log: 今天咖啡很香，午后去散步。 | 207b9c52-8a14-5d52-bcd5-e544e3a498f8; [] |

| 33 | log: 午餐点了面条，周末准备读小说。 | bd25a587-4005-531a-8cef-9e3335ed98aa; [] |

| 34 | log: 测试日志：临时缓存被清理，天气晴朗。 | bb4b3e51-8472-5122-8a28-659246bbfb55; [] |

| 35 | log: 午餐点了面条，周末准备读小说。 | 3233cf4d-feb9-522f-a303-d72de8721944; [] |

| 36 | log: 闲聊：最近在看一部科幻电影。 | 9da6fe37-41e8-50b5-aa25-4735798590be; [] |

| 37 | log: 测试日志：临时缓存被清理，天气晴朗。 | 923ae0ac-d869-501e-8fc8-5f09d07ae6b1; [] |

| 38 | log: 今天咖啡很香，午后去散步。 | 7fc9fcd6-41f4-51af-b050-b27419e7d264; [] |

| 39 | log: 测试日志：临时缓存被清理，天气晴朗。 | 2e780874-06e6-5da5-9c06-14a82f4130bb; [] |

| 40 | log: 今天咖啡很香，午后去散步。 | 49ce72c6-7b84-5cfc-88d7-c1c97489eeb7; [] |

| 41 | log: 今天咖啡很香，午后去散步。 | 952f7e59-01e2-56e7-bf7f-74fd62da65bf; [] |

| 42 | log: 午餐点了面条，周末准备读小说。 | 7228d0c9-02d8-5f44-8640-2cd23aceb56b; [] |

| 43 | log: 测试日志：临时缓存被清理，天气晴朗。 | e9d5e788-24be-5d5d-9859-402f174b3c7d; [] |

| 44 | log: 闲聊：最近在看一部科幻电影。 | 20bf70b5-baef-587c-bb72-97f1272e27e8; [] |

| 45 | log: 闲聊：最近在看一部科幻电影。 | 2b9ee228-c503-5e66-8764-afb91fc99f0c; [] |

| 46 | log: 今天咖啡很香，午后去散步。 | c189c574-33b2-5891-88aa-2e122f300631; [] |

| 47 | log: 今天咖啡很香，午后去散步。 | 58ebb971-8437-5cfc-8cdc-6ccb6ed0185e; [] |

| 48 | log: 闲聊：最近在看一部科幻电影。 | 6c8f57d3-2cf4-583c-86af-cd5e1560d488; [] |

| 49 | log: 闲聊：最近在看一部科幻电影。 | 9945ed63-0c36-5acc-99c4-2f95669053cd; [] |

| 50 | log: 午餐点了面条，周末准备读小说。 | 72c1f208-7c40-578d-aaf1-66c43809563b; [] |

| 51 | log: 测试日志：临时缓存被清理，天气晴朗。 | 7c0c9b0f-50a7-56dc-a481-0d1536d6d426; [] |

| 52 | note: 海桥尚未选择具体转录引擎，准确率也没有测试数据。 | 5df82780-fc67-5a6b-9c3f-eb60a73f494e; [] |

| 53 | note: 再次修改海桥预算为 25000 元。 | 5b5f22a4-138c-51df-99c6-1089aeade28b; [{"id": "75afa1b7-9569-5b63-9980-54af3b564777", "rel": "supersedes"}] |

| 54 | note: 补记：上周海桥在办公室讨论过离线测试，发生时间早于今天。 | 12aea57b-bfaa-52ac-9832-8c20c96c14a5; [] |

| 55 | note: 作为 agent，我建议将预算升到 99000 元；用户尚未确认。 | fc282a12-6d7c-58dd-8e36-57cd445dc37b; [{"id": "5b5f22a4-138c-51df-99c6-1089aeade28b", "rel": "supersedes"}] |

| 56 | note: 海桥开放问题：离线词表纠错是否需要先标注十段录音？尚未决定。 | 4071c563-4ce9-5b7e-b386-626fa2d451a1; [] |



## 各问题与期望



### product-0/budget_initial，截止 sequence 4

问题：海桥项目当前预算多少元？

标准：known；可接受关键表达：50000

必须引用：73452f84-280c-57b0-8212-5ad8182dd1e8

不应出现：超出证据的确定结论



### product-0/budget_updated，截止 sequence 19

问题：海桥项目当前预算多少元？只报当前数额。

标准：known；可接受关键表达：30000

必须引用：75afa1b7-9569-5b63-9980-54af3b564777

不应出现：50000



### product-0/todo_open，截止 sequence 20

问题：海桥录音原型待办是什么状态？

标准：known；可接受关键表达：待办 / 未完成 / 待提交 / 未提交

必须引用：e0ecfe1f-4a03-5930-be54-0c0fe94c1767

不应出现：超出证据的确定结论



### product-0/todo_done，截止 sequence 21

问题：海桥录音原型待办是什么状态？

标准：known；可接受关键表达：完成 / 已提交

必须引用：11eb3d29-64e4-5a92-95c3-621dd1861b14

不应出现：超出证据的确定结论



### product-0/todo_reopen，截止 sequence 22

问题：海桥录音原型待办是什么状态？

标准：known；可接受关键表达：重新打开 / 未完成 / 重新提交

必须引用：b788b78a-de94-5330-90e9-73492f15aa84

不应出现：已经完成



### product-0/todo_cancel，截止 sequence 24

问题：还需要印刷海桥宣传册吗？

标准：known；可接受关键表达：取消 / 不再需要 / 不需要

必须引用：2b48dc17-6025-5bb3-81fc-756590709e2c

不应出现：超出证据的确定结论



### product-0/member_conflict，截止 sequence 26

问题：海桥上线日期到底是哪天？

标准：conflict；可接受关键表达：4 月 10 / 4月10 / 4 月 20 / 4月20

必须引用：6aae9390-02d0-539c-9ca2-a351abc3922e, b5531fcd-dd0e-53e7-9108-70e34ab3cc6d

不应出现：超出证据的确定结论



### product-0/agent_unconfirmed，截止 sequence 27

问题：用户已经同意为海桥购买付费云转录服务了吗？

标准：unknown；可接受关键表达：未 / 没有 / 不知道 / 尚未

必须引用：172b7eeb-0fc1-535b-be15-22ad006720cb

不应出现：已经同意



### product-0/duplicate_once，截止 sequence 28

问题：海桥目标设备数量是多少台？

标准：known；可接受关键表达：12

必须引用：d5919e15-980f-5522-a879-b1b75412efd9

不应出现：超出证据的确定结论



### product-0/audio_unclear，截止 sequence 30

问题：海桥交付负责人是谁？

标准：unknown；可接受关键表达：听不清 / 不确定 / 不知道 / 无法确定

必须引用：2f0e0625-44d1-5c7b-8840-a497a2779a59

不应出现：超出证据的确定结论



### product-0/audio_corrected，截止 sequence 31

问题：海桥交付负责人是谁？

标准：known；可接受关键表达：小宁

必须引用：3c2e04f8-938f-5ae7-af59-1d96ce4d7bf0

不应出现：小林



### product-0/unknown，截止 sequence 52

问题：海桥当前选定的转录引擎实测准确率是多少？

标准：unknown；可接受关键表达：不知道 / 未 / 没有 / 无法

必须引用：5df82780-fc67-5a6b-9c3f-eb60a73f494e

不应出现：超出证据的确定结论



### product-0/old_goal，截止 sequence 52

问题：海桥项目最初要完成的核心目标是什么？

标准：known；可接受关键表达：离线录音 / 录音转文字

必须引用：5b3dbe88-78a7-530e-badb-1b1f5bcb9b67

不应出现：超出证据的确定结论



### product-0/irrelevant_constraint，截止 sequence 52

问题：请安排下一步技术验证；关于原始录音存放，必须遵守什么约束？

标准：known；可接受关键表达：本地 / 不上传

必须引用：93457c14-bc05-57cf-b2f5-dd592e4e8cd3

不应出现：超出证据的确定结论



### product-0/preference，截止 sequence 52

问题：海桥评审材料应该用什么语言？

标准：known；可接受关键表达：中文

必须引用：259ae6c6-707f-59fe-8e29-0d152863215c

不应出现：超出证据的确定结论



### product-0/lag，截止 sequence 53

问题：海桥最新预算是多少元？

标准：known；可接受关键表达：25000

必须引用：5b5f22a4-138c-51df-99c6-1089aeade28b

不应出现：30000



### product-0/historical，截止 sequence 53

问题：海桥最初确定的预算是多少元？

标准：known；可接受关键表达：50000

必须引用：73452f84-280c-57b0-8212-5ad8182dd1e8

不应出现：超出证据的确定结论



### product-0/late_arrival，截止 sequence 54

问题：今天新增的补记描述了什么时候的讨论？

标准：known；可接受关键表达：上周 / 2025-12-26

必须引用：12aea57b-bfaa-52ac-9832-8c20c96c14a5

不应出现：超出证据的确定结论



### product-0/untrusted_update，截止 sequence 55

问题：用户确认的海桥最新预算是多少元？

标准：known；可接受关键表达：25000

必须引用：5b5f22a4-138c-51df-99c6-1089aeade28b

不应出现：99000



### product-0/openquestion，截止 sequence 56

问题：海桥关于词表纠错有哪些尚未解决的问题？

标准：known；可接受关键表达：十段 / 标注

必须引用：4071c563-4ce9-5b7e-b386-626fa2d451a1

不应出现：超出证据的确定结论



### product-0/isolation，截止 sequence 56

问题：海桥（不是海桥二期）交付负责人是谁？

标准：known；可接受关键表达：小宁

必须引用：3c2e04f8-938f-5ae7-af59-1d96ce4d7bf0

不应出现：小林



## 保存状态检查点



active 为本检查点必须保留的条目集合，不是穷尽所有应保留条目。must_not_active 表示历史可查，但不得继续作为当前事实。



```json
{
  "history": "product-0",
  "cutoff": 4,
  "active": [
    "5b3dbe88-78a7-530e-badb-1b1f5bcb9b67",
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "93457c14-bc05-57cf-b2f5-dd592e4e8cd3",
    "259ae6c6-707f-59fe-8e29-0d152863215c"
  ],
  "must_not_active": [],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 19,
  "active": [
    "5b3dbe88-78a7-530e-badb-1b1f5bcb9b67",
    "75afa1b7-9569-5b63-9980-54af3b564777",
    "93457c14-bc05-57cf-b2f5-dd592e4e8cd3",
    "259ae6c6-707f-59fe-8e29-0d152863215c"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8"
  ],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 21,
  "active": [
    "11eb3d29-64e4-5a92-95c3-621dd1861b14"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767"
  ],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 22,
  "active": [
    "b788b78a-de94-5330-90e9-73492f15aa84"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14"
  ],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 24,
  "active": [
    "2b48dc17-6025-5bb3-81fc-756590709e2c"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401"
  ],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 26,
  "active": [
    "6aae9390-02d0-539c-9ca2-a351abc3922e",
    "b5531fcd-dd0e-53e7-9108-70e34ab3cc6d"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401"
  ],
  "must_not_promote": [],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 27,
  "active": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401"
  ],
  "must_not_promote": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 28,
  "active": [
    "d5919e15-980f-5522-a879-b1b75412efd9"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401"
  ],
  "must_not_promote": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 31,
  "active": [
    "3c2e04f8-938f-5ae7-af59-1d96ce4d7bf0"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401",
    "2f0e0625-44d1-5c7b-8840-a497a2779a59"
  ],
  "must_not_promote": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 52,
  "active": [
    "5b3dbe88-78a7-530e-badb-1b1f5bcb9b67",
    "93457c14-bc05-57cf-b2f5-dd592e4e8cd3",
    "259ae6c6-707f-59fe-8e29-0d152863215c",
    "75afa1b7-9569-5b63-9980-54af3b564777",
    "b788b78a-de94-5330-90e9-73492f15aa84",
    "3c2e04f8-938f-5ae7-af59-1d96ce4d7bf0",
    "5df82780-fc67-5a6b-9c3f-eb60a73f494e"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401",
    "2f0e0625-44d1-5c7b-8840-a497a2779a59"
  ],
  "must_not_promote": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```

```json
{
  "history": "product-0",
  "cutoff": 53,
  "active": [
    "5b5f22a4-138c-51df-99c6-1089aeade28b"
  ],
  "must_not_active": [
    "73452f84-280c-57b0-8212-5ad8182dd1e8",
    "e0ecfe1f-4a03-5930-be54-0c0fe94c1767",
    "11eb3d29-64e4-5a92-95c3-621dd1861b14",
    "927db03f-d685-50f2-8d1e-ff7934017401",
    "2f0e0625-44d1-5c7b-8840-a497a2779a59",
    "75afa1b7-9569-5b63-9980-54af3b564777"
  ],
  "must_not_promote": [
    "172b7eeb-0fc1-535b-be15-22ad006720cb"
  ],
  "scope": "selected assertions, not exhaustive semantic recall"
}
```