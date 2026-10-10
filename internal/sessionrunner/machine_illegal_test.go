package sessionrunner

import "testing"

func TestMachineRejectsEmptySkipAheadAndPostTerminalWork(t *testing.T) {
	if _, _, err := (*Machine)(nil).Enter(PhaseClaim); err == nil {
		t.Fatal("nil machine accepted a phase")
	}
	machine := NewMachine()
	if _, _, err := machine.Enter(""); err == nil {
		t.Fatal("empty phase was accepted")
	}
	if _, _, err := machine.Enter(PhaseRecovery); err == nil {
		t.Fatal("recovery was accepted before claim")
	}
	if _, _, err := machine.Enter(PhaseClaim); err != nil {
		t.Fatal(err)
	}
	if _, _, err := machine.Enter(PhaseContext); err == nil {
		t.Fatal("context was accepted before recovery")
	}
	if _, _, err := machine.Enter(PhaseTerminal); err != nil {
		t.Fatal(err)
	}
	if _, _, err := machine.Enter(PhaseClaim); err == nil {
		t.Fatal("claim was accepted after terminal")
	}
	if machine.Current() != PhaseTerminal {
		t.Fatalf("current=%s want terminal", machine.Current())
	}
}

func TestMachineHistoryIsDetachedFromInternalTrace(t *testing.T) {
	machine := NewMachine()
	if _, _, err := machine.Enter(PhaseClaim); err != nil {
		t.Fatal(err)
	}
	history := machine.History()
	if len(history) != 1 || history[0].To != PhaseClaim {
		t.Fatalf("history=%#v", history)
	}
	history[0].To = PhaseTerminal
	if got := machine.History(); len(got) != 1 || got[0].To != PhaseClaim {
		t.Fatalf("internal history mutated through returned slice: %#v", got)
	}
}
