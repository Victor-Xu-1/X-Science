package productstate

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// Staging receipts are never authority. They remain private, unclaimed recovery
// evidence on interruption; only the verified atomic final publication is read.
func publishPendingReceipt(root, target string, proof namespaceReceipt, write func(string, []byte, os.FileMode) error) error {
	if err := checkPath(root, filepath.Dir(target)); err != nil {
		return err
	}
	stage := filepath.Join(filepath.Dir(target), ".namespace-upgrade.receipt-"+rand.Text()+".next")
	raw, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	if err := write(stage, raw, 0o600); err != nil {
		return err
	}
	// Readback verifies a complete JSON receipt plus exact source proof, mode,
	// ownership and ordinary-file shape before it can become authoritative.
	receipt, found, err := readReceipt(root, stage)
	if err != nil || !found || !reflect.DeepEqual(receipt, proof) {
		return errors.New("staged private namespace receipt is unconfirmed")
	}
	if err := publishMissing(stage, target); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	receipt, found, err = readReceipt(root, target)
	if err != nil || !found || !reflect.DeepEqual(receipt, proof) {
		return errors.New("published private namespace receipt is unconfirmed")
	}
	return nil
}
