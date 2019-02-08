// Copyright 2019 The go-ethereum Authors
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

package core

import (
	"github.com/clearmatics/autonity/accounts/abi"
	"github.com/clearmatics/autonity/common"
	"github.com/clearmatics/autonity/core/rawdb"
	"github.com/clearmatics/autonity/core/state"
	"github.com/clearmatics/autonity/core/types"
	"github.com/clearmatics/autonity/core/vm"
	"github.com/clearmatics/autonity/crypto"
	"github.com/clearmatics/autonity/log"
	"github.com/clearmatics/autonity/p2p/enode"
	"math/big"
	"strings"
)

func (bc *BlockChain) updateEnodesWhitelist(state *state.StateDB, block *types.Block) {
	var newWhitelist []*enode.Node
	var err error
	if block.Number().Uint64() == 1 {
		// deploy the contract
		newWhitelist, err = bc.deployContract(state, block.Header())
		if err != nil {
			log.Crit("Could not deploy Glienicke contract.", "error", err.Error())
		}
	}else{
		// call retrieveWhitelist contract function
		newWhitelist, err = bc.callGlienickeContract(state, block.Header())
		if err != nil {
			log.Crit("Could not call Glienicke contract.", "error", err.Error())
		}
	}

	rawdb.WriteEnodeWhitelist(bc.db, newWhitelist)
	bc.glienickeFeed.Send(GlienickeEvent{Whitelist:newWhitelist})
}

// Instantiates a new EVM object which is required when creating or calling a deployed contract
func (bc *BlockChain) getEVM(header *types.Header, origin common.Address, statedb *state.StateDB) *vm.EVM {

	coinbase, _ := bc.engine.Author(header)
	evmContext := vm.Context{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     GetHashFn(header, bc),
		Origin:      origin,
		Coinbase:    coinbase,
		BlockNumber: header.Number,
		Time:        header.Time,
		GasLimit:    header.GasLimit,
		Difficulty:  header.Difficulty,
		GasPrice:    new(big.Int).SetUint64(0x0),
	}
	evm := vm.NewEVM(evmContext, statedb, bc.Config(), bc.vmConfig)
	return evm
}

// deployContract deploys the contract contained within the genesis field bytecode
func (bc *BlockChain) deployContract(state *state.StateDB, header *types.Header) ([]*enode.Node, error) {
	// Convert the contract bytecode from hex into bytes
	contractBytecode := common.Hex2Bytes(bc.chainConfig.GlienickeBytecode)
	evm := bc.getEVM(header, bc.chainConfig.GlienickeDeployer, state)
	sender := vm.AccountRef(bc.chainConfig.GlienickeDeployer)

	currentWhitelist :=  rawdb.ReadEnodeWhitelist(bc.db)

	GlienickeAbi, err := abi.JSON(strings.NewReader(bc.chainConfig.GlienickeABI))
	if err != nil {
		return nil, err
	}
	constructorParams, err := GlienickeAbi.Pack("", currentWhitelist)
	if err != nil {
		return nil, err
	}

	data := append(contractBytecode, constructorParams...)
	gas := uint64(0xFFFFFFFF)
	value := new(big.Int).SetUint64(0x00)

	// Deploy the Glienicke validator governance contract
	_, contractAddress, gas, vmerr := evm.Create(sender, data, gas, value)

	if vmerr != nil {
		log.Error("Error Glienicke Contract deployment")
		return nil, vmerr
	}

	log.Info("Deployed Glienicke Contract", "Address", contractAddress.String())

	return currentWhitelist, nil
}

func (bc *BlockChain) callGlienickeContract(state *state.StateDB, header *types.Header) ([]*enode.Node, error){
	sender := vm.AccountRef(bc.chainConfig.GlienickeDeployer)
	gas := uint64(0xFFFFFFFF)
	evm := bc.getEVM(header, bc.chainConfig.GlienickeDeployer, state)
	SomaAbi, err := abi.JSON(strings.NewReader(bc.chainConfig.GlienickeABI))
	input, err := SomaAbi.Pack("getWhitelist")

	if err != nil {
		return nil, err
	}
	value := new(big.Int).SetUint64(0x00)
	//A standard call is issued - we leave the possibility to modify the state

	// we need the address
	glienickeAddress := crypto.CreateAddress(bc.chainConfig.GlienickeDeployer, 0)

	ret, gas, vmerr := evm.Call(sender, glienickeAddress, input, gas, value)
	if vmerr != nil {
		log.Error("Error Glienicke Contract getWhitelist()")
		return nil, vmerr
	}

	var returnedEnodes []string
	if err := SomaAbi.Unpack(&returnedEnodes, "getWhitelist", ret); err != nil { // can't work with aliased types
		log.Error("Could not unpack getWhitelist returned value")
		return nil, err
	}

	enodes := make([]*enode.Node, 0, len(returnedEnodes))
	for i := range returnedEnodes {
		newEnode, err := enode.ParseV4(returnedEnodes[i])
		if err != nil {
			enodes = append(enodes, newEnode)
		} else {
			log.Error("Invalid whitelisted enode", "returned enode", returnedEnodes[i], "error", err.Error())
		}
	}
	return enodes, nil
}
