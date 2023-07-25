// Copyright 2022 The go-ethereum Authors
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
	"testing"

	"github.com/ethereum/go-ethereum/params"
)

func TestCalcExcessBlobGas(t *testing.T) {
	var tests = []struct {
		parentExcessBlobGas uint64
		newBlobs            int
		want                uint64
	}{
		{0, 0, 0},
		{0, 1, 0},
		{0, params.TargetBlobGasPerBlock / params.BlobGasPerBlob, 0},
		{0, (params.TargetBlobGasPerBlock / params.BlobGasPerBlob) + 1, params.BlobGasPerBlob},
		{100000, (params.TargetBlobGasPerBlock / params.BlobGasPerBlob) + 1, params.BlobGasPerBlob + 100000},
		{params.TargetBlobGasPerBlock, 1, params.BlobGasPerBlob},
		{params.TargetBlobGasPerBlock, 0, 0},
		{params.TargetBlobGasPerBlock, (params.TargetBlobGasPerBlock / params.BlobGasPerBlob), params.TargetBlobGasPerBlock},
	}

	for _, tt := range tests {
		blobGasUsed := uint64(tt.newBlobs * params.BlobGasPerBlob)
		result := CalcExcessBlobGas(&tt.parentExcessBlobGas, &blobGasUsed)
		if tt.want != result {
			t.Errorf("got %v want %v", result, tt.want)
		}
	}

	// Test nil value for parentExcessBlobGas
	blobGasUsed := uint64(params.TargetBlobGasPerBlock + params.BlobGasPerBlob)
	result := CalcExcessBlobGas(nil, &blobGasUsed)
	if result != params.BlobGasPerBlob {
		t.Errorf("got %v want %v", result, params.BlobGasPerBlob)
	}
}
