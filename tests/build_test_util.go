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
	"reflect"
	"slices"
	"strings"

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
// builder's own gas limit target, where 0 restores its default, and a function
// releasing them.
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
	// forkchoiceUpdatedV4 on and through its own gas target before. From V4 on its
	// own target stays at the default, so only the attributes can reach the limit.
	// A valid payload's gas limit is within one step of its parent's.
	if payload.FcuVersion >= 4 {
		gasLimit := want.GasLimit
		attributes.TargetGasLimit = &gasLimit
		setGasTarget(0)
	} else {
		setGasTarget(want.GasLimit)
	}

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

	mismatches, err := fieldMismatches(index, payload.Params[0], built.ExecutionPayload)
	if err != nil {
		return fmt.Errorf("payload %d: %v", index, err)
	}
	t.Mismatches = append(t.Mismatches, mismatches...)

	// The execution requests are a newPayload param from V4 on.
	if payload.Version >= 4 {
		if len(payload.Params) < 4 {
			return fmt.Errorf("payload %d: no executionRequests param", index)
		}
		// Requests the builder left out encode as null, not as an empty list.
		var gotRequests []hexutil.Bytes
		if built.Requests != nil {
			gotRequests = make([]hexutil.Bytes, len(built.Requests))
			for i, request := range built.Requests {
				gotRequests[i] = request
			}
		}
		got, err := json.Marshal(gotRequests)
		if err != nil {
			return err
		}
		mismatch, err := valueMismatch(index, "executionRequests", payload.Params[3], got)
		if err != nil {
			return fmt.Errorf("payload %d: executionRequests: %v", index, err)
		}
		if mismatch != nil {
			t.Mismatches = append(t.Mismatches, *mismatch)
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

// fieldMismatches compares every field of the fixture's execution payload, as
// the fixture has it, with the built payload's in its JSON form, so both values
// of a mismatch read as they do in a fixture. A field only the built payload has
// is not compared. Fields are listed in name order.
func fieldMismatches(index int, fixture json.RawMessage, got *engine.ExecutableData) ([]Mismatch, error) {
	var wantFields map[string]json.RawMessage
	if err := json.Unmarshal(fixture, &wantFields); err != nil {
		return nil, err
	}
	gotFields, err := jsonFields(got)
	if err != nil {
		return nil, err
	}
	var mismatches []Mismatch
	for _, name := range slices.Sorted(maps.Keys(wantFields)) {
		mismatch, err := valueMismatch(index, name, wantFields[name], orNull(gotFields[name]))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if mismatch != nil {
			mismatches = append(mismatches, *mismatch)
		}
	}
	return mismatches, nil
}

// valueMismatch returns the mismatch of field when the fixture's value and the
// built one differ as JSON values, and nil when they are equal.
func valueMismatch(index int, field string, expected, got json.RawMessage) (*Mismatch, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, expected); err != nil {
		return nil, err
	}
	var want, have any
	if err := json.Unmarshal(buf.Bytes(), &want); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(got, &have); err != nil {
		return nil, err
	}
	if jsonValuesEqual(want, have) {
		return nil, nil
	}
	return &Mismatch{Index: index, Field: field, Expected: json.RawMessage(buf.Bytes()), Got: got}, nil
}

// jsonValuesEqual compares two decoded JSON values as values: objects by their
// fields in any order, a missing field as null, and strings, which carry the
// payload's hex, regardless of case.
func jsonValuesEqual(a, b any) bool {
	switch a := a.(type) {
	case string:
		b, ok := b.(string)
		return ok && strings.EqualFold(a, b)
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !jsonValuesEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range a {
			if !jsonValuesEqual(value, b[key]) {
				return false
			}
		}
		for key, value := range b {
			if _, ok := a[key]; !ok && value != nil {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
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
