//go:build !mock_verifier

package verifier

/*
#cgo LDFLAGS: -lzkp -lm -ldl -L${SRCDIR}/lib/ -Wl,-rpath=${SRCDIR}/lib
#cgo gpu LDFLAGS: -lzkp -lm -ldl -lgmp -lstdc++ -lprocps -L/usr/local/cuda/lib64/ -lcudart -L${SRCDIR}/lib/ -Wl,-rpath=${SRCDIR}/lib
#include <stdlib.h>
#include "./lib/libzkp.h"
*/
import "C" //nolint:typecheck

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path"
	"unsafe"

	"github.com/scroll-tech/go-ethereum/log"

	"scroll-tech/common/types/message"

	"scroll-tech/coordinator/internal/config"
)

// This struct maps to `CircuitConfig` in common/libzkp/impl/src/verifier.rs
// Define a brand new struct here is to eliminate side effects in case fields
// in `*config.CircuitConfig` being changed
type rustCircuitConfig struct {
	ForkName   string `json:"fork_name"`
	ParamsPath string `json:"params_path"`
	AssetsPath string `json:"assets_path"`
}

func newRustCircuitConfig(cfg *config.CircuitConfig) *rustCircuitConfig {
	return &rustCircuitConfig{
		ForkName:   cfg.ForkName,
		ParamsPath: cfg.ParamsPath,
		AssetsPath: cfg.AssetsPath,
	}
}

// This struct maps to `VerifierConfig` in common/libzkp/impl/src/verifier.rs
// Define a brand new struct here is to eliminate side effects in case fields
// in `*config.VerifierConfig` being changed
type rustVerifierConfig struct {
	LowVersionCircuit  *rustCircuitConfig `json:"low_version_circuit"`
	HighVersionCircuit *rustCircuitConfig `json:"high_version_circuit"`
}

func newRustVerifierConfig(cfg *config.VerifierConfig) *rustVerifierConfig {
	return &rustVerifierConfig{
		LowVersionCircuit:  newRustCircuitConfig(cfg.LowVersionCircuit),
		HighVersionCircuit: newRustCircuitConfig(cfg.HighVersionCircuit),
	}
}

// NewVerifier Sets up a rust ffi to call verify.
func NewVerifier(cfg *config.VerifierConfig) (*Verifier, error) {
	if cfg.MockMode {
		chunkVKMap := map[string]struct{}{"mock_vk": {}}
		batchVKMap := map[string]struct{}{"mock_vk": {}}
		bundleVKMap := map[string]struct{}{"mock_vk": {}}
		return &Verifier{cfg: cfg, ChunkVKMap: chunkVKMap, BatchVKMap: batchVKMap, BundleVkMap: bundleVKMap}, nil
	}
	verifierConfig := newRustVerifierConfig(cfg)
	configBytes, err := json.Marshal(verifierConfig)
	if err != nil {
		return nil, err
	}

	configStr := C.CString(string(configBytes))
	assetsPathHiStr := C.CString(cfg.HighVersionCircuit.AssetsPath)
	defer func() {
		C.free(unsafe.Pointer(configStr))
		C.free(unsafe.Pointer(assetsPathHiStr))
	}()

	C.init(configStr)

	v := &Verifier{
		cfg:         cfg,
		ChunkVKMap:  make(map[string]struct{}),
		BatchVKMap:  make(map[string]struct{}),
		BundleVkMap: make(map[string]struct{}),
	}

	bundleVK, err := v.readVK(path.Join(cfg.HighVersionCircuit.AssetsPath, "vk_bundle.vkey"))
	if err != nil {
		return nil, err
	}
	batchVK, err := v.readVK(path.Join(cfg.HighVersionCircuit.AssetsPath, "vk_batch.vkey"))
	if err != nil {
		return nil, err
	}
	chunkVK, err := v.readVK(path.Join(cfg.HighVersionCircuit.AssetsPath, "vk_chunk.vkey"))
	if err != nil {
		return nil, err
	}
	v.BundleVkMap[bundleVK] = struct{}{}
	v.BatchVKMap[batchVK] = struct{}{}
	v.ChunkVKMap[chunkVK] = struct{}{}

	if err := v.loadLowVersionVKs(cfg); err != nil {
		return nil, err
	}
	return v, nil
}

