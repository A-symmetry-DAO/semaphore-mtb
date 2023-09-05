package prover

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"
	"worldcoin/gnark-mbu/logging"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/iden3/go-iden3-crypto/keccak256"
)

type DeletionParameters struct {
	InputHash       big.Int
	PreRoot         big.Int
	PostRoot        big.Int
	DeletionIndices []uint32
	IdComms         []big.Int
	MerkleProofs    [][]big.Int
}

func (p *DeletionParameters) ValidateShape(treeDepth uint32, batchSize uint32) error {
	if len(p.IdComms) != int(batchSize) {
		return fmt.Errorf("wrong number of identity commitments: %d", len(p.IdComms))
	}
	if len(p.MerkleProofs) != int(batchSize) {
		return fmt.Errorf("wrong number of merkle proofs: %d", len(p.MerkleProofs))
	}
	if len(p.DeletionIndices) != int(batchSize) {
		return fmt.Errorf("wrong number of deletion indices: %d", len(p.DeletionIndices))
	}
	for i, proof := range p.MerkleProofs {
		if len(proof) != int(treeDepth) {
			return fmt.Errorf("wrong size of merkle proof for proof %d: %d", i, len(proof))
		}
	}
	return nil
}

// ComputeInputHashDeletion computes the input hash to the prover and verifier.
//
// It uses big-endian byte ordering (network ordering) in order to agree with
// Solidity and avoid the need to perform the byte swapping operations on-chain
// where they would increase our gas cost.
func (p *DeletionParameters) ComputeInputHashDeletion() error {
	var data []byte
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, p.DeletionIndices)
	if err != nil {
		return err
	}
	data = append(data, buf.Bytes()...)
	data = append(data, p.PreRoot.Bytes()...)
	data = append(data, p.PostRoot.Bytes()...)

	hashBytes := keccak256.Hash(data)
	p.InputHash.SetBytes(hashBytes)
	return nil
}

func BuildR1CSDeletion(treeDepth uint32, batchSize uint32) (constraint.ConstraintSystem, error) {
	proofs := make([][]frontend.Variable, batchSize)
	for i := 0; i < int(batchSize); i++ {
		proofs[i] = make([]frontend.Variable, treeDepth)
	}
	circuit := DeletionMbuCircuit{
		Depth:           int(treeDepth),
		BatchSize:       int(batchSize),
		DeletionIndices: make([]frontend.Variable, batchSize),
		IdComms:         make([]frontend.Variable, batchSize),
		MerkleProofs:    proofs,
	}
	return frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &circuit)
}

func SetupDeletion(treeDepth uint32, batchSize uint32) (*ProvingSystem, error) {
	ccs, err := BuildR1CSDeletion(treeDepth, batchSize)
	if err != nil {
		return nil, err
	}
	pk, vk, err := groth16.Setup(ccs)
	if err != nil {
		return nil, err
	}
	return &ProvingSystem{treeDepth, batchSize, pk, vk, ccs}, nil
}

func (ps *ProvingSystem) ProveDeletion(params *DeletionParameters) (*Proof, error) {
	if err := params.ValidateShape(ps.TreeDepth, ps.BatchSize); err != nil {
		return nil, err
	}

	deletionIndices := make([]frontend.Variable, ps.BatchSize)
	for i := 0; i < int(ps.BatchSize); i++ {
		deletionIndices[i] = params.DeletionIndices[i]
	}

	idComms := make([]frontend.Variable, ps.BatchSize)
	for i := 0; i < int(ps.BatchSize); i++ {
		idComms[i] = params.IdComms[i]
	}
	proofs := make([][]frontend.Variable, ps.BatchSize)
	for i := 0; i < int(ps.BatchSize); i++ {
		proofs[i] = make([]frontend.Variable, ps.TreeDepth)
		for j := 0; j < int(ps.TreeDepth); j++ {
			proofs[i][j] = params.MerkleProofs[i][j]
		}
	}
	assignment := DeletionMbuCircuit{
		InputHash:       params.InputHash,
		DeletionIndices: deletionIndices,
		PreRoot:         params.PreRoot,
		PostRoot:        params.PostRoot,
		IdComms:         idComms,
		MerkleProofs:    proofs,
	}
	witness, err := frontend.NewWitness(&assignment, ecc.BN254.ScalarField())
	if err != nil {
		return nil, err
	}
	logging.Logger().Info().Msg("generating proof")
	proof, err := groth16.Prove(ps.ConstraintSystem, ps.ProvingKey, witness)
	if err != nil {
		return nil, err
	}
	logging.Logger().Info().Msg("proof generated successfully")
	return &Proof{proof}, nil
}

func (ps *ProvingSystem) VerifyDeletion(inputHash big.Int, proof *Proof) error {
	publicAssignment := DeletionMbuCircuit{
		InputHash:       inputHash,
		DeletionIndices: make([]frontend.Variable, ps.BatchSize),
	}
	witness, err := frontend.NewWitness(&publicAssignment, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return err
	}
	return groth16.Verify(proof.Proof, ps.VerifyingKey, witness)
}
