// Copyright 2020 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package t8ntool

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/consensus/misc/eip4844"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/overlay"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/state/snapshot"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-verkle"
	"golang.org/x/crypto/sha3"
)

type Prestate struct {
	Env stEnv                         `json:"env"`
	Pre core.GenesisAlloc             `json:"pre"`
	VKT map[common.Hash]hexutil.Bytes `json:"vkt,omitempty"`
}

// ExecutionResult contains the execution status after running a state test, any
// error that might have occurred and a dump of the final state if requested.
type ExecutionResult struct {
	StateRoot            common.Hash           `json:"stateRoot"`
	TxRoot               common.Hash           `json:"txRoot"`
	ReceiptRoot          common.Hash           `json:"receiptsRoot"`
	LogsHash             common.Hash           `json:"logsHash"`
	Bloom                types.Bloom           `json:"logsBloom"        gencodec:"required"`
	Receipts             types.Receipts        `json:"receipts"`
	Rejected             []*rejectedTx         `json:"rejected,omitempty"`
	Difficulty           *math.HexOrDecimal256 `json:"currentDifficulty" gencodec:"required"`
	GasUsed              math.HexOrDecimal64   `json:"gasUsed"`
	BaseFee              *math.HexOrDecimal256 `json:"currentBaseFee,omitempty"`
	WithdrawalsRoot      *common.Hash          `json:"withdrawalsRoot,omitempty"`
	CurrentExcessBlobGas *math.HexOrDecimal64  `json:"currentExcessBlobGas,omitempty"`
	CurrentBlobGasUsed   *math.HexOrDecimal64  `json:"currentBlobGasUsed,omitempty"`

	// Verkle witness
	VerkleProof *verkle.VerkleProof `json:"verkleProof,omitempty"`
	StateDiff   verkle.StateDiff    `json:"stateDiff,omitempty"`

	// Values to test the verkle conversion
	CurrentAccountAddress *common.Address `json:"currentConversionAddress,omitempty" gencodec:"optional"`
	CurrentSlotHash       *common.Hash    `json:"currentConversionSlotHash,omitempty" gencodec:"optional"`
	Started               *bool           `json:"currentConversionStarted,omitempty" gencodec:"optional"`
	Ended                 *bool           `json:"currentConversionEnded,omitempty" gencodec:"optional"`
	StorageProcessed      *bool           `json:"currentConversionStorageProcessed,omitempty" gencodec:"optional"`
}

type ommer struct {
	Delta   uint64         `json:"delta"`
	Address common.Address `json:"address"`
}

//go:generate go run github.com/fjl/gencodec -type stEnv -field-override stEnvMarshaling -out gen_stenv.go
type stEnv struct {
	Coinbase            common.Address                      `json:"currentCoinbase"   gencodec:"required"`
	Difficulty          *big.Int                            `json:"currentDifficulty"`
	Random              *big.Int                            `json:"currentRandom"`
	ParentDifficulty    *big.Int                            `json:"parentDifficulty"`
	ParentBaseFee       *big.Int                            `json:"parentBaseFee,omitempty"`
	ParentGasUsed       uint64                              `json:"parentGasUsed,omitempty"`
	ParentGasLimit      uint64                              `json:"parentGasLimit,omitempty"`
	GasLimit            uint64                              `json:"currentGasLimit"   gencodec:"required"`
	Number              uint64                              `json:"currentNumber"     gencodec:"required"`
	Timestamp           uint64                              `json:"currentTimestamp"  gencodec:"required"`
	ParentTimestamp     uint64                              `json:"parentTimestamp,omitempty"`
	BlockHashes         map[math.HexOrDecimal64]common.Hash `json:"blockHashes,omitempty"`
	Ommers              []ommer                             `json:"ommers,omitempty"`
	Withdrawals         []*types.Withdrawal                 `json:"withdrawals,omitempty"`
	BaseFee             *big.Int                            `json:"currentBaseFee,omitempty"`
	ParentUncleHash     common.Hash                         `json:"parentUncleHash"`
	ExcessBlobGas       *uint64                             `json:"excessBlobGas,omitempty"`
	ParentExcessBlobGas *uint64                             `json:"parentExcessBlobGas,omitempty"`
	ParentBlobGasUsed   *uint64                             `json:"parentBlobGasUsed,omitempty"`
	ParentHash          *common.Hash                        `json:"parentHash,omitempty"`

	// Values to test the verkle conversion
	CurrentAccountAddress *common.Address `json:"currentConversionAddress" gencodec:"optional"`
	CurrentSlotHash       *common.Hash    `json:"currentConversionSlotHash" gencodec:"optional"`
	Started               *bool           `json:"currentConversionStarted" gencodec:"optional"`
	Ended                 *bool           `json:"currentConversionEnded" gencodec:"optional"`
	StorageProcessed      *bool           `json:"currentConversionStorageProcessed" gencodec:"optional"`
}

