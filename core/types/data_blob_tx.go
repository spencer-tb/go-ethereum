package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

type BlobTxMessage struct {
	ChainID             *big.Int
	Nonce               uint64
	GasTipCap           *big.Int // a.k.a. maxPriorityFeePerGas
	GasFeeCap           *big.Int // a.k.a. maxFeePerGas
	Gas                 uint64
	To                  *common.Address `rlp:"nil"` // nil means contract creation
	Value               *big.Int
	Data                []byte
	AccessList          AccessList
	MaxFeePerBlobGas    *big.Int // a.k.a. maxFeePerBlobGas
	BlobVersionedHashes []common.Hash

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

func (tx *BlobTxMessage) setChainID(chainID *big.Int) {
	if tx.ChainID == nil {
		tx.ChainID = new(big.Int)
	}
	tx.ChainID.Set(chainID)
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *BlobTxMessage) copy() TxData {
	cpy := &BlobTxMessage{
		Nonce: tx.Nonce,
		To:    tx.To,
		Data:  common.CopyBytes(tx.Data),
		Gas:   tx.Gas,
		// These are copied below.
		AccessList:          make(AccessList, len(tx.AccessList)),
		BlobVersionedHashes: make([]common.Hash, len(tx.BlobVersionedHashes)),
		Value:               new(big.Int),
		ChainID:             new(big.Int),
		GasTipCap:           new(big.Int),
		GasFeeCap:           new(big.Int),
		MaxFeePerBlobGas:    new(big.Int),
		V:                   new(big.Int),
		R:                   new(big.Int),
		S:                   new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	copy(cpy.BlobVersionedHashes, tx.BlobVersionedHashes)

	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.MaxFeePerBlobGas != nil {
		cpy.MaxFeePerBlobGas.Set(tx.MaxFeePerBlobGas)
	}
	if tx.V != nil {
		cpy.V.Set(tx.V)
	}
	if tx.R != nil {
		cpy.R.Set(tx.R)
	}
	if tx.S != nil {
		cpy.S.Set(tx.S)
	}
	return cpy
}

// accessors for innerTx.
func (tx *BlobTxMessage) txType() byte               { return BlobTxType }
func (tx *BlobTxMessage) chainID() *big.Int          { return tx.ChainID }
func (tx *BlobTxMessage) accessList() AccessList     { return tx.AccessList }
func (tx *BlobTxMessage) dataHashes() []common.Hash  { return tx.BlobVersionedHashes }
func (tx *BlobTxMessage) data() []byte               { return tx.Data }
func (tx *BlobTxMessage) gas() uint64                { return tx.Gas }
func (tx *BlobTxMessage) gasFeeCap() *big.Int        { return tx.GasFeeCap }
func (tx *BlobTxMessage) gasTipCap() *big.Int        { return tx.GasTipCap }
func (tx *BlobTxMessage) maxFeePerBlobGas() *big.Int { return tx.MaxFeePerBlobGas }
func (tx *BlobTxMessage) gasPrice() *big.Int         { return tx.GasFeeCap }
func (tx *BlobTxMessage) value() *big.Int            { return tx.Value }
func (tx *BlobTxMessage) nonce() uint64              { return tx.Nonce }
func (tx *BlobTxMessage) to() *common.Address        { return tx.To }

func (tx *BlobTxMessage) rawSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}
func (tx *BlobTxMessage) setSignatureValues(chainID, v, r, s *big.Int) {
	if tx.ChainID == nil {
		tx.ChainID = new(big.Int)
	}
	tx.ChainID.Set(chainID)
	if tx.V == nil {
		tx.V = new(big.Int)
	}
	tx.V.Set(v)
	if tx.R == nil {
		tx.R = new(big.Int)
	}
	tx.R.Set(r)
	if tx.S == nil {
		tx.S = new(big.Int)
	}
	tx.S.Set(s)
}

func (tx *BlobTxMessage) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

// fakeExponential approximates factor * e ** (num / denom) using a taylor expansion
// as described in the EIP-4844 spec.
func fakeExponential(factor, num, denom *big.Int) *big.Int {
	output := new(big.Int)
	numAccum := new(big.Int).Mul(factor, denom)
	for i := 1; numAccum.Sign() > 0; i++ {
		output.Add(output, numAccum)
		numAccum.Mul(numAccum, num)
		iBig := big.NewInt(int64(i))
		numAccum.Div(numAccum, iBig.Mul(iBig, denom))
	}
	return output.Div(output, denom)
}

// GetBlobGasPrice implements get_data_gas_price from EIP-4844
func GetBlobGasPrice(excessBlobGas *uint64) *big.Int {
	if excessBlobGas == nil {
		return nil
	}
	return fakeExponential(big.NewInt(params.MinBlobGasPrice), new(big.Int).SetUint64(*excessBlobGas), big.NewInt(params.BlobGasPriceUpdateFraction))
}

// GetBlobGasUsed returns the amount of blobgas consumed by a transaction with the specified number
// of blobs
func GetBlobGasUsed(blobs int) uint64 {
	return uint64(blobs) * params.BlobGasPerBlob
}
