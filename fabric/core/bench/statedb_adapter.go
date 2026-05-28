// by lyj
package bench

import (
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/statedb"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/version"
)

// below by lyj

// StateDBAdapter 封装 VersionedDB，为重放引擎提供简单读写接口
// 支持 read-your-own-writes：先检查本地写缓存，再查底层 DB
type StateDBAdapter struct {
	db     statedb.VersionedDB
	batch  *statedb.UpdateBatch
	height *version.Height
}

// NewStateDBAdapter 创建适配器，height 用于写入的版本号
func NewStateDBAdapter(db statedb.VersionedDB, blockNum uint64, numTxs int) *StateDBAdapter {
	return &StateDBAdapter{
		db:     db,
		batch:  statedb.NewUpdateBatch(),
		height: version.NewHeight(blockNum, uint64(numTxs-1)),
	}
}

// GetState 读取指定 namespace/key 的值（先查本地写缓存，再查底层 DB）
func (a *StateDBAdapter) GetState(namespace, key string) ([]byte, error) {
	if vv := a.batch.Get(namespace, key); vv != nil {
		return vv.Value, nil
	}
	vv, err := a.db.GetState(namespace, key)
	if err != nil {
		return nil, err
	}
	if vv == nil {
		return nil, nil
	}
	return vv.Value, nil
}

// PutState 写入单个 key（累积到内部 batch，Flush 时一次性提交）
func (a *StateDBAdapter) PutState(namespace, key string, value []byte) {
	a.batch.Put(namespace, key, value, a.height)
}

// Flush 将累积的所有写入一次性提交到底层 stateDB
func (a *StateDBAdapter) Flush() error {
	return a.db.ApplyUpdates(a.batch, a.height)
}

// end by lyj
