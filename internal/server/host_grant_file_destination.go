package server

import "os"

// External project input writes need their existing writable-project authority
// independently of the source grant. Managed task roots retain their existing
// frame-owned input route and do not acquire a new host grant.
func (s *Server) hostFileDestinationCheck(ownerID, projectID, path string, directory, inputs *os.Root) (func() error, error) {
	directoryInfo, err := openedHostInputDirectoryIdentity(directory)
	if err != nil {
		return nil, invalidWebConversationInputFile()
	}
	inputInfo, err := openedHostInputDirectoryIdentity(inputs)
	if err != nil {
		return nil, invalidWebConversationInputFile()
	}
	// Called inside the source snapshot's short hostGrantKernelMu commit.
	// The order matches grant mutation: grant mutex -> settings read. No
	// workspace mutation/kernel termination, content copy or hash runs here.
	return func() error {
		if err := s.validateProjectPathAuthority(ownerID, projectID, path); err != nil {
			return invalidWebConversationInputFile()
		}
		current, err := os.OpenRoot(path)
		if err != nil {
			return invalidWebConversationInputFile()
		}
		defer current.Close()
		currentInfo, err := openedHostInputDirectoryIdentity(current)
		if err != nil || !os.SameFile(directoryInfo, currentInfo) {
			return invalidWebConversationInputFile()
		}
		currentInputs, err := current.OpenRoot("inputs")
		if err != nil {
			return invalidWebConversationInputFile()
		}
		defer currentInputs.Close()
		currentInputInfo, err := openedHostInputDirectoryIdentity(currentInputs)
		if err != nil || !os.SameFile(inputInfo, currentInputInfo) {
			return invalidWebConversationInputFile()
		}
		return nil
	}, nil
}
