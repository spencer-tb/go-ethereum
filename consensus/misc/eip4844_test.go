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

func TestCalcExcessDataGas(t *testing.T) {
	var tests = []struct {
		parentExcessDataGas uint64
		newBlobs            int
		want                uint64
	}{
		{0, 0, 0},
		{0, 1, 0},
		{0, params.TargetDataGasPerBlock / params.DataGasPerBlob, 0},
		{0, (params.TargetDataGasPerBlock / params.DataGasPerBlob) + 1, params.DataGasPerBlob},
		{100000, (params.TargetDataGasPerBlock / params.DataGasPerBlob) + 1, params.DataGasPerBlob + 100000},
		{params.TargetDataGasPerBlock, 1, params.DataGasPerBlob},
		{params.TargetDataGasPerBlock, 0, 0},
		{params.TargetDataGasPerBlock, (params.TargetDataGasPerBlock / params.DataGasPerBlob), params.TargetDataGasPerBlock},
	}

	for _, tt := range tests {
		dataGasUsed := uint64(tt.newBlobs * params.DataGasPerBlob)
		result := CalcExcessDataGas(&tt.parentExcessDataGas, &dataGasUsed)
		if tt.want != result {
			t.Errorf("got %v want %v", result, tt.want)
		}
	}

	// Test nil value for parentExcessDataGas
	dataGasUsed := uint64(params.TargetDataGasPerBlock + params.DataGasPerBlob)
	result := CalcExcessDataGas(nil, &dataGasUsed)
	if result != params.DataGasPerBlob {
		t.Errorf("got %v want %v", result, params.DataGasPerBlob)
	}
}
