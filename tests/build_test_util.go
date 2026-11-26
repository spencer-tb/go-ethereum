// Copyright 2026 The go-ethereum Authors
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

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/rpc"
)

// BuildTest checks the block builder against engine API fixtures. Every payload
// the fixture expects to be valid is built again through testing_buildBlockV1,
// on its parent and from its attributes, transactions and extra data, and the
// built payload is compared with the fixture's before the fixture's payload is
// imported.
type BuildTest struct {
	json etJSON

	// Built is the number of payloads the builder produced during Run.
	Built int

	// Mismatches lists every field of a built payload that differs from the
	// fixture's payload, in fixture order.
	Mismatches []Mismatch

	// Rejections lists every payload the client refused to build or to
	// import, with the client's error.
	Rejections []Rejection
}

// Mismatch is a field of a built payload that differs from the fixture's, with
// both values as they appear in the payload's JSON.
type Mismatch struct {
	Index    int             `json:"index"` // position in the fixture's engineNewPayloads
	Field    string          `json:"field"`
	Expected json.RawMessage `json:"expected"`
	Got      json.RawMessage `json:"got"`
}

// UnmarshalJSON implements json.Unmarshaler interface.
func (t *BuildTest) UnmarshalJSON(in []byte) error {
	return json.Unmarshal(in, &t.json)
}

// Network returns the network/fork name for this test.
func (t *BuildTest) Network() string {
	return t.json.Network
}

// BuildAPIFunc attaches the engine API and the testing API to a test's freshly
// built chain. It returns an in-process client for both, a function setting the
// builder's gas limit target, and a function releasing them.
type BuildAPIFunc func(chain *core.BlockChain, db ethdb.Database) (client *rpc.Client, setGasTarget func(uint64), release func(), err error)

// Run executes the build test on a fresh in-memory chain built from the
// fixture's genesis. Payloads the fixture expects to be invalid are skipped.
// Every other payload is built on the current head, compared with the fixture's,
// and then the fixture's payload is imported, so the chain follows the fixture
// whatever the builder produced. A payload that fails to import ends the run,
// since the next payload builds on it.
func (t *BuildTest) Run(scheme string, cfg RunConfig, attach BuildAPIFunc) error {
	t.Built, t.Mismatches, t.Rejections = 0, []Mismatch{}, []Rejection{}
	chain, db, err := t.json.newChain(scheme, nil, cfg)
	if err != nil {
		return err
	}
	defer chain.Stop()

	if err := t.json.checkGenesis(chain.Genesis()); err != nil {
		return err
	}
	client, setGasTarget, release, err := attach(chain, db)
	if err != nil {
		return err
	}
	defer release()
	if err := t.json.forkchoiceToGenesis(client, chain.Genesis().Hash()); err != nil {
		return err
	}
	for i, payload := range t.json.Payloads {
		if payload.ValidationError != "" || payload.ErrorCode != nil {
			continue
		}
		if err := t.build(client, setGasTarget, i, &payload); err != nil {
			return err
		}
		imported, err := t.importPayload(client, i, &payload)
		if err != nil {
			return err
		}
		if !imported {
			break
		}
	}
	if len(t.Mismatches) > 0 || len(t.Rejections) > 0 {
		return fmt.Errorf("%d mismatched fields, %d rejected payloads", len(t.Mismatches), len(t.Rejections))
	}
	return nil
}

