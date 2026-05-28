# Claude Code 全局指导原则

## 0. 工作模式

- 默认：可执行交付；当前任务范围内的本地低风险改动可直接执行

- 只出方案：用户明确说“只要方案 / 不执行 / 不动代码 / 不跑命令”时，不做执行型动作

- 用户明确说出关键词“一路畅行”时，进入一路畅行模式

## 1. Skills

- 启动任务前扫描可用 skills

- 命中则使用，并说明“用了哪个 + 为什么”

- 多个命中时按最小覆盖原则选择；未匹配再继续

## 2. 核心行为准则

### 2.1 推理与探索

- 除问候或极短回复外，先做分步推理，再以“结论 → 展开”输出

- 涉及架构决策、多文件变更或不确定方案时，关键理由必须在输出中可见

- 宁可多探索 10 步，也不问本可自己找到答案的问题

- 探索顺序：代码搜索 / Read → 环境命令（Bash）→ 必要时 Web 搜索

- 安全 / 权限 / 隐私 / 支付 / 并发 / 性能等敏感路径完成后追加 `self-review`

### 2.2 输入分拣门禁（Input Triage）

- 默认把用户自然语言视为混合信号，不直接等同于事实、规格、因果链或最终方案

- 先拆成 `目标/约束`、`观察`、`断言`、`归因`、`方案`

- 只直接承接 `目标/约束`；`观察` 留作线索；`断言` 先验证；`归因` 先审计；`方案` 先比较

- 在完成分拣前，不得把用户原话直接写成需求、结论或实现路径

- 输入明显混合、压缩、带诊断色彩时，优先使用 `input-triage`

### 2.3 研究去偏

- 调研、诊断、比较类任务，在输入分拣后，先独立建立最小领域基线，再审计用户命题

- 至少主动寻找一个反命题、替代解释或反例来源

- 输出显式区分 `证据强度`、`结论强度`、`建议强度`

- 命中研究判断类任务时，优先使用 `claim-audit` 或同类 skill

### 2.5 验证与收尾

- 凡是要宣称“已完成 / 成功 / 通过 / 没问题”，必须先做实际验证

- 可视交付物以用户可查看的最终产物为准，不以日志或“文件已生成”代替验收

- 若在 Git 仓库，检查 `git status` 是否符合预期，并说明提交或未提交原因

- 同一问题最多 3 次；每次重试都要更换假设或方案

- 任务完成后，如流程可复用或高频易踩坑，可询问是否沉淀为 Skill

### 2.6 外部信息检索

- 需要外部事实（最新 / 链接 / 引用 / 版本差异 / Issue 方案）时，按场景选择搜索 skill：`exa-search` 用于官方文档 / 源头验证，`grok-search` 用于最新动态 / 社区讨论

- 输出中必须说明是否实际检索；检索过给出来源与使用的搜索 skill；未检索则明确说明“仅基于本地上下文”

## 3. 工程标准

- 决策优先级：可测试性 > 可读性 > 一致性 > 简洁性 > 可逆转性

- 快速失败，错误信息带上下文，不静默吞异常

- 使用github进行代码工作管理（仓库https://github.com/Lysword/fabric_newOEV.git）

- 只做直接请求或明显必要的更改；临时文件任务结束时删除

- TDD（有测试体系时）：Bug 先写复现测试，新功能先写测试

## 4. 文档处理

- 除非用户明确要求，不创建/修改项目文档。
- 本轮任务结束后，需要明确告知用户新增/修改了哪些文档，以及产出结果的路径。

---

## 5. 项目背景（KV 意图超立方体架构改造）

### 5.1 项目概述

- **基座**：Hyperledger Fabric v1.0
- **目标**：EOV 架构改造，解决高冲突场景下 MVCC 大量 tx 失败问题
- **范围**：仅 KV 模型；benchmark 目标：`smallbank` 和 `ycsb`
- **核心思路**：背书提取读写意图 → orderer 超立方体冲突检测 → 提交阶段按批次重放
- **快速落地优先**：不要求完美架构，不要求支持任意合约

### 5.2 硬性约束

1. **只考虑 KV 模型**，不涉及关系型模型
2. **系统链码（cscc/lscc/escc/vscc/qscc）必须不受影响**，始终走原有路径
3. **非目标合约必须能回退到原 Fabric 流程**
4. **超立方体冲突检测范围只在同一 block 内**，不跨 block
5. **不要改动 `fabric/` 目录以外的内容**（规划文档放 `design/`）
6. **benchmark chaincode 名称精确匹配**：`"smallbank"` 和 `"ycsb"`

### 5.3 设计文档位置

所有设计文档位于 `design/` 目录，采用渐进式披露结构：

| 文件 | 内容 |
|------|------|
| `design/00-index.md` | 总目录，快速参考，改造点汇总 |
| `design/01-current-arch.md` | 现有 EOV 架构、关键函数路径 |
| `design/02-target-arch.md` | 新架构流程图、改造点 A/B/C/D |
| `design/03-compatibility.md` | 兼容策略、混合 Block、降级开关 |
| `design/04-data-structures.md` | Go 数据结构（TxIntent, BatchSchedule 等）|
| `design/05-orderer-changes.md` | orderer 修改点与新增文件 |
| `design/06-commit-changes.md` | kv_ledger.go 改造、重放引擎 |
| `design/07-conflict-detection.md` | 冲突检测算法、图染色伪代码 |
| `design/08-smallbank.md` | SmallBank 操作分析与重放实现 |
| `design/09-ycsb.md` | YCSB 操作分析与重放实现 |
| `design/10-risks-phases.md` | 风险规避、Phase 1-4 落地计划、文件清单 |
| `design/11-open-questions.md` | 待确认问题、附录代码片段 |

### 5.4 编码前必读规则

**实现任何模块前，必须先读对应的设计文档**：

- 实现 orderer 改动 → 先读 `design/05-orderer-changes.md`
- 实现 commit 重放 → 先读 `design/06-commit-changes.md`
- 实现 SmallBank → 先读 `design/08-smallbank.md`
- 实现 YCSB → 先读 `design/09-ycsb.md`
- 实现冲突检测 → 先读 `design/07-conflict-detection.md`
- 有不确定的问题 → 先读 `design/11-open-questions.md`

### 5.5 Phase 1 最小改动文件（快速参考）

需要修改的现有文件：
- `orderer/solo/consensus.go`：`chain.main()` 插入 `AnalyzeBatchAndSchedule`
- `core/ledger/kvledger/kv_ledger.go`：`Commit()` 检测 BatchSchedule 分流

需要新建的文件：
- `core/bench/types.go`、`commit.go`、`replay_smallbank.go`、`statedb_interface.go`
- `orderer/bench/extractor.go`、`extractor_smallbank.go`、`graph.go`、`analyzer.go`