// VerifyBatchProof Verify a ZkProof by marshaling it and sending it to the Halo2 Verifier.
func (v *Verifier) VerifyBatchProof(proof *message.BatchProof, forkName string) (bool, error) {
	if v.cfg.MockMode {
		log.Info("Mock mode, batch verifier disabled")
		if string(proof.Proof) == InvalidTestProof {
			return false, nil
		}
		return true, nil

	}
	buf, err := json.Marshal(proof)
	if err != nil {
		return false, err
	}

	log.Info("Start to verify batch proof", "forkName", forkName)
	proofStr := C.CString(string(buf))
	forkNameStr := C.CString(forkName)
	defer func() {
		C.free(unsafe.Pointer(proofStr))
		C.free(unsafe.Pointer(forkNameStr))
	}()

	verified := C.verify_batch_proof(proofStr, forkNameStr)
	return verified != 0, nil
}

// VerifyChunkProof Verify a ZkProof by marshaling it and sending it to the Halo2 Verifier.
func (v *Verifier) VerifyChunkProof(proof *message.ChunkProof, forkName string) (bool, error) {
	if v.cfg.MockMode {
		log.Info("Mock mode, verifier disabled")
		if string(proof.Proof) == InvalidTestProof {
			return false, nil
		}
		return true, nil

	}
	buf, err := json.Marshal(proof)
	if err != nil {
		return false, err
	}

	log.Info("Start to verify chunk proof", "forkName", forkName)
	proofStr := C.CString(string(buf))
	forkNameStr := C.CString(forkName)
	defer func() {
		C.free(unsafe.Pointer(proofStr))
		C.free(unsafe.Pointer(forkNameStr))
	}()

	verified := C.verify_chunk_proof(proofStr, forkNameStr)
	return verified != 0, nil
}

// VerifyBundleProof Verify a ZkProof for a bundle of batches, by marshaling it and verifying it via the EVM verifier.
func (v *Verifier) VerifyBundleProof(proof *message.BundleProof, forkName string) (bool, error) {
	if v.cfg.MockMode {
		log.Info("Mock mode, verifier disabled")
		if string(proof.Proof) == InvalidTestProof {
			return false, nil
		}
		return true, nil

	}
	buf, err := json.Marshal(proof)
	if err != nil {
		return false, err
	}

	proofStr := C.CString(string(buf))
	forkNameStr := C.CString(forkName)
	defer func() {
		C.free(unsafe.Pointer(proofStr))
		C.free(unsafe.Pointer(forkNameStr))
	}()

	log.Info("Start to verify bundle proof ...")
	verified := C.verify_bundle_proof(proofStr, forkNameStr)
	return verified != 0, nil
}

func (v *Verifier) readVK(filePat string) (string, error) {
	f, err := os.Open(filePat)
	if err != nil {
		return "", err
	}
	byt, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(byt), nil
}

// load low version vks, current is darwin
func (v *Verifier) loadLowVersionVKs(cfg *config.VerifierConfig) error {
	bundleVK, err := v.readVK(path.Join(cfg.LowVersionCircuit.AssetsPath, "vk_bundle.vkey"))
	if err != nil {
		return err
	}
	batchVK, err := v.readVK(path.Join(cfg.LowVersionCircuit.AssetsPath, "vk_batch.vkey"))
	if err != nil {
		return err
	}
	chunkVK, err := v.readVK(path.Join(cfg.LowVersionCircuit.AssetsPath, "vk_chunk.vkey"))
	if err != nil {
		return err
	}
	v.BundleVkMap[bundleVK] = struct{}{}
	v.BatchVKMap[batchVK] = struct{}{}
	v.ChunkVKMap[chunkVK] = struct{}{}
	return nil
}
