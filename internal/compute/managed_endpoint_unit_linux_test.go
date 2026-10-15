//go:build linux

package compute

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedEndpointAbsentUnitDoesNotOverridePopulatedOwner(t *testing.T) {
	for _, populated := range []bool{true, false} {
		t.Run(map[bool]string{true: "owner cgroup still populated", false: "owner cgroup empty"}[populated], func(t *testing.T) {
			directory := t.TempDir()
			control := filepath.Join(directory, "private-systemctl")
			// Reject any attempt to stop by name. The only permitted call is the
			// bounded show projection; no real unit or cgroup is operated upon.
			script := "#!/bin/sh\nif test \"$2\" != show; then exit 91; fi\nprintf 'LoadState=not-found\\nActiveState=inactive\\n'\n"
			if err := os.WriteFile(control, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			instance := ManagedEndpointOwnerInstance{ID: "private-stop-proof", Registration: ManagedEndpointRegistration{Owner: &ManagedEndpointOwnerSpec{Version: 1, Kind: "native-unit"}}}
			instance.Identity = ManagedEndpointRuntimeIdentity{Kind: "native-unit", Unit: ManagedEndpointUnitName(instance.ID), BootID: "private-boot", InvocationID: strings.Repeat("a", 32), ControlGroup: "/private-fixture-never-operated", PID: 42, StartTicks: 1234}
			reads := 0
			unit := &managedEndpointUnit{control: control, cgroupPopulated: func(group string) (bool, error) {
				reads++
				if group != instance.Identity.ControlGroup {
					t.Fatalf("foreign cgroup read: %q", group)
				}
				return populated, nil
			}}
			proof, err := unit.stop(context.Background(), instance)
			if reads != 1 {
				t.Fatalf("original cgroup not observed exactly once: %d", reads)
			}
			if populated {
				if err == nil || proof != nil {
					t.Fatalf("missing unit name invented a stopped owner: proof=%#v err=%v", proof, err)
				}
			} else if err != nil || proof == nil || !proof.CgroupEmpty || proof.Identity != instance.Identity {
				t.Fatalf("positive empty owner proof was lost: proof=%#v err=%v", proof, err)
			}
		})
	}
}
