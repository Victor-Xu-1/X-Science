package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	kernelruntime "synon-go/internal/kernel"
	workspace "synon-go/internal/persistence/workspace"
)

// One SSH transfer path owns both direct file copies and managed job archives.
// rsync's I/O-idle timeout is not a total-transfer deadline. Its verified
// partial-directory protocol survives interruption without publishing an
// incomplete destination or inventing success from an old destination file.
func runAgentSSHCopy(ctx context.Context, provider workspace.ComputeProvider, localPath, remotePath string, upload bool) error {
	alias := strings.TrimPrefix(provider.Name, "ssh:")
	if !validComputeSSHTransferAlias(alias) || !agentComputeRemotePathPattern.MatchString(remotePath) {
		return errors.New("SSH transfer authority is invalid")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		return &kernelruntime.ProviderOperationError{Kind: "invalid_request", Message: "resumable SSH transfer requires rsync on the controller and selected host"}
	}
	if upload {
		info, err := os.Lstat(localPath)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("SSH transfer source must be a regular file")
		}
	} else {
		if info, err := os.Lstat(localPath); err == nil && !info.Mode().IsRegular() {
			return errors.New("SSH transfer destination must not be a link or special file")
		}
	}
	probe, err := runKernelComputeSSHCommand(ctx, provider, kernelComputeCommandRequest{
		Command: "command -v rsync >/dev/null && " + func() string {
			if upload {
				return "test ! -L " + shellSingleQuote(remotePath)
			}
			return "test -f " + shellSingleQuote(remotePath) + " && test ! -L " + shellSingleQuote(remotePath)
		}(), Intent: "Verify the selected SSH transfer endpoint", Timeout: 30 * time.Second,
	})
	if err != nil {
		return err
	}
	if int(numberValue(probe["exit_code"])) != 0 {
		return &kernelruntime.ProviderOperationError{Kind: "invalid_request", Message: "SSH transfer endpoint is unavailable or does not support verified resumable transfer"}
	}
	ssh := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}
	if user := strings.TrimSpace(stringValue(provider.SSHOverrides["user"])); user != "" {
		ssh = append(ssh, "-l", user)
	}
	if port := int(numberValue(provider.SSHOverrides["port"])); port > 0 {
		ssh = append(ssh, "-p", strconv.Itoa(port))
	}
	if identity := strings.TrimSpace(stringValue(provider.SSHOverrides["identityFile"])); identity != "" {
		ssh = append(ssh, "-i", identity)
	}
	for n := range ssh {
		ssh[n] = shellSingleQuote(ssh[n])
	}
	identity := sha256String(alias + "\x00" + remotePath + "\x00" + localPath + "\x00" + strconv.FormatBool(upload))[:24]
	// Force negotiation even when size and mtime match. Native transfer
	// checksums verify the complete file, without a quiet pre-transfer whole
	// file checksum pass that can outlast I/O-idle detection on huge datasets.
	args := []string{"--protect-args", "--ignore-times", "--times", "--sparse", "--partial-dir=.synon-biomed-partial-" + identity,
		"--timeout=120", "--info=progress2", "--outbuf=L", "--chmod=F600", "--rsh=" + strings.Join(ssh, " "), "--"}
	remote := alias + ":" + remotePath
	if upload {
		args = append(args, localPath, remote)
	} else {
		args = append(args, remote, localPath)
	}
	command := exec.CommandContext(ctx, "rsync", args...)
	stderr := &boundedComputeBuffer{limit: 64 << 10}
	command.Stderr = stderr
	// Native rsync observes network I/O itself; console output is bounded and
	// has no role in execution authority or renewing a fake progress heartbeat.
	command.Stdout = &boundedComputeBuffer{limit: 64 << 10}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &kernelruntime.ProviderOperationError{Kind: "transient", Message: fmt.Sprintf("verified SSH transfer interrupted; partial data retained: %s", strings.TrimSpace(stderr.String()))}
	}
	if !upload {
		info, err := os.Lstat(localPath)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("SSH transferred file failed post-transfer validation")
		}
		if err := os.Chmod(localPath, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func validComputeSSHTransferAlias(value string) bool {
	if value == "" || len(value) > 64 || strings.HasPrefix(value, "-") {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