// build builds the payload's block through testing_buildBlockV1 and records
// every field in which it differs from the fixture's payload. A build the
// client refuses is a rejection.
func (t *BuildTest) build(client *rpc.Client, setGasTarget func(uint64), index int, payload *etNewPayload) error {
	var want engine.ExecutableData
	if err := json.Unmarshal(payload.Params[0], &want); err != nil {
		return fmt.Errorf("payload %d: %v", index, err)
	}
	attributes := engine.PayloadAttributes{
		Timestamp:             want.Timestamp,
		Random:                want.Random,
		SuggestedFeeRecipient: want.FeeRecipient,
		Withdrawals:           want.Withdrawals,
		SlotNumber:            want.SlotNumber,
	}
	// The parent beacon block root is a newPayload param from V3 on.
	if payload.Version >= 3 {
		if len(payload.Params) < 3 {
			return fmt.Errorf("payload %d: no parentBeaconBlockRoot param", index)
		}
		var root common.Hash
		if err := json.Unmarshal(payload.Params[2], &root); err != nil {
			return fmt.Errorf("payload %d: parentBeaconBlockRoot: %v", index, err)
		}
		attributes.BeaconRoot = &root
	}
	// The builder targets the fixture's gas limit, through the attributes from
	// forkchoiceUpdatedV4 on and through its own gas target before. A valid
	// payload's gas limit is within one step of its parent's, so it is reached.
	if payload.FcuVersion >= 4 {
		gasLimit := want.GasLimit
		attributes.TargetGasLimit = &gasLimit
	}
	setGasTarget(want.GasLimit)

	transactions := make([]hexutil.Bytes, len(want.Transactions))
	for i, tx := range want.Transactions {
		transactions[i] = tx
	}
	var built engine.ExecutionPayloadEnvelope
	err := client.CallContext(context.Background(), &built, "testing_buildBlockV1", want.ParentHash, attributes, transactions, hexutil.Bytes(want.ExtraData))
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) {
		t.Rejections = append(t.Rejections, Rejection{Index: index, Error: rpcRejection(rpcErr)})
		return nil
	}
	if err != nil {
		return fmt.Errorf("payload %d: testing_buildBlockV1: %v", index, err)
	}
	t.Built++

	mismatches, err := fieldMismatches(index, &want, built.ExecutionPayload)
	if err != nil {
		return fmt.Errorf("payload %d: %v", index, err)
	}
	t.Mismatches = append(t.Mismatches, mismatches...)

	// The execution requests are a newPayload param from V4 on.
	if payload.Version >= 4 {
		if len(payload.Params) < 4 {
			return fmt.Errorf("payload %d: no executionRequests param", index)
		}
		var wantRequests []hexutil.Bytes
		if err := json.Unmarshal(payload.Params[3], &wantRequests); err != nil {
			return fmt.Errorf("payload %d: executionRequests: %v", index, err)
		}
		gotRequests := make([]hexutil.Bytes, len(built.Requests))
		for i, request := range built.Requests {
			gotRequests[i] = request
		}
		expected, err := json.Marshal(wantRequests)
		if err != nil {
			return err
		}
		got, err := json.Marshal(gotRequests)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, got) {
			t.Mismatches = append(t.Mismatches, Mismatch{Index: index, Field: "executionRequests", Expected: expected, Got: got})
		}
	}
	return nil
}

// importPayload imports the fixture's payload through engine_newPayload and
// moves the head to it. A payload the client does not accept as valid is a
// rejection, and importPayload then reports it was not imported.
func (t *BuildTest) importPayload(client *rpc.Client, index int, payload *etNewPayload) (bool, error) {
	params := make([]any, len(payload.Params))
	for j, param := range payload.Params {
		params[j] = param
	}
	var status engine.PayloadStatusV1
	err := client.CallContext(context.Background(), &status, fmt.Sprintf("engine_newPayloadV%d", payload.Version), params...)
	if accepted, err := t.accepted(index, status, err); !accepted || err != nil {
		return false, err
	}
	fcResp, err := forkchoiceUpdated(client, payload.FcuVersion, payload.BlockHash)
	return t.accepted(index, fcResp.PayloadStatus, err)
}

// accepted reports whether an engine API call answered VALID. A JSON-RPC error
// or any other status is recorded as a rejection of the payload.
func (t *BuildTest) accepted(index int, status engine.PayloadStatusV1, err error) (bool, error) {
	var rpcErr rpc.Error
	switch {
	case errors.As(err, &rpcErr):
		t.Rejections = append(t.Rejections, Rejection{Index: index, Error: rpcRejection(rpcErr)})
		return false, nil
	case err != nil:
		return false, fmt.Errorf("payload %d: %v", index, err)
	case status.Status != engine.VALID:
		reason := status.Status
		if status.ValidationError != nil {
			reason = *status.ValidationError
		}
		t.Rejections = append(t.Rejections, Rejection{Index: index, Error: reason})
		return false, nil
	}
	return true, nil
}

// fieldMismatches compares two execution payloads field by field in their JSON
// form, so both values of a mismatch read as they do in a fixture. Fields are
// listed in name order.
func fieldMismatches(index int, want, got *engine.ExecutableData) ([]Mismatch, error) {
	wantFields, err := jsonFields(want)
	if err != nil {
		return nil, err
	}
	gotFields, err := jsonFields(got)
	if err != nil {
		return nil, err
	}
	names := maps.Clone(wantFields)
	maps.Copy(names, gotFields)

	var mismatches []Mismatch
	for _, name := range slices.Sorted(maps.Keys(names)) {
		expected, got := orNull(wantFields[name]), orNull(gotFields[name])
		if !bytes.Equal(expected, got) {
			mismatches = append(mismatches, Mismatch{Index: index, Field: name, Expected: expected, Got: got})
		}
	}
	return mismatches, nil
}

// jsonFields returns the top-level fields of v's JSON encoding.
func jsonFields(v any) (map[string]json.RawMessage, error) {
	enc, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(enc, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// orNull returns raw, or JSON null for a field one payload does not have.
func orNull(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return json.RawMessage("null")
	}
	return raw
}
