package server

import "net/http"

// Compute file reads share the same owner/project/explicit-grant authority as
// the file browser. Host-wide reads are only a passwordless loopback capability.
func (s *Server) computeLocalFileAccess(w http.ResponseWriter, r *http.Request, path, workspaceRoot string) (webFSAccess, bool) {
	userID, ok := attachmentUserID(w, r)
	if !ok {
		return webFSAccess{}, false
	}
	access, err := s.resolveWebFSPath(userID, path, workspaceRoot, false, true, s.allowWebFSLocalRead(r))
	if err != nil {
		writeWebFSError(w, err)
		return webFSAccess{}, false
	}
	return access, true
}
