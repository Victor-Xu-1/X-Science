//go:build linux

package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"x-science/internal/compute"
)

type managedEndpointPhysicalProcess struct {
	PID   int64  `json:"pid"`
	Ticks uint64 `json:"ticks"`
}

func (fixture *managedEndpointPhysicalFixture) unitFacts(ctx context.Context) (map[string]string, error) {
	name := compute.ManagedEndpointUnitName(fixture.instance.ID)
	raw, err := managedEndpointPhysicalCommand(ctx, fixture.systemctl, "--user", "show", "--property=LoadState,ActiveState,InvocationID,ControlGroup,MainPID,MemoryMax,TasksMax,CPUQuotaPerSecUSec", "--", name)
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	if values["LoadState"] == "not-found" && values["ActiveState"] == "inactive" {
		return values, nil
	}
	return values, err
}

func (fixture *managedEndpointPhysicalFixture) verifyPrivateWorker(ctx context.Context, identity compute.ManagedEndpointRuntimeIdentity) error {
	if identity.Kind != "native-unit" || identity.Unit != compute.ManagedEndpointUnitName(fixture.instance.ID) || !strings.HasSuffix(identity.ControlGroup, "/"+identity.Unit) || identity.ControlGroup == "/" {
		return errors.New("private worker identity does not match the exact fixture")
	}
	values, err := fixture.unitFacts(ctx)
	if err != nil {
		return err
	}
	if values["LoadState"] != "loaded" || values["InvocationID"] != identity.InvocationID || values["ControlGroup"] != identity.ControlGroup || values["MainPID"] != strconv.FormatInt(identity.PID, 10) {
		return errors.New("private worker physical incarnation changed; no cleanup authority")
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) != identity.BootID {
		return errors.New("private worker boot identity changed")
	}
	live, err := managedEndpointPhysicalProcessLive(managedEndpointPhysicalProcess{PID: identity.PID, Ticks: identity.StartTicks})
	if err != nil || !live {
		return errors.New("private worker original PID incarnation is not live")
	}
	if values["MemoryMax"] != "268435456" || values["TasksMax"] != "32" {
		return errors.New("private worker cgroup memory/task bounds were not applied")
	}
	quota, err := time.ParseDuration(values["CPUQuotaPerSecUSec"])
	if err != nil || quota <= 0 || quota > 500*time.Millisecond {
		return fmt.Errorf("private worker CPU cgroup bound is not positive and finite: %q", values["CPUQuotaPerSecUSec"])
	}
	return nil
}

func (fixture *managedEndpointPhysicalFixture) stopPrivateWorker(ctx context.Context, identity compute.ManagedEndpointRuntimeIdentity) error {
	values, err := fixture.unitFacts(ctx)
	if err != nil {
		return err
	}
	if values["LoadState"] != "not-found" && values["ActiveState"] != "inactive" && values["ActiveState"] != "failed" {
		if err := fixture.verifyPrivateWorker(ctx, identity); err != nil {
			return err
		}
		if _, err := managedEndpointPhysicalCommand(ctx, fixture.systemctl, "--user", "stop", "--", identity.Unit); err != nil {
			return err
		}
	}
	return managedEndpointPhysicalWait(ctx, func() error {
		if err := managedEndpointPhysicalCgroupEmpty(identity.ControlGroup); err != nil {
			return err
		}
		live, err := managedEndpointPhysicalProcessLive(managedEndpointPhysicalProcess{PID: identity.PID, Ticks: identity.StartTicks})
		if err != nil || live {
			return errors.Join(err, errors.New("original private worker process remains live"))
		}
		return nil
	})
}

func managedEndpointPhysicalCgroupEmpty(group string) error {
	if !strings.HasPrefix(group, "/") || group == "/" || strings.Contains(group, "..") || strings.ContainsRune(group, '\x00') {
		return errors.New("refuse to inspect an unbounded cgroup identity")
	}
	raw, err := os.ReadFile("/sys/fs/cgroup" + group + "/cgroup.events")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "populated 0" {
			return nil
		}
	}
	return errors.New("the exact original private cgroup remains populated or unknown")
}

func managedEndpointPhysicalProcessLive(expected managedEndpointPhysicalProcess) (bool, error) {
	if expected.PID < 1 || expected.Ticks < 1 {
		return false, errors.New("private process identity is incomplete")
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", expected.PID))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return false, errors.New("private process stat is malformed")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return false, errors.New("private process stat is incomplete")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	return err == nil && ticks == expected.Ticks && fields[0] != "Z" && fields[0] != "X", err
}

// The scientific runtime is deliberately not involved: a tiny standard-library
// HTTP process and its setsid child self-expire after 55s, inherit only the owned
// output descriptor, and write bounded records under their private directory.
const managedEndpointPhysicalPython = `
import http.server, json, os, signal, socketserver, subprocess, sys
mode, owner, port, directory, bind = sys.argv[1:6]
os.makedirs(directory, exist_ok=True)
signal.alarm(55)
def record(name):
    fields = open('/proc/self/stat').read().rpartition(')')[2].split()
    with open(os.path.join(directory, name), 'w') as out:
        json.dump({'pid': os.getpid(), 'ticks': int(fields[19])}, out)
record('parent.json')
counter = os.path.join(directory, 'starts')
with open(counter, 'a') as out:
    out.write(owner + chr(10))
child_code = """
import json, os, signal, sys, time
directory, owner = sys.argv[1:3]
signal.alarm(55)
fields = open('/proc/self/stat').read().rpartition(')')[2].split()
with open(os.path.join(directory, 'child.json'), 'w') as out:
    json.dump({'pid': os.getpid(), 'ticks': int(fields[19])}, out)
time.sleep(0.5)
for unused in range(12):
    print('late-owned-output:' + owner, flush=True)
    time.sleep(0.25)
time.sleep(50)
"""
child = subprocess.Popen([sys.executable, '-u', '-c', child_code, directory, owner], start_new_session=True)
print('physical-owner-start:' + owner, flush=True)
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        data = json.dumps({'owner': owner, 'starts': len(open(counter).readlines()), 'pid': os.getpid()}).encode()
        self.send_response(200)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)
    def log_message(self, *args):
        pass
class Server(socketserver.TCPServer):
    allow_reuse_address = True
with Server((bind, int(port)), Handler) as server:
    server.serve_forever(poll_interval=0.05)
`
