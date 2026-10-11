package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const runtimeEndpointLimit = 4096

type runtimeEndpointReceipt struct {
	Schema    int    `json:"schema"`
	PID       int    `json:"pid"`
	HealthURL string `json:"health_url"`
}

// A receipt describes the already-bound listener, never a second config read.
// Publication is atomic and refuses to replace any existing file or link.
func publishRuntimeEndpoint(path string, address net.Addr) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	path, err := privateRuntimeEndpointPath(path)
	if err != nil {
		return nil, err
	}
	healthURL, err := runtimeListenerHealthURL(address)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(runtimeEndpointReceipt{Schema: 1, PID: os.Getpid(), HealthURL: healthURL})
	if err != nil {
		return nil, err
	}
	if len(payload)+1 > runtimeEndpointLimit {
		return nil, errors.New("runtime endpoint exceeds its publication boundary")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".runtime-endpoint-*")
	if err != nil {
		return nil, err
	}
	published := false
	defer func() {
		_ = os.Remove(temporary.Name())
		if !published {
			_ = temporary.Close()
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		return nil, err
	}
	content := append(payload, '\n')
	if _, err := temporary.Write(content); err != nil {
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		return nil, err
	}
	info, err := temporary.Stat()
	if err != nil {
		return nil, err
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		return nil, err
	}
	published = true
	return func() {
		defer temporary.Close()
		observed, err := os.Lstat(path)
		if err != nil || !observed.Mode().IsRegular() || !os.SameFile(info, observed) {
			return
		}
		// Keep the original inode open through teardown, preventing inode reuse
		// from making an unrelated replacement look like the owned publication.
		if _, err := temporary.Seek(0, io.SeekStart); err != nil {
			return
		}
		current, err := io.ReadAll(io.LimitReader(temporary, runtimeEndpointLimit+1))
		if err == nil && bytes.Equal(current, content) {
			_ = os.Remove(path)
		}
	}, nil
}

func runtimeListenerHealthURL(address net.Addr) (string, error) {
	tcp, ok := address.(*net.TCPAddr)
	if !ok || tcp.Port <= 0 || tcp.Port > 65535 {
		return "", errors.New("runtime endpoint requires a bound TCP listener")
	}
	ip := tcp.IP
	if ip == nil || ip.IsUnspecified() {
		if ip == nil || ip.To4() != nil {
			ip = net.ParseIP("127.0.0.1")
		} else {
			ip = net.ParseIP("::1")
		}
	}
	host := ip.String()
	if tcp.Zone != "" {
		host += "%" + tcp.Zone
	}
	value := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(tcp.Port)), Path: "/health"}).String()
	return value, validateRuntimeHealthURL(value)
}

func privateRuntimeEndpointPath(value string) (string, error) {
	path, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return "", errors.New("runtime endpoint parent must be an existing non-symlink directory")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", errors.New("runtime endpoint parent must be private")
	}
	return path, nil
}

func readRuntimeEndpoint(path string, expectedPID int) (string, error) {
	if expectedPID <= 0 {
		return "", errors.New("runtime endpoint requires a positive expected pid")
	}
	path, err := privateRuntimeEndpointPath(path)
	if err != nil {
		return "", err
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > runtimeEndpointLimit ||
		(runtime.GOOS != "windows" && before.Mode().Perm()&0077 != 0) {
		return "", errors.New("runtime endpoint must be a bounded private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return "", errors.New("runtime endpoint changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(file, runtimeEndpointLimit+1))
	if err != nil || len(payload) > runtimeEndpointLimit {
		return "", errors.New("runtime endpoint exceeds its read boundary")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var receipt runtimeEndpointReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return "", fmt.Errorf("invalid runtime endpoint: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", errors.New("runtime endpoint contains trailing data")
	}
	if receipt.Schema != 1 || receipt.PID != expectedPID {
		return "", errors.New("runtime endpoint does not identify the owned child")
	}
	if err := validateRuntimeHealthURL(receipt.HealthURL); err != nil {
		return "", err
	}
	return receipt.HealthURL, nil
}

func validateRuntimeHealthURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.Path != "/health" || parsed.RawPath != "" {
		return errors.New("runtime endpoint must be a plain HTTP health URL")
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return errors.New("runtime endpoint requires a numeric host and port")
	}
	ip := net.ParseIP(strings.SplitN(host, "%", 2)[0])
	port, portErr := strconv.Atoi(portText)
	if ip == nil || ip.IsUnspecified() || portErr != nil || port <= 0 || port > 65535 {
		return errors.New("runtime endpoint requires a reachable numeric host and port")
	}
	return nil
}

func runRuntimeEndpointCLI(args []string, output io.Writer) error {
	flags := newOperationalFlagSet("runtime-endpoint")
	path := flags.String("file", "", "private owned-listener receipt")
	pid := flags.Int("pid", 0, "expected owned child process id")
	if err := parseOperationalFlags(flags, args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--file is required")
	}
	healthURL, err := readRuntimeEndpoint(*path, *pid)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, healthURL)
	return err
}
