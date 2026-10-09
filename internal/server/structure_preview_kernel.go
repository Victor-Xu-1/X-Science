package server

import kernelruntime "synon-go/internal/kernel"

func (s *Server) startStructurePreviewKernel(spec kernelruntime.SessionSpec) (*kernelruntime.Worker, error) {
	// These one-shot kernels are owned by a bounded HTTP request, not by the
	// parent conversation's idle policy. The caller still closes the kernel on
	// completion/cancellation before removing its private operation directory.
	spec.Fresh = true
	return s.kernelManager.StartSession(spec)
}