type stEnvMarshaling struct {
	Coinbase            common.UnprefixedAddress
	Difficulty          *math.HexOrDecimal256
	Random              *math.HexOrDecimal256
	ParentDifficulty    *math.HexOrDecimal256
	ParentBaseFee       *math.HexOrDecimal256
	ParentGasUsed       math.HexOrDecimal64
	ParentGasLimit      math.HexOrDecimal64
	GasLimit            math.HexOrDecimal64
	Number              math.HexOrDecimal64
	Timestamp           math.HexOrDecimal64
	ParentTimestamp     math.HexOrDecimal64
	BaseFee             *math.HexOrDecimal256
	ExcessBlobGas       *math.HexOrDecimal64
	ParentExcessBlobGas *math.HexOrDecimal64
	ParentBlobGasUsed   *math.HexOrDecimal64

	// Values to test the verkle conversion
	CurrentAccountAddress *common.UnprefixedAddress
	CurrentSlotHash       *common.UnprefixedHash
	Started               *bool
	Ended                 *bool
	StorageProcessed      *bool
}

type rejectedTx struct {
	Index int    `json:"index"`
	Err   string `json:"error"`
}

// Apply applies a set of transactions to a pre-state
func (pre *Prestate) Apply(vmConfig vm.Config, chainConfig *params.ChainConfig,
	txs types.Transactions, miningReward int64,
	getTracerFn func(txIndex int, txHash common.Hash) (tracer vm.EVMLogger, err error)) (*state.StateDB, *ExecutionResult, error) {
	// Capture errors for BLOCKHASH operation, if we haven't been supplied the
	// required blockhashes
	var hashError error
	getHash := func(num uint64) common.Hash {
		if pre.Env.BlockHashes == nil {
			hashError = fmt.Errorf("getHash(%d) invoked, no blockhashes provided", num)
			return common.Hash{}
		}
		h, ok := pre.Env.BlockHashes[math.HexOrDecimal64(num)]
		if !ok {
			hashError = fmt.Errorf("getHash(%d) invoked, blockhash for that block not provided", num)
		}
		return h
	}
	var (
		statedb     = MakePreState(rawdb.NewMemoryDatabase(), chainConfig, pre, chainConfig.IsPrague(big.NewInt(int64(pre.Env.Number)), pre.Env.Timestamp))
		vtrpre      *trie.VerkleTrie
		signer      = types.MakeSigner(chainConfig, new(big.Int).SetUint64(pre.Env.Number), pre.Env.Timestamp)
		gaspool     = new(core.GasPool)
		blockHash   = common.Hash{0x13, 0x37}
		rejectedTxs []*rejectedTx
		includedTxs types.Transactions
		gasUsed     = uint64(0)
		receipts    = make(types.Receipts, 0)
		txIndex     = 0
	)
	gaspool.AddGas(pre.Env.GasLimit)
	vmContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		Coinbase:    pre.Env.Coinbase,
		BlockNumber: new(big.Int).SetUint64(pre.Env.Number),
		Time:        pre.Env.Timestamp,
		Difficulty:  pre.Env.Difficulty,
		GasLimit:    pre.Env.GasLimit,
		GetHash:     getHash,
	}
	// Save pre verkle tree to build the proof at the end
	if pre.VKT != nil && len(pre.VKT) > 0 {
		switch tr := statedb.GetTrie().(type) {
		case *trie.VerkleTrie:
			vtrpre = tr.Copy()
		case *trie.TransitionTrie:
			vtrpre = tr.Overlay().Copy()
		default:
			panic("invalid trie type")
		}
	}
	// If currentBaseFee is defined, add it to the vmContext.
	if pre.Env.BaseFee != nil {
		vmContext.BaseFee = new(big.Int).Set(pre.Env.BaseFee)
	}
	// If random is defined, add it to the vmContext.
	if pre.Env.Random != nil {
		rnd := common.BigToHash(pre.Env.Random)
		vmContext.Random = &rnd
	}
	// If excessBlobGas is defined, add it to the vmContext.
	if pre.Env.ExcessBlobGas != nil {
		vmContext.ExcessBlobGas = pre.Env.ExcessBlobGas
	} else {
		// If it is not explicitly defined, but we have the parent values, we try
		// to calculate it ourselves.
		parentExcessBlobGas := pre.Env.ParentExcessBlobGas
		parentBlobGasUsed := pre.Env.ParentBlobGasUsed
		if parentExcessBlobGas != nil && parentBlobGasUsed != nil {
			excessBlobGas := eip4844.CalcExcessBlobGas(*parentExcessBlobGas, *parentBlobGasUsed)
			vmContext.ExcessBlobGas = &excessBlobGas
		}
	}
	// If DAO is supported/enabled, we need to handle it here. In geth 'proper', it's
	// done in StateProcessor.Process(block, ...), right before transactions are applied.
	if chainConfig.DAOForkSupport &&
		chainConfig.DAOForkBlock != nil &&
		chainConfig.DAOForkBlock.Cmp(new(big.Int).SetUint64(pre.Env.Number)) == 0 {
		misc.ApplyDAOHardFork(statedb)
	}
	if chainConfig.IsPrague(big.NewInt(int64(pre.Env.Number)), pre.Env.Timestamp) {
		// insert all parent hashes in the contract
		for i := pre.Env.Number - 1; i > 0 && i >= pre.Env.Number-257; i-- {
			core.ProcessParentBlockHash(statedb, i, pre.Env.BlockHashes[math.HexOrDecimal64(i)])
		}
	}
	var blobGasUsed uint64
	for i, tx := range txs {
		if tx.Type() == types.BlobTxType && vmContext.ExcessBlobGas == nil {
			errMsg := "blob tx used but field env.ExcessBlobGas missing"
			log.Warn("rejected tx", "index", i, "hash", tx.Hash(), "error", errMsg)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, errMsg})
			continue
		}
		msg, err := core.TransactionToMessage(tx, signer, pre.Env.BaseFee)
		if err != nil {
			log.Warn("rejected tx", "index", i, "hash", tx.Hash(), "error", err)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
			continue
		}
		tracer, err := getTracerFn(txIndex, tx.Hash())
		if err != nil {
			return nil, nil, err
		}
		vmConfig.Tracer = tracer
		statedb.SetTxContext(tx.Hash(), txIndex)

		var (
			txContext = core.NewEVMTxContext(msg)
			snapshot  = statedb.Snapshot()
			prevGas   = gaspool.Gas()
		)
		evm := vm.NewEVM(vmContext, txContext, statedb, chainConfig, vmConfig)

		// (ret []byte, usedGas uint64, failed bool, err error)
		msgResult, err := core.ApplyMessage(evm, msg, gaspool)
		if err != nil {
			statedb.RevertToSnapshot(snapshot)
			log.Info("rejected tx", "index", i, "hash", tx.Hash(), "from", msg.From, "error", err)
			rejectedTxs = append(rejectedTxs, &rejectedTx{i, err.Error()})
			gaspool.SetGas(prevGas)
			continue
		}
		if tx.Type() == types.BlobTxType {
			blobGasUsed += params.BlobTxBlobGasPerBlob
		}
		includedTxs = append(includedTxs, tx)
		if hashError != nil {
			return nil, nil, NewError(ErrorMissingBlockhash, hashError)
		}
		gasUsed += msgResult.UsedGas

		// Receipt:
		{
			var root []byte
			if chainConfig.IsByzantium(vmContext.BlockNumber) {
				statedb.Finalise(true)
			} else {
				root = statedb.IntermediateRoot(chainConfig.IsEIP158(vmContext.BlockNumber)).Bytes()
			}

			// Create a new receipt for the transaction, storing the intermediate root and
			// gas used by the tx.
			receipt := &types.Receipt{Type: tx.Type(), PostState: root, CumulativeGasUsed: gasUsed}
			if msgResult.Failed() {
				receipt.Status = types.ReceiptStatusFailed
			} else {
				receipt.Status = types.ReceiptStatusSuccessful
			}
			receipt.TxHash = tx.Hash()
			receipt.GasUsed = msgResult.UsedGas

			// If the transaction created a contract, store the creation address in the receipt.
			if msg.To == nil {
				receipt.ContractAddress = crypto.CreateAddress(evm.TxContext.Origin, tx.Nonce())
			}

			// Set the receipt logs and create the bloom filter.
			receipt.Logs = statedb.GetLogs(tx.Hash(), vmContext.BlockNumber.Uint64(), blockHash)
			receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
			// These three are non-consensus fields:
			//receipt.BlockHash
			//receipt.BlockNumber
			receipt.TransactionIndex = uint(txIndex)
			receipts = append(receipts, receipt)
		}

		txIndex++
	}
	statedb.IntermediateRoot(chainConfig.IsEIP158(vmContext.BlockNumber))
	// Add mining reward? (-1 means rewards are disabled)
	if miningReward >= 0 {
		// Add mining reward. The mining reward may be `0`, which only makes a difference in the cases
		// where
		// - the coinbase self-destructed, or
		// - there are only 'bad' transactions, which aren't executed. In those cases,
		//   the coinbase gets no txfee, so isn't created, and thus needs to be touched
		var (
			blockReward = big.NewInt(miningReward)
			minerReward = new(big.Int).Set(blockReward)
			perOmmer    = new(big.Int).Div(blockReward, big.NewInt(32))
		)
		for _, ommer := range pre.Env.Ommers {
			// Add 1/32th for each ommer included
			minerReward.Add(minerReward, perOmmer)
			// Add (8-delta)/8
			reward := big.NewInt(8)
			reward.Sub(reward, new(big.Int).SetUint64(ommer.Delta))
			reward.Mul(reward, blockReward)
			reward.Div(reward, big.NewInt(8))
			statedb.AddBalance(ommer.Address, reward)
		}
		statedb.AddBalance(pre.Env.Coinbase, minerReward)
	}
	// Apply withdrawals
	for _, w := range pre.Env.Withdrawals {
		// Amount is in gwei, turn into wei
		amount := new(big.Int).Mul(new(big.Int).SetUint64(w.Amount), big.NewInt(params.GWei))
		statedb.AddBalance(w.Address, amount)
	}
	if chainConfig.IsPrague(big.NewInt(int64(pre.Env.Number)), pre.Env.Timestamp) {
		if err := overlay.OverlayVerkleTransition(statedb, common.Hash{}, chainConfig.OverlayStride); err != nil {
			log.Error("error performing the transition", "err", err)
		}
	}
	// Commit block
	root, err := statedb.Commit(vmContext.BlockNumber.Uint64(), chainConfig.IsEIP158(vmContext.BlockNumber))
	if err != nil {
		return nil, nil, NewError(ErrorEVM, fmt.Errorf("could not commit state: %v", err))
	}

	// Add the witness to the execution result
	var p *verkle.VerkleProof
	var k verkle.StateDiff
	if chainConfig.IsPrague(big.NewInt(int64(pre.Env.Number)), pre.Env.Timestamp) {
		keys := statedb.Witness().Keys()

		if len(keys) > 0 {
			p, k, err = trie.ProveAndSerialize(vtrpre, statedb.GetTrie().(*trie.VerkleTrie), keys, vtrpre.FlatdbNodeResolver)
			if err != nil {
				return nil, nil, fmt.Errorf("error generating verkle proof for block %d: %w", pre.Env.Number, err)
			}
		}
	}

	sdb := statedb.Database()
	execRs := &ExecutionResult{
		StateRoot:   root,
		TxRoot:      types.DeriveSha(includedTxs, trie.NewStackTrie(nil)),
		ReceiptRoot: types.DeriveSha(receipts, trie.NewStackTrie(nil)),
		Bloom:       types.CreateBloom(receipts),
		LogsHash:    rlpHash(statedb.Logs()),
		Receipts:    receipts,
		Rejected:    rejectedTxs,
		Difficulty:  (*math.HexOrDecimal256)(vmContext.Difficulty),
		GasUsed:     (math.HexOrDecimal64)(gasUsed),
		BaseFee:     (*math.HexOrDecimal256)(vmContext.BaseFee),
		VerkleProof: p,
		StateDiff:   k,
	}
	if pre.Env.Withdrawals != nil {
		h := types.DeriveSha(types.Withdrawals(pre.Env.Withdrawals), trie.NewStackTrie(nil))
		execRs.WithdrawalsRoot = &h
	}
	if vmContext.ExcessBlobGas != nil {
		execRs.CurrentExcessBlobGas = (*math.HexOrDecimal64)(vmContext.ExcessBlobGas)
		execRs.CurrentBlobGasUsed = (*math.HexOrDecimal64)(&blobGasUsed)
	}
	if chainConfig.IsPrague(big.NewInt(int64(pre.Env.Number)), pre.Env.Timestamp) {
		ended := sdb.Transitioned()
		if !ended {
			var (
				currentSlotHash = sdb.GetCurrentSlotHash()
				started         = sdb.InTransition()
			)
			execRs.CurrentAccountAddress = sdb.GetCurrentAccountAddress()
			execRs.CurrentSlotHash = &currentSlotHash
			execRs.Started = &started
		}
		execRs.Ended = &ended
	}
	// Re-create statedb instance with new root upon the updated database
	// for accessing latest states.
	statedb, err = state.New(root, statedb.Database(), nil)
	if err != nil {
		return nil, nil, NewError(ErrorEVM, fmt.Errorf("could not reopen state: %v", err))
	}
	return statedb, execRs, nil
}

