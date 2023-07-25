// Copyright 2021 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package misc

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
)

// CalcExcessBlobGas implements calc_excess_data_gas from EIP-4844
func CalcExcessBlobGas(parentExcessBlobGas *uint64, parentBlobGasUsed *uint64) uint64 {
	var (
		pEdg = uint64(0)
		pDgu = uint64(0)
	)
	if parentExcessBlobGas != nil {
		pEdg = *parentExcessBlobGas
	}
	if parentBlobGasUsed != nil {
		pDgu = *parentBlobGasUsed
	}
	if pEdg+pDgu < params.TargetBlobGasPerBlock {
		return 0
	}
	return pEdg + pDgu - params.TargetBlobGasPerBlock
}

// CountBlobs returns the number of blob transactions in txs
func CountBlobs(txs []*types.Transaction) int {
	var count int
	for _, tx := range txs {
		count += len(tx.DataHashes())
	}
	return count
}

// VerifyEip4844Header verifies that the header is not malformed but does *not* check the value of excessBlobGas.
// See VerifyExcessBlobGas for the full check.
func VerifyEip4844Header(config *params.ChainConfig, parent, header *types.Header) error {
	if header.ExcessBlobGas == nil {
		return fmt.Errorf("header is missing excessBlobGas")
	}
	if header.BlobGasUsed == nil {
		return fmt.Errorf("header is missing blobGasUsed")
	}
	var (
		excessBlobGas = *header.ExcessBlobGas
	)
	if excessBlobGas != CalcExcessBlobGas(parent.ExcessBlobGas, parent.BlobGasUsed) {
		return fmt.Errorf("invalid excessBlobGas: have %d want %d", excessBlobGas, CalcExcessBlobGas(parent.ExcessBlobGas, parent.BlobGasUsed))
	}
	return nil
}

// VerifyExcessBlobGas verifies the excess_data_gas in the block header
func VerifyExcessBlobGas(chainReader ChainReader, block *types.Block) error {
	excessBlobGas := block.ExcessBlobGas()
	blobGasUsed := block.BlobGasUsed()
	if !chainReader.Config().IsCancun(block.Time()) {
		if excessBlobGas != nil {
			return fmt.Errorf("unexpected excessBlobGas in header")
		}
		if blobGasUsed != nil {
			return fmt.Errorf("unexpected blobGasUsed in header")
		}
		return nil
	}
	if excessBlobGas == nil {
		return fmt.Errorf("header is missing excessBlobGas")
	}
	if blobGasUsed == nil {
		return fmt.Errorf("header is missing blobGasUsed")
	}

	number, parent := block.NumberU64()-1, block.ParentHash()
	parentBlock := chainReader.GetBlock(parent, number)
	if parentBlock == nil {
		return fmt.Errorf("parent block not found")
	}
	expectedEDG := CalcExcessBlobGas(parentBlock.ExcessBlobGas(), parentBlock.BlobGasUsed())
	if *excessBlobGas != expectedEDG {
		return fmt.Errorf("invalid excessBlobGas: have %d want %d", *excessBlobGas, expectedEDG)
	}
	return nil
}

// ChainReader defines a small collection of methods needed to access the local
// blockchain for EIP4844 block verifcation.
type ChainReader interface {
	Config() *params.ChainConfig
	// GetBlock retrieves a block from the database by hash and number.
	GetBlock(hash common.Hash, number uint64) *types.Block
}
