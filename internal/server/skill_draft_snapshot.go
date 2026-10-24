package server

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"

	"x-science/internal/skills"
)

type stagedSkillDraftSnapshot struct {
	container string
	owner     os.FileInfo
	source    string
	skill     skills.Skill
}

func (staged *stagedSkillDraftSnapshot) cleanup(filesystem *skillImportFilesystem) {
	if staged != nil && staged.owner != nil {
		_ = filesystem.removeOwnedDirectory(staged.container, staged.owner)
	}
}

// Reuse the private import staging capability and the existing declarative
// validator. No helper is executed and no live draft is reread for promotion.
func stageSkillDraftSnapshot(filesystem *skillImportFilesystem, snapshot skillDraftSnapshot) (*stagedSkillDraftSnapshot, error) {
	container, owner, err := filesystem.createPrivateDirectory()
	if err != nil {
		return nil, err
	}
	staged := &stagedSkillDraftSnapshot{container: container, owner: owner, source: filepath.Join(container, snapshot.name)}
	completed := false
	defer func() {
		if !completed {
			staged.cleanup(filesystem)
		}
	}()
	directory, err := filesystem.openDirectory(container)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, path := range snapshot.paths {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: snapshot.name + "/" + path, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(snapshot.resources[path]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	// Retain the existing case-collision/path/count/byte extraction contract.
	// The archive contains only the captured bytes; its metadata is not identity.
	if err := extractSkillArchive(directory, archive.Bytes()); err != nil {
		return nil, err
	}
	staged.skill, err = validateCompatibilitySkillPackage(staged.source, snapshot.name)
	if err != nil {
		return nil, err
	}
	completed = true
	return staged, nil
}

func (s *Server) validateSkillDraftSnapshot(snapshot skillDraftSnapshot) error {
	filesystem, err := openSkillImportFilesystem(s.fileRoot, false)
	if err != nil {
		return err
	}
	defer filesystem.close()
	staged, err := stageSkillDraftSnapshot(filesystem, snapshot)
	if err != nil {
		return err
	}
	staged.cleanup(filesystem)
	return nil
}
