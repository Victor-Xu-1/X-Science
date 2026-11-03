package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReaderFirstDocumentsDoNotUpgradeExistingSQLiteAuthority(t *testing.T) {
	ctx := context.Background()
	store, backend, _, execution := newAcceptedDetachedKernelExecutionFixture(t)
	v1Spec, err := DecodeKernelExecutionSessionSpecV1(backend.SessionSpecJSON)
	if err != nil {
		t.Fatal(err)
	}
	v1Request, err := DecodeKernelDetachedExecutionRequestV1(execution.RequestJSON)
	if err != nil {
		t.Fatal(err)
	}
	contract := KernelExecutionResultContract{ResultVersion: 2, HostResourceEvidenceVersion: 1}
	specRaw, _ := json.Marshal(KernelExecutionSessionSpecV2{Version: 2, Session: v1Spec, ResultContract: contract})
	binding := KernelDetachedExecutionBindingV2{BackendID: backend.BackendID, BackendGeneration: backend.BackendGeneration, ExecutorInstanceID: backend.ExecutorInstanceID, SessionSpecSHA256: sha256HexString(string(specRaw))}
	requestRaw, _ := json.Marshal(KernelDetachedExecutionRequestV2{Version: 2, Request: v1Request, Binding: binding, ResultContract: contract})
	// Reading V2 must never permit upgrading an existing immutable V1 record.
	if _, err := store.db.Exec(`UPDATE kernel_execution_backends SET session_spec_json=?,session_spec_sha256=? WHERE backend_id=?`, string(specRaw), binding.SessionSpecSHA256, backend.BackendID); err == nil {
		t.Fatal("existing session authority was rewritten")
	}
	if _, err := store.db.Exec(`UPDATE kernel_detached_executions SET request_json=?,request_sha256=? WHERE execution_id=?`, string(requestRaw), sha256HexString(string(requestRaw)), execution.ExecutionID); err == nil {
		t.Fatal("existing request authority was rewritten")
	}
	loadedBackend, found, err := store.GetKernelExecutionBackend(ctx, backend.BackendID)
	if err != nil || !found || loadedBackend.SessionSpecJSON != backend.SessionSpecJSON {
		t.Fatalf("backend read found=%t err=%v", found, err)
	}
	loaded, found, err := store.GetDetachedKernelExecution(ctx, execution.ExecutionID)
	if err != nil || !found || loaded.RequestJSON != execution.RequestJSON || loaded.RequestSHA256 != execution.RequestSHA256 {
		t.Fatalf("execution read found=%t err=%v", found, err)
	}
	page, err := store.ListDetachedKernelExecutionRecoveryPage(ctx, "", 10)
	if err != nil || len(page.Candidates) != 1 {
		t.Fatalf("recovery page=%#v err=%v", page, err)
	}
	copy := loaded
	copy.RequestJSON, copy.RequestSHA256 = string(requestRaw), sha256HexString(string(requestRaw))
	if doc, err := ReadDetachedExecutionDocument(copy); err != nil || doc.Binding != binding {
		t.Fatalf("V2 closed request read=%#v err=%v", doc, err)
	}
	copy.BackendGeneration++
	if _, err := ReadDetachedExecutionDocument(copy); err == nil {
		t.Fatal("replacement generation granted authority to old request")
	}
}

func TestReaderFirstDocumentDecodersPreserveClosedV1(t *testing.T) {
	store, backend, _, execution := newAcceptedDetachedKernelExecutionFixture(t)
	_ = store
	for _, raw := range []string{backend.SessionSpecJSON, execution.RequestJSON} {
		changed := strings.TrimSuffix(raw, "}") + `,"result_contract":{"result_version":2,"host_resource_evidence_version":1}}`
		if _, err := DecodeKernelExecutionSessionSpecV1(changed); err == nil {
			t.Fatal("V1 session accepted unknown fields")
		}
		if _, err := DecodeKernelDetachedExecutionRequestV1(changed); err == nil {
			t.Fatal("V1 request accepted unknown fields")
		}
	}
	session, err := DecodeKernelExecutionSessionDocument(backend.SessionSpecJSON)
	if err != nil || session.DocumentVersion != 1 || session.ResultContract.Valid() {
		t.Fatalf("V1 session acquired capability: %#v %v", session, err)
	}
	request, err := ReadDetachedExecutionDocument(execution)
	if err != nil || request.DocumentVersion != 1 || request.ResultContract.Valid() {
		t.Fatalf("V1 request acquired capability: %#v %v", request, err)
	}
	encoded, _ := json.Marshal(request.KernelDetachedExecutionRequestV1)
	if string(encoded) != execution.RequestJSON || sha256HexString(string(encoded)) != execution.RequestSHA256 {
		t.Fatal("V1 request bytes/hash changed")
	}
	for _, raw := range []string{`{"version":3}`, `{"version":2,"extra":true}`, `{"version":2,"request":null}`} {
		if _, err := DecodeKernelDetachedExecutionDocument(raw); err == nil {
			t.Fatalf("accepted invalid request %s", raw)
		}
		if _, err := DecodeKernelExecutionSessionDocument(raw); err == nil {
			t.Fatalf("accepted invalid session %s", raw)
		}
	}
}
