// Copyright 2026 The go-ethereum Authors
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

package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"

	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/eth/catalyst"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/miner"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/tests"
	"github.com/urfave/cli/v2"
)

var buildTestCommand = &cli.Command{
	Action:    buildTestCmd,
	Name:      "buildtest",
	Usage:     "Builds the valid payloads of the given engine API tests with geth's block builder and compares them with the fixtures. Filenames can be fed via standard input (batch mode) or as arguments.",
	ArgsUsage: "<path>...",
	Flags: []cli.Flag{
		HumanReadableFlag,
		RunFlag,
		WorkersFlag,
		CacheNoPrecompileFlag,
	},
}

func buildTestCmd(ctx *cli.Context) error {
	// If paths are provided, run the tests at those paths.
	if ctx.Args().Present() {
		files, err := collectFixtures(ctx.Args().Slice()...)
		if err != nil {
			return cli.Exit(err, exitIncomplete)
		}
		results, err := runFiles(ctx, files, runBuildTest)
		report(ctx, results)
		if err != nil {
			return cli.Exit(err, exitIncomplete)
		}
		return failures(results)
	}
	// Otherwise, read filenames from stdin and execute back-to-back.
	return runStdin(ctx, runBuildTest)
}

func runBuildTest(ctx *cli.Context, fname string) ([]testResult, error) {
	src, err := os.ReadFile(fname)
	if err != nil {
		return nil, err
	}
	var testsByName map[string]*tests.BuildTest
	if err = json.Unmarshal(src, &testsByName); err != nil {
		return nil, fmt.Errorf("unable to read test file %s: %w", fname, err)
	}
	re, err := regexp.Compile(ctx.String(RunFlag.Name))
	if err != nil {
		return nil, fmt.Errorf("invalid regex -%s: %v", RunFlag.Name, err)
	}
	var results []testResult
	for _, name := range slices.Sorted(maps.Keys(testsByName)) {
		if !re.MatchString(name) {
			continue
		}
		test := testsByName[name]
		result := testResult{Name: name, Pass: true}
		if err := test.Run(rawdb.PathScheme, runConfig(ctx), attachBuildAPI); err != nil {
			result.Pass, result.Error = false, err.Error()
		}
		built := test.Built
		result.Fork = test.Network()
		result.Rejections = test.Rejections
		result.Built = &built
		result.Mismatches = test.Mismatches
		results = append(results, result)
	}
	return results, nil
}

// buildBackend is enginetest's backend with a miner, so that the testing API
// can build payloads on a test's chain. The miner has no transaction pool:
// buildtest always names the transactions of the block it builds.
type buildBackend struct {
	*engineBackend
	miner *miner.Miner
}

func (b *buildBackend) Miner() *miner.Miner { return b.miner }

// attachBuildAPI puts geth's engine API and its testing API, which serves
// testing_buildBlockV1, on a test's chain. It returns an in-process RPC client
// for both and a function setting the miner's gas limit target. No node,
// networking, RPC transport or other services are started.
func attachBuildAPI(chain *core.BlockChain, db ethdb.Database) (*rpc.Client, func(uint64), func(), error) {
	base := &engineBackend{
		chain:      chain,
		db:         db,
		downloader: downloader.New(db, ethconfig.FullSync, chain, func(string) {}, func() {}, false),
	}
	backend := &buildBackend{
		engineBackend: base,
		miner:         miner.New(base, miner.DefaultConfig, chain.Engine()),
	}
	server := rpc.NewServer()
	testingAPI := catalyst.NewTestingAPI(backend)
	for name, service := range map[string]any{
		"engine":             catalyst.NewConsensusAPIWithoutHeartbeat(backend),
		testingAPI.Namespace: testingAPI.Service,
	} {
		if err := server.RegisterName(name, service); err != nil {
			base.downloader.Terminate()
			return nil, nil, nil, err
		}
	}
	client := rpc.DialInProc(server)
	release := func() {
		client.Close()
		server.Stop()
		base.downloader.Terminate()
	}
	return client, backend.miner.SetGasCeil, release, nil
}
