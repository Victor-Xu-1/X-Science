package transcript

// InputWake returns the current committed-input broadcast generation. Capture
// it before querying runnable work so a commit between query and wait is not
// lost. It is an in-process hint, never a substitute for transactional claims;
// restart and cross-process recovery retain their bounded polling backstop.
func (r *Repository) InputWake() <-chan struct{} {
	if r == nil {
		return nil
	}
	r.inputWakeMu.Lock()
	defer r.inputWakeMu.Unlock()
	if r.inputWake == nil {
		r.inputWake = make(chan struct{})
	}
	return r.inputWake
}

func (r *Repository) signalCommittedInput() {
	r.inputWakeMu.Lock()
	defer r.inputWakeMu.Unlock()
	if r.inputWake != nil {
		// The repository owns the generation; consumers only receive from it.
		close(r.inputWake)
	}
	r.inputWake = make(chan struct{})
}

func (tx *ImmediateTransaction) markInputAdded(created bool, err error) {
	if created && err == nil {
		tx.inputAdded = true
	}
}
