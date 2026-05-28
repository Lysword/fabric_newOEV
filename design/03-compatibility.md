# 03 — 新旧流程兼容策略

> 上级目录：[00-index.md](00-index.md) | 前置：[02-target-arch.md](02-target-arch.md)

---

## 3.1 合约分流规则

通过 `chaincode_id.Name` 字段识别合约类型（精确匹配，不支持通配符）：

```go
// orderer/bench/extractor.go

const (
    BenchmarkSmallBank = "smallbank"
    BenchmarkYCSB      = "ycsb"
)

func IsBenchmarkChaincode(name string) bool {
    return name == BenchmarkSmallBank || name == BenchmarkYCSB
}
```

分流时机：
- **Orderer 侧**：`CreateNextBlock` 后，遍历 tx 时检查 chaincode 名称
- **Peer commit 侧**：读取 BatchSchedule 后，对每笔 tx 检查 chaincode 名称

---

## 3.2 兼容矩阵

| TX 类型 | 背书阶段 | Orderer 分析 | Commit 路径 |
|---------|---------|-------------|------------|
| smallbank / ycsb | 原有路径（仍执行合约，生成 rwset） | 提取 intent，参与染色 | 批次重放，跳过 MVCC |
| 系统链码（cscc/lscc/escc/vscc/qscc） | 原有路径 | 跳过 intent 提取，标记为 `non-benchmark` | 原有 MVCC 路径 |
| 普通用户链码（非 benchmark） | 原有路径 | 跳过 intent 提取，标记为 `non-benchmark` | 原有 MVCC 路径 |

---

## 3.3 混合 Block 处理

同一 block 内可能同时包含 benchmark tx 和普通 tx：

**Orderer 侧**：
- 只对 benchmark tx 构建 hypercube 参与图染色
- 普通 tx（系统链码、用户链码）不进入 ConflictGraph
- BatchSchedule 只包含 benchmark tx 的 txID
- 普通 tx 在 BatchSchedule 中不出现

**Peer Commit 侧**：
- 有 BatchSchedule → benchmark tx 走重放路径，普通 tx 走原 MVCC 路径
- 普通 tx 不在 BatchSchedule 中 → 按原 txsFilter 处理
- 两类 tx 的写操作独立，peer 按 batch 顺序完成 benchmark tx 后，再处理普通 tx 的 MVCC 路径

**风险**：benchmark tx 和普通 tx 访问同一 key 时行为未定义。  
**规避**：benchmark（smallbank/ycsb）和系统链码访问的 key 命名空间天然不重叠（`account:` vs 系统 key），普通用户链码不与 benchmark 混合运行（测试脚本保证）。

---

## 3.4 降级开关

在 `orderer/localconfig/` 或通过 viper 配置添加环境变量：

```bash
FABRIC_BENCH_INTENT_ENABLED=true   # 启用新架构（默认）
FABRIC_BENCH_INTENT_ENABLED=false  # 降级到原有 MVCC 路径
```

降级逻辑：
- 若为 false，`analyzeBatchAndSchedule` 直接返回 nil
- `WriteBlock(block, committers, nil)` → 原有行为
- Peer 的 `parseBatchSchedule` 返回 false → 走原 MVCC 路径
- 等效于完全未改造

---

## 3.5 系统链码保护检查清单

在开发中，需要验证以下场景系统链码仍正常工作：

- [ ] `lscc` deploy 链码：正常走原路径
- [ ] `cscc` join channel：正常走原路径
- [ ] `escc` 背书签名：不受影响
- [ ] `vscc` 背书策略验证：不受影响
- [ ] `qscc` 查询：只读，不经过 commit

---

*返回：[目录](00-index.md) | 前一篇：[02-target-arch.md](02-target-arch.md) | 下一篇：[04-data-structures.md](04-data-structures.md)*
