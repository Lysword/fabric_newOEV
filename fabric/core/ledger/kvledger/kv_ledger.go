/*
Copyright IBM Corp. 2016 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kvledger

import (
	"errors"
	"fmt"

	"github.com/hyperledger/fabric/common/flogging"
	commonledger "github.com/hyperledger/fabric/common/ledger"
	"github.com/hyperledger/fabric/common/ledger/blkstorage"
	"github.com/hyperledger/fabric/core/bench" // below by lyj // end by lyj
	"github.com/hyperledger/fabric/core/ledger"
	"github.com/hyperledger/fabric/core/ledger/kvledger/history/historydb"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/statedb"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/txmgr"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/txmgr/lockbasedtxmgr"
	"github.com/hyperledger/fabric/core/ledger/ledgerconfig"
	"github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/protos/peer"
)

var logger = flogging.MustGetLogger("kvledger")

// KVLedger provides an implementation of `ledger.PeerLedger`.
// This implementation provides a key-value based data model
type kvLedger struct {
	ledgerID    string
	blockStore  blkstorage.BlockStore
	txtmgmt     txmgr.TxMgr
	historyDB   historydb.HistoryDB
	versionedDB statedb.VersionedDB // below by lyj: benchmark 重放直接操作 stateDB // end by lyj
}

// NewKVLedger constructs new `KVLedger`
func newKVLedger(ledgerID string, blockStore blkstorage.BlockStore,
	versionedDB statedb.VersionedDB, historyDB historydb.HistoryDB) (*kvLedger, error) {

	logger.Debugf("Creating KVLedger ledgerID=%s: ", ledgerID)

	//Initialize transaction manager using state database
	var txmgmt txmgr.TxMgr
	txmgmt = lockbasedtxmgr.NewLockBasedTxMgr(versionedDB)

	// Create a kvLedger for this chain/ledger, which encasulates the underlying
	// id store, blockstore, txmgr (state database), history database
	// below by lyj
	l := &kvLedger{
		ledgerID:    ledgerID,
		blockStore:  blockStore,
		txtmgmt:     txmgmt,
		historyDB:   historyDB,
		versionedDB: versionedDB,
	}
	// end by lyj

	//Recover both state DB and history DB if they are out of sync with block storage
	if err := l.recoverDBs(); err != nil {
		panic(fmt.Errorf(`Error during state DB recovery:%s`, err))
	}

	return l, nil
}

//Recover the state database and history database (if exist)
//by recommitting last valid blocks
func (l *kvLedger) recoverDBs() error {
	logger.Debugf("Entering recoverDB()")
	//If there is no block in blockstorage, nothing to recover.
	info, _ := l.blockStore.GetBlockchainInfo()
	if info.Height == 0 {
		logger.Debug("Block storage is empty.")
		return nil
	}
	lastAvailableBlockNum := info.Height - 1
	recoverables := []recoverable{l.txtmgmt, l.historyDB}
	recoverers := []*recoverer{}
	for _, recoverable := range recoverables {
		recoverFlag, firstBlockNum, err := recoverable.ShouldRecover(lastAvailableBlockNum)
		if err != nil {
			return err
		}
		if recoverFlag {
			recoverers = append(recoverers, &recoverer{firstBlockNum, recoverable})
		}
	}
	if len(recoverers) == 0 {
		return nil
	}
	if len(recoverers) == 1 {
		return l.recommitLostBlocks(recoverers[0].firstBlockNum, lastAvailableBlockNum, recoverers[0].recoverable)
	}

	// both dbs need to be recovered
	if recoverers[0].firstBlockNum > recoverers[1].firstBlockNum {
		// swap (put the lagger db at 0 index)
		recoverers[0], recoverers[1] = recoverers[1], recoverers[0]
	}
	if recoverers[0].firstBlockNum != recoverers[1].firstBlockNum {
		// bring the lagger db equal to the other db
		if err := l.recommitLostBlocks(recoverers[0].firstBlockNum, recoverers[1].firstBlockNum-1,
			recoverers[0].recoverable); err != nil {
			return err
		}
	}
	// get both the db upto block storage
	return l.recommitLostBlocks(recoverers[1].firstBlockNum, lastAvailableBlockNum,
		recoverers[0].recoverable, recoverers[1].recoverable)
}

//recommitLostBlocks retrieves blocks in specified range and commit the write set to either
//state DB or history DB or both
func (l *kvLedger) recommitLostBlocks(firstBlockNum uint64, lastBlockNum uint64, recoverables ...recoverable) error {
	var err error
	var block *common.Block
	for blockNumber := firstBlockNum; blockNumber <= lastBlockNum; blockNumber++ {
		if block, err = l.GetBlockByNumber(blockNumber); err != nil {
			return err
		}
		for _, r := range recoverables {
			if err := r.CommitLostBlock(block); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetTransactionByID retrieves a transaction by id
func (l *kvLedger) GetTransactionByID(txID string) (*peer.ProcessedTransaction, error) {

	tranEnv, err := l.blockStore.RetrieveTxByID(txID)
	if err != nil {
		return nil, err
	}

	txVResult, err := l.blockStore.RetrieveTxValidationCodeByTxID(txID)

	if err != nil {
		return nil, err
	}

	processedTran := &peer.ProcessedTransaction{TransactionEnvelope: tranEnv, ValidationCode: int32(txVResult)}
	return processedTran, nil
}

// GetBlockchainInfo returns basic info about blockchain
func (l *kvLedger) GetBlockchainInfo() (*common.BlockchainInfo, error) {
	return l.blockStore.GetBlockchainInfo()
}

// GetBlockByNumber returns block at a given height
// blockNumber of  math.MaxUint64 will return last block
func (l *kvLedger) GetBlockByNumber(blockNumber uint64) (*common.Block, error) {
	return l.blockStore.RetrieveBlockByNumber(blockNumber)

}

// GetBlocksIterator returns an iterator that starts from `startBlockNumber`(inclusive).
// The iterator is a blocking iterator i.e., it blocks till the next block gets available in the ledger
// ResultsIterator contains type BlockHolder
func (l *kvLedger) GetBlocksIterator(startBlockNumber uint64) (commonledger.ResultsIterator, error) {
	return l.blockStore.RetrieveBlocks(startBlockNumber)

}

// GetBlockByHash returns a block given it's hash
func (l *kvLedger) GetBlockByHash(blockHash []byte) (*common.Block, error) {
	return l.blockStore.RetrieveBlockByHash(blockHash)
}

// GetBlockByTxID returns a block which contains a transaction
func (l *kvLedger) GetBlockByTxID(txID string) (*common.Block, error) {
	return l.blockStore.RetrieveBlockByTxID(txID)
}

func (l *kvLedger) GetTxValidationCodeByTxID(txID string) (peer.TxValidationCode, error) {
	return l.blockStore.RetrieveTxValidationCodeByTxID(txID)
}

//Prune prunes the blocks/transactions that satisfy the given policy
func (l *kvLedger) Prune(policy commonledger.PrunePolicy) error {
	return errors.New("Not yet implemented")
}

// NewTxSimulator returns new `ledger.TxSimulator`
func (l *kvLedger) NewTxSimulator() (ledger.TxSimulator, error) {
	return l.txtmgmt.NewTxSimulator()
}

// NewQueryExecutor gives handle to a query executor.
// A client can obtain more than one 'QueryExecutor's for parallel execution.
// Any synchronization should be performed at the implementation level if required
func (l *kvLedger) NewQueryExecutor() (ledger.QueryExecutor, error) {
	return l.txtmgmt.NewQueryExecutor()
}

// NewHistoryQueryExecutor gives handle to a history query executor.
// A client can obtain more than one 'HistoryQueryExecutor's for parallel execution.
// Any synchronization should be performed at the implementation level if required
// Pass the ledger blockstore so that historical values can be looked up from the chain
func (l *kvLedger) NewHistoryQueryExecutor() (ledger.HistoryQueryExecutor, error) {
	return l.historyDB.NewHistoryQueryExecutor(l.blockStore)
}

// Commit commits the valid block (returned in the method RemoveInvalidTransactionsAndPrepare) and related state changes
func (l *kvLedger) Commit(block *common.Block) error {
	var err error
	blockNo := block.Header.Number

	// below by lyj
	schedule, hasBatchSchedule := bench.ParseBatchSchedule(block)
	if hasBatchSchedule {
		return l.commitWithBatchSchedule(block, schedule)
	}
	// end by lyj

	logger.Debugf("Channel [%s]: Validating block [%d]", l.ledgerID, blockNo)
	err = l.txtmgmt.ValidateAndPrepare(block, true)
	if err != nil {
		return err
	}

	logger.Debugf("Channel [%s]: Committing block [%d] to storage", l.ledgerID, blockNo)
	if err = l.blockStore.AddBlock(block); err != nil {
		return err
	}
	logger.Infof("Channel [%s]: Created block [%d] with %d transaction(s)", l.ledgerID, block.Header.Number, len(block.Data.Data))

	logger.Debugf("Channel [%s]: Committing block [%d] transactions to state database", l.ledgerID, blockNo)
	if err = l.txtmgmt.Commit(); err != nil {
		panic(fmt.Errorf(`Error during commit to txmgr:%s`, err))
	}

	// History database could be written in parallel with state and/or async as a future optimization
	if ledgerconfig.IsHistoryDBEnabled() {
		logger.Debugf("Channel [%s]: Committing block [%d] transactions to history database", l.ledgerID, blockNo)
		if err := l.historyDB.Commit(block); err != nil {
			panic(fmt.Errorf(`Error during commit to history db:%s`, err))
		}
	}

	return nil
}

// below by lyj

// commitWithBatchSchedule benchmark block 的提交路径：
// 1. 正常 MVCC 验证（非 benchmark tx 仍走原路径）
// 2. 存储 block
// 3. 提交 MVCC 验证结果（非 benchmark tx 写集）
// 4. 按 BatchSchedule 重放 benchmark tx 并直接写入 stateDB
func (l *kvLedger) commitWithBatchSchedule(block *common.Block, schedule *bench.BatchSchedule) error {
	blockNo := block.Header.Number
	logger.Infof("Channel [%s]: Block [%d] has BatchSchedule with %d batches, using bench commit path",
		l.ledgerID, blockNo, len(schedule.Batches))

	// Step 1: MVCC 验证所有 tx（benchmark tx 冲突会被标记 INVALID，后续重放会覆盖）
	logger.Debugf("Channel [%s]: Validating block [%d] (MVCC)", l.ledgerID, blockNo)
	if err := l.txtmgmt.ValidateAndPrepare(block, true); err != nil {
		return err
	}

	// Step 2: 存储 block
	logger.Debugf("Channel [%s]: Committing block [%d] to storage", l.ledgerID, blockNo)
	if err := l.blockStore.AddBlock(block); err != nil {
		return err
	}
	logger.Infof("Channel [%s]: Created block [%d] with %d transaction(s)", l.ledgerID, blockNo, len(block.Data.Data))

	// Step 3: 提交 MVCC 验证结果（非 benchmark tx 的写集；benchmark tx 若通过 MVCC 也会写入，后续重放覆盖）
	logger.Debugf("Channel [%s]: Committing block [%d] validated writes to state database", l.ledgerID, blockNo)
	if err := l.txtmgmt.Commit(); err != nil {
		panic(fmt.Errorf(`Error during commit to txmgr:%s`, err))
	}

	// Step 4: 按 batch 顺序重放 benchmark tx，结果直接写入 stateDB
	logger.Debugf("Channel [%s]: Replaying benchmark tx for block [%d]", l.ledgerID, blockNo)
	if err := bench.CommitBenchmarkTxs(block, schedule, l.versionedDB); err != nil {
		logger.Warningf("Channel [%s]: Benchmark replay error in block [%d]: %s", l.ledgerID, blockNo, err)
	}

	// Step 5: History DB
	if ledgerconfig.IsHistoryDBEnabled() {
		logger.Debugf("Channel [%s]: Committing block [%d] transactions to history database", l.ledgerID, blockNo)
		if err := l.historyDB.Commit(block); err != nil {
			panic(fmt.Errorf(`Error during commit to history db:%s`, err))
		}
	}

	return nil
}

// end by lyj

// Close closes `KVLedger`
func (l *kvLedger) Close() {
	l.blockStore.Shutdown()
	l.txtmgmt.Shutdown()
}
