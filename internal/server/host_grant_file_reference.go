package server

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const hostGrantFileReferenceScheme = "x-science-host-grant"

type hostGrantFileReference struct {
	grantID string
	path    string
}

func parseHostGrantFileReference(value string) (hostGrantFileReference, bool, error) {
	if !strings.HasPrefix(strings.ToLower(value), hostGrantFileReferenceScheme+":") {
		return hostGrantFileReference{}, false, nil
	}
	invalid := func() (hostGrantFileReference, bool, error) {
		return hostGrantFileReference{}, true, invalidWebConversationInputFile()
	}
	uri, err := url.Parse(value)
	if err != nil || len(value) > 8192 || uri.Scheme != hostGrantFileReferenceScheme || uri.Host != "file" ||
		uri.User != nil || uri.Path != "" || uri.Opaque != "" || uri.Fragment != "" || strings.Contains(value, "#") {
		return invalid()
	}
	query, err := url.ParseQuery(uri.RawQuery)
	if err != nil || len(query) != 2 || len(query["grant"]) != 1 || len(query["path"]) != 1 {
		return invalid()
	}
	grant, relative := query.Get("grant"), query.Get("path")
	if grant == "" || strings.TrimSpace(grant) != grant || !utf8.ValidString(grant) || !utf8.ValidString(relative) ||
		strings.ContainsRune(relative, '\\') || !fs.ValidPath(relative) || relative == "." || !filepath.IsLocal(filepath.FromSlash(relative)) {
		return invalid()
	}
	for _, value := range []string{grant, relative} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return invalid()
		}
	}
	return hostGrantFileReference{grantID: grant, path: relative}, true, nil
}

type hostGrantFileSnapshot struct {
	server   *Server
	ownerID  string
	grant    hostGrant
	epoch    uint64
	root     *os.Root
	rootInfo os.FileInfo
	file     *os.File
}

func (s *Server) openHostGrantFileSnapshot(ctx context.Context, ownerID string, reference hostGrantFileReference) (*hostGrantFileSnapshot, error) {
	s.hostGrantKernelMu.Lock()
	defer s.hostGrantKernelMu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return nil, errors.Join(ctx.Err(), cause)
	}
	grant, err := s.ownedHostFileGrant(ownerID, reference.grantID)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(grant.Path)
	if err != nil {
		return nil, invalidWebConversationInputFile()
	}
	rootInfo, err := hostGrantRootIdentity(root, grant)
	if err != nil {
		_ = root.Close()
		return nil, invalidWebConversationInputFile()
	}
	file, err := openGrantedRegularFileInRoot(root, grant, filepath.Join(grant.Path, filepath.FromSlash(reference.path)))
	if err != nil {
		_ = root.Close()
		return nil, invalidWebConversationInputFile()
	}
	return &hostGrantFileSnapshot{server: s, ownerID: ownerID, grant: grant,
		epoch: s.hostGrantKernelFenceEpoch[ownerID], root: root, rootInfo: rootInfo, file: file}, nil
}

func (s *Server) ownedHostFileGrant(ownerID, grantID string) (hostGrant, error) {
	if s.settingsStore == nil || strings.TrimSpace(ownerID) == "" {
		return hostGrant{}, invalidWebConversationInputFile()
	}
	grants, err := s.loadHostGrants(ownerID)
	if err != nil {
		return hostGrant{}, invalidWebConversationInputFile()
	}
	for _, grant := range grants {
		if grant.ID == grantID && filepath.IsAbs(grant.Path) && (grant.Mode == "read" || grant.Mode == "read_write") {
			return grant, nil
		}
	}
	return hostGrant{}, invalidWebConversationInputFile()
}

func hostGrantRootIdentity(root *os.Root, grant hostGrant) (os.FileInfo, error) {
	if err := verifyOpenedGrantedDirectory(root, []hostGrant{grant}); err != nil {
		return nil, err
	}
	return openedHostInputDirectoryIdentity(root)
}

func openedHostInputDirectoryIdentity(root *os.Root) (os.FileInfo, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	return info, errors.Join(statErr, file.Close())
}

func (snapshot *hostGrantFileSnapshot) commit(ctx context.Context, action func() error) error {
	s := snapshot.server
	s.hostGrantKernelMu.Lock()
	defer s.hostGrantKernelMu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return errors.Join(ctx.Err(), cause)
	}
	grant, err := s.ownedHostFileGrant(snapshot.ownerID, snapshot.grant.ID)
	if err != nil || grant.Path != snapshot.grant.Path || grant.Mode != snapshot.grant.Mode ||
		!grant.CreatedAt.Equal(snapshot.grant.CreatedAt) || !grant.UpdatedAt.Equal(snapshot.grant.UpdatedAt) ||
		s.hostGrantKernelFenceEpoch[snapshot.ownerID] != snapshot.epoch {
		return invalidWebConversationInputFile()
	}
	current, err := os.OpenRoot(grant.Path)
	if err != nil {
		return invalidWebConversationInputFile()
	}
	info, identityErr := hostGrantRootIdentity(current, grant)
	closeErr := current.Close()
	if identityErr != nil || closeErr != nil || !os.SameFile(snapshot.rootInfo, info) {
		return invalidWebConversationInputFile()
	}
	return action()
}

func (snapshot *hostGrantFileSnapshot) close() error {
	return errors.Join(snapshot.file.Close(), snapshot.root.Close())
}
