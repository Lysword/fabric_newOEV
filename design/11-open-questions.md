# 11 — 待确认问题与附录代码

> 上级目录：[00-index.md](00-index.md) | 前置：[10-risks-phases.md](10-risks-phases.md)

---

## 11.1 待确认问题

在开始编码前，需要与导师/需求方确认以下问题：

1. **背书阶段是否需要跳过真实执行**？  
   当前方案（Phase 1）仍然执行合约，只是提交时跳过 MVCC 改为重放。若需要背书阶段也不真实执行（提升背书 TPS），需要额外修改 `simulateProposal()`，可能影响 ESCC 签名内容，有兼容性风险。

2. **非 benchmark tx（普通链码）如何处理写集与新路径写集的时序**？  
   当 block 内同时存在 benchmark tx 和普通 tx 时，两类 tx 更新同一 key 的行为需要明确定义。当前方案：批次写入（benchmark tx）在非批次写入（普通 MVCC tx）之前执行。如果它们确实访问同一 key，行为未定义。需要确认是否允许混合 block 中跨类型 key 访问。

3. **orderer 在多 orderer 场景（Kafka）下 BatchSchedule 的一致性**？  
   本文档基于 Solo orderer。Kafka 场景下，BatchSchedule 由接收交付的 orderer 节点计算。由于图染色算法是确定性的（固定 txID 字典序），不同 orderer 节点对同一 batch 应产生相同结果，但需要验证。

4. **是否需要保留原有 rwset 的 MVCC 验证作为 double-check**？  
   完全跳过 MVCC 后，如果重放计算出错（如合约逻辑 bug），没有版本检查作为保护。是否需要在 debug 模式下保留 MVCC 作为正确性验证？

5. **benchmark 测试 chaincode 名称是否固定为 "smallbank" / "ycsb"**？  
   若测试脚本部署时使用不同名称（如 "smallbank_cc"），需要配置映射关系。建议通过环境变量或配置文件指定 benchmark chaincode 名称列表。

---

## 11.2 附录：原型实现快速参考

### A. 解析 Envelope 获取 chaincode 调用参数（orderer 侧完整实现）

```go
import (
    "github.com/hyperledger/fabric/protos/utils"
    pb "github.com/hyperledger/fabric/protos/peer"
    "github.com/golang/protobuf/proto"
)

func extractCCInvocation(envBytes []byte) (ccName, funcName string, args []string, err error) {
    env, err := utils.GetEnvelopeFromBlock(envBytes)
    if err != nil { return }
    payload, err := utils.GetPayload(env)
    if err != nil { return }
    hdrExt, err := utils.GetChaincodeHeaderExtension(payload.Header)
    if err != nil { return }
    ccName = hdrExt.ChaincodeId.Name

    tx, err := utils.GetTransaction(payload.Data)
    if err != nil { return }
    cap, err := utils.GetChaincodeActionPayload(tx.Actions[0].Payload)
    if err != nil { return }
    cpp, err := utils.GetChaincodeProposalPayload(cap.ChaincodeProposalPayload)
    if err != nil { return }
    cis := &pb.ChaincodeInvocationSpec{}
    if err = proto.Unmarshal(cpp.Input, cis); err != nil { return }

    rawArgs := cis.ChaincodeSpec.Input.Args
    if len(rawArgs) < 1 { return }
    funcName = string(rawArgs[0])
    for _, a := range rawArgs[1:] {
        args = append(args, string(a))
    }
    return
}
```

### B. 读写 BatchSchedule（peer 侧完整实现）

```go
// 写（orderer WriteBlock 第三个参数）
scheduleBytes, _ := json.Marshal(schedule)
ch.support.WriteBlock(block, committers, scheduleBytes)

// 读（peer Commit 阶段）
func parseBatchSchedule(block *common.Block) (*BatchSchedule, bool) {
    if len(block.Metadata.Metadata) <= int(common.BlockMetadataIndex_ORDERER) {
        return nil, false
    }
    ordererMeta := &common.Metadata{}
    if err := proto.Unmarshal(
        block.Metadata.Metadata[common.BlockMetadataIndex_ORDERER],
        ordererMeta,
    ); err != nil {
        return nil, false
    }
    if len(ordererMeta.Value) == 0 { return nil, false }
    var schedule BatchSchedule
    if err := json.Unmarshal(ordererMeta.Value, &schedule); err != nil {
        return nil, false
    }
    if schedule.Version != "v1" { return nil, false }
    return &schedule, true
}
```

### C. 从 block 中找到指定 txID 的 Envelope 字节

```go
func findTxInBlock(block *common.Block, txID string) []byte {
    for _, envBytes := range block.Data.Data {
        env, err := utils.GetEnvelopeFromBlock(envBytes)
        if err != nil { continue }
        payload, err := utils.GetPayload(env)
        if err != nil { continue }
        chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
        if err != nil { continue }
        if chdr.TxId == txID { return envBytes }
    }
    return nil
}
```

### D. WriteBlock 第三个参数注入点（chainsupport.go:274 参考）

```go
// orderer/multichain/chainsupport.go
func (cs *chainSupport) WriteBlock(block *cb.Block, committers []filter.Committer, encodedMetadataValue []byte) *cb.Block {
    for i, c := range committers {
        if err := c.Commit(); err != nil {
            ...
        }
    }

    // 关键：若 encodedMetadataValue != nil，写入 ORDERER slot
    if encodedMetadataValue != nil {
        block.Metadata.Metadata[cb.BlockMetadataIndex_ORDERER] = utils.MarshalOrPanic(
            &cb.Metadata{Value: encodedMetadataValue},
        )
    }

    cs.addBlockSignature(block)
    cs.addLastConfigSignature(block)

    err := cs.ledger.Append(block)
    ...
    return block
}
```

---

*返回：[目录](00-index.md) | 前一篇：[10-risks-phases.md](10-risks-phases.md)*