func MakePreState(db ethdb.Database, chainConfig *params.ChainConfig, pre *Prestate, verkle bool) *state.StateDB {
	// Start with generating the MPT DB, which should be empty if it's post-verkle transition
	mptSdb := state.NewDatabaseWithConfig(db, &trie.Config{Preimages: true, Verkle: false})
	statedb, _ := state.New(types.EmptyRootHash, mptSdb, nil)

	// MPT pre is the same as the pre state for first conversion block
	for addr, a := range pre.Pre {
		statedb.SetCode(addr, a.Code)
		statedb.SetNonce(addr, a.Nonce)
		statedb.SetBalance(addr, a.Balance)
		for k, v := range a.Storage {
			statedb.SetState(addr, k, v)
		}
	}

	// If verkle mode started, establish the conversion
	if verkle {
		// Commit db an create a snapshot from it.
		mptRoot, err := statedb.Commit(0, false)
		if err != nil {
			panic(err)
		}
		rawdb.WritePreimages(mptSdb.DiskDB(), statedb.Preimages())
		mptSdb.TrieDB().WritePreimages()
		snaps, err := snapshot.New(snapshot.Config{AsyncBuild: false, CacheSize: 10}, mptSdb.DiskDB(), mptSdb.TrieDB(), mptRoot)
		if err != nil {
			panic(err)
		}
		if snaps == nil {
			panic("snapshot is nil")
		}
		snaps.Cap(mptRoot, 0)

		// reuse the backend db so that the snapshot can be enumerated
		sdb := mptSdb // := state.NewDatabaseWithConfig(db, &trie.Config{Verkle: true})

		// Load the conversion status
		sdb.InitTransitionStatus(pre.Env.Started != nil && *pre.Env.Started, pre.Env.Ended != nil && !*pre.Env.Ended)
		if pre.Env.CurrentAccountAddress != nil {
			sdb.SetCurrentAccountAddress(*pre.Env.CurrentAccountAddress)
		}
		if pre.Env.CurrentSlotHash != nil {
			sdb.SetCurrentSlotHash(*pre.Env.CurrentSlotHash)
		}
		if pre.Env.StorageProcessed != nil {
			sdb.SetStorageProcessed(*pre.Env.StorageProcessed)
		}

		// start the conversion on the first block
		if !sdb.InTransition() && !sdb.Transitioned() {
			sdb.StartVerkleTransition(mptRoot, mptRoot, chainConfig, chainConfig.PragueTime, mptRoot)
		}

		statedb, err = state.New(types.EmptyRootHash, sdb, nil)
		if err != nil {
			panic(err)
		}

		// Load verkle tree from prestate
		var vtr *trie.VerkleTrie
		switch tr := statedb.GetTrie().(type) {
		case *trie.VerkleTrie:
			vtr = tr
		case *trie.TransitionTrie:
			vtr = tr.Overlay()
		default:
			panic("invalid trie type")
		}

		// create the vkt, should be empty on first insert
		for k, v := range pre.VKT {
			values := make([][]byte, 256)
			values[k[31]] = make([]byte, 32)
			copy(values[k[31]], v)
			vtr.UpdateStem(k.Bytes(), values)
		}

		root, _ := statedb.Commit(0, false)
		statedb, err = state.New(root, sdb, snaps)
		if err != nil {
			panic(err)
		}
	}

	return statedb
}

func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

// calcDifficulty is based on ethash.CalcDifficulty. This method is used in case
// the caller does not provide an explicit difficulty, but instead provides only
// parent timestamp + difficulty.
// Note: this method only works for ethash engine.
func calcDifficulty(config *params.ChainConfig, number, currentTime, parentTime uint64,
	parentDifficulty *big.Int, parentUncleHash common.Hash) *big.Int {
	uncleHash := parentUncleHash
	if uncleHash == (common.Hash{}) {
		uncleHash = types.EmptyUncleHash
	}
	parent := &types.Header{
		ParentHash: common.Hash{},
		UncleHash:  uncleHash,
		Difficulty: parentDifficulty,
		Number:     new(big.Int).SetUint64(number - 1),
		Time:       parentTime,
	}
	return ethash.CalcDifficulty(config, currentTime, parent)
}
