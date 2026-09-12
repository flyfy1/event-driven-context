# 逐条人工式复核

由主协调 agent 阅读原始回答、对应源事件与摘要完成；没有独立人类标注员。特意选择未知/冲突题和方法分歧，不能将这份有偏样本的正确比例当总体准确率。保留自动分数不覆盖，并列内容和来源判断。

| 问题/方法 | 自动规则 | 正文正确 | 来源支持 | 复核结论 |
|---|---:|---:|---:|---|
|product-0/member_conflict/A|0|False|False|A把4/20叫最终决定、4/10叫早期决定，没有取代证据；其他组正确并列。方括号格式错误与内容错误分开。|
|product-0/member_conflict/B|1|True|True|A把4/20叫最终决定、4/10叫早期决定，没有取代证据；其他组正确并列。方括号格式错误与内容错误分开。|
|product-0/member_conflict/C|1|True|True|A把4/20叫最终决定、4/10叫早期决定，没有取代证据；其他组正确并列。方括号格式错误与内容错误分开。|
|product-0/member_conflict/D|1|True|True|A把4/20叫最终决定、4/10叫早期决定，没有取代证据；其他组正确并列。方括号格式错误与内容错误分开。|
|product-0/agent_unconfirmed/A|1|True|True|正文明确没有用户确认，符合原文。部分组 citations 多一层方括号；显示可识别但不满足原始 ID API 格式。|
|product-0/agent_unconfirmed/B|1|True|True|正文明确没有用户确认，符合原文。部分组 citations 多一层方括号；显示可识别但不满足原始 ID API 格式。|
|product-0/agent_unconfirmed/C|1|True|True|正文明确没有用户确认，符合原文。部分组 citations 多一层方括号；显示可识别但不满足原始 ID API 格式。|
|product-0/agent_unconfirmed/D|1|True|True|正文明确没有用户确认，符合原文。部分组 citations 多一层方括号；显示可识别但不满足原始 ID API 格式。|
|product-0/audio_unclear/A|1|True|True|B回答“林”并标 known，来源只支持小林/小宁听不清；其摘要末尾被截断。其他组保留歧义。|
|product-0/audio_unclear/B|0|False|False|B回答“林”并标 known，来源只支持小林/小宁听不清；其摘要末尾被截断。其他组保留歧义。|
|product-0/audio_unclear/C|1|True|True|B回答“林”并标 known，来源只支持小林/小宁听不清；其摘要末尾被截断。其他组保留歧义。|
|product-0/audio_unclear/D|1|True|True|B回答“林”并标 known，来源只支持小林/小宁听不清；其摘要末尾被截断。其他组保留歧义。|
|product-0/audio_corrected/B|0|False|False|漏掉更正来源，仍依赖音频/歧义转录并说不能确定；是摘要覆盖失败，不是成功拒答。|
|product-0/unknown/A|0|True|True|四组正文均正确说明未选引擎/没有准确率数据，但填 known；内容层拒答正确，结构化状态失败。不能把该规则失分称为事实幻觉。|
|product-0/unknown/B|0|True|True|四组正文均正确说明未选引擎/没有准确率数据，但填 known；内容层拒答正确，结构化状态失败。不能把该规则失分称为事实幻觉。|
|product-0/unknown/C|0|True|True|四组正文均正确说明未选引擎/没有准确率数据，但填 known；内容层拒答正确，结构化状态失败。不能把该规则失分称为事实幻觉。|
|product-0/unknown/D|0|True|True|四组正文均正确说明未选引擎/没有准确率数据，但填 known；内容层拒答正确，结构化状态失败。不能把该规则失分称为事实幻觉。|
|product-0/irrelevant_constraint/B|0|True|True|正文给出正确本地保存约束并附来源；因同时谈上线冲突而把总体status填conflict。gold仅一个来源不能否定额外已支持的引用。|
|product-0/historical/B|0|False|False|最初预算50000，返回30000。摘要保留最初事件ID却没有保住对应金额；ID召回并不证明事实保真。|
|product-0/late_arrival/B|0|False|False|问题只需“上周”，摘要丢失补记并转而要求具体当前日期；答案缺失，非无证据的标准未知问题。|
|product-0/untrusted_update/C|0|True|True|正文区分25000用户预算和99000未确认建议，未把建议当事实。但status标conflict不适合当前已确认预算；禁用词命中不能等同断言99000为当前预算。|
|locomo-0/q24/C|0|False|True|Running由e00128支持，即使gold只列e00130也不是假引用；但漏掉pottery，答案不完整。|
|locomo-0/q24/D|0|False|True|Running由e00128支持，即使gold只列e00130也不是假引用；但漏掉pottery，答案不完整。|
|locomo-0/q25/B|0|True|True|原文7/12说两天前；July 10, 2023与gold 10 July 2023相同，子串顺序造成假阴性。|
|locomo-0/q25/C|0|True|True|原文7/12说两天前；July 10, 2023与gold 10 July 2023相同，子串顺序造成假阴性。|
|locomo-0/q25/D|0|True|True|原文7/12说两天前；July 10, 2023与gold 10 July 2023相同，子串顺序造成假阴性。|
|locomo-0/q73/C|0|True|True|Last month relative to October 13明确指9月；语义正确而字面规则失败。|
|locomo-0/q73/D|0|True|True|Last month relative to October 13明确指9月；语义正确而字面规则失败。|
|locomo-0/q88/D|0|True|True|提供safe loving home for kids的释义完整，e00357确实支持。gold列早期e00032不穷尽可用证据，精确引用召回0不能说明答案没来源。|
|locomo-0/q103/C|1|True|True|正确给出Charlotte's Web并引用e00102。|
|locomo-0/q103/D|0|False|False|D在相同预算内加入大量前邻轮次/状态头，e00102被挤出；C能取到。显示闭包/邻居扩展会降低有效容量。|
|locomo-0/q187/B|0|False|False|去咖啡馆并提供照片的是Melanie；Caroline只是回复照片。回答把caption的标志内容当作Caroline在咖啡馆看到的内容，混淆主体与场景；应拒绝该前提。|
|locomo-0/q187/C|0|False|False|去咖啡馆并提供照片的是Melanie；Caroline只是回复照片。回答把caption的标志内容当作Caroline在咖啡馆看到的内容，混淆主体与场景；应拒绝该前提。|
|locomo-0/q187/D|0|False|False|去咖啡馆并提供照片的是Melanie；Caroline只是回复照片。回答把caption的标志内容当作Caroline在咖啡馆看到的内容，混淆主体与场景；应拒绝该前提。|
|locomo-1/q28/C|0|False|False|原文6/19说last week；C擅定6/18，D擅定6/19。D虽然因包含June 2023通过规则，具体日期无依据，是规则假阳性。|
|locomo-1/q28/D|1|False|False|原文6/19说last week；C擅定6/18，D擅定6/19。D虽然因包含June 2023通过规则，具体日期无依据，是规则假阳性。|
|locomo-1/q63/C|0|None|True|gold问5/23，原文5/27仅说just got accepted，没有支持5/23。reader指出日期前提不足，但已识别fashion internship；记争议/部分正确，不强改成成功或失败。|
|locomo-1/q63/D|0|None|True|gold问5/23，原文5/27仅说just got accepted，没有支持5/23。reader指出日期前提不足，但已识别fashion internship；记争议/部分正确，不强改成成功或失败。|
|mab-0/0/q30/D|1|True|False|答New York City碰到gold，但缺少Lou Reed到Fordham的e00401中间边，引用还包括旧演奏者e00054；答案匹配不证明已由所给证据推导。|
|mab-0/0/q41/C|0|False|False|451死亡地点改USA，295规定USA官方语言German，二者都在上下文；reader仍当作无法解决的冲突。该基准明确规定最高原始事实编号胜出。|
|mab-0/0/q41/D|0|False|False|451死亡地点改USA，295规定USA官方语言German，二者都在上下文；reader仍当作无法解决的冲突。该基准明确规定最高原始事实编号胜出。|
|mab-0/4/q45/C|0|False|False|e00303新事实已检索到，但仍选旧作者Ursula K. Le Guin；是reader忽略基准更新规则。|
|mab-0/4/q45/D|1|True|True|按原始事实301得到Pius XII，引用e00303支持。|
|mab-0/4/q30/C|0|False|False|原始事实382已把baseball的国家改为Japan；该事实与旧160同时在上下文内，reader仍答旧USA。是更新应用失败，不是没检索到。|
|mab-0/4/q30/D|0|False|False|原始事实382已把baseball的国家改为Japan；该事实与旧160同时在上下文内，reader仍答旧USA。是更新应用失败，不是没检索到。|
|longmemeval-432/5c40ec5b/A|0|True|True|Twice与gold句子语义相同，A的e00478、C/D的e00470/e00478支持；E缺背景而拒答。|
|longmemeval-432/5c40ec5b/C|0|True|True|Twice与gold句子语义相同，A的e00478、C/D的e00470/e00478支持；E缺背景而拒答。|
|longmemeval-432/5c40ec5b/D|0|True|True|Twice与gold句子语义相同，A的e00478、C/D的e00470/e00478支持；E缺背景而拒答。|
|longmemeval-432/5c40ec5b/E|0|False|False|Twice与gold句子语义相同，A的e00478、C/D的e00470/e00478支持；E缺背景而拒答。|
|longmemeval-440/0ddfec37_abs/A|1|True|True|问题问autographed footballs，原文只有baseballs，拒答正确；D说明两者区别。|
|longmemeval-440/0ddfec37_abs/C|1|True|True|问题问autographed footballs，原文只有baseballs，拒答正确；D说明两者区别。|
|longmemeval-440/0ddfec37_abs/D|1|True|True|问题问autographed footballs，原文只有baseballs，拒答正确；D说明两者区别。|
|longmemeval-440/0ddfec37_abs/E|1|True|True|问题问autographed footballs，原文只有baseballs，拒答正确；D说明两者区别。|