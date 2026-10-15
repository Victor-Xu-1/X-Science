package workspace

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"

	"x-science/internal/compute"
)

// Production owned claims create their intent and physical-instance authority
// atomically. A web crash cannot leave a starting/stopping row with no ref for
// recovery. Historical unowned metadata keeps its existing read-only boundary.
func (s *Store) prepareClaimedManagedEndpointTx(ctx context.Context, tx *sql.Tx, endpoint ManagedEndpoint, roots []string) error {
	if endpoint.Location == "remote" || endpoint.OwnerSpec == nil {
		return nil
	}
	if len(roots) != 1 || !filepath.IsAbs(roots[0]) {
		return errors.New("owned endpoint claim requires its trusted runtime root")
	}
	credential := ""
	if endpoint.CredentialName != nil {
		credential = *endpoint.CredentialName
	}
	registration := compute.ManagedEndpointRegistration{Name: endpoint.Name, URL: endpoint.URL, Port: endpoint.Port, CredentialName: credential, SkillName: endpoint.SkillName, StartScript: endpoint.StartScript, StopScript: endpoint.StopScript, LivePath: endpoint.LivePath, Owner: endpoint.OwnerSpec}
	claim := compute.ManagedEndpointClaim{UserID: endpoint.RegisteredBy, Name: endpoint.Name, Handle: endpoint.ClaimHandle, Generation: endpoint.ClaimGeneration, Registration: registration, ApprovedHash: endpoint.ApprovedScriptHash, ServiceDir: endpoint.ServiceDir}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	_, _, err := s.prepareManagedEndpointActionTx(ctx, tx, claim, hex.EncodeToString(nonce[:]), roots[0])
	return err
}
