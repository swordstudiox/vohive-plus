package modem

import "testing"

func TestShouldLogURCStateOnlyWhenSignatureChanges(t *testing.T) {
	m := &Manager{}

	if !m.shouldLogURC(m.formatURC("+CREG: 0,5")) {
		t.Fatal("first +CREG state should be logged")
	}
	if m.shouldLogURC(m.formatURC("+CREG: 0,5")) {
		t.Fatal("repeated +CREG state should not be logged")
	}
	if !m.shouldLogURC(m.formatURC("+CREG: 0,3")) {
		t.Fatal("changed +CREG state should be logged")
	}
	if !m.shouldLogURC(m.formatURC("+CGREG: 0,5")) {
		t.Fatal("different registration domain should keep its own state")
	}

	if !m.shouldLogURC(m.formatURC("+CEREG: 5")) {
		t.Fatal("single-field registration URC should be logged first")
	}
	if m.shouldLogURC(m.formatURC("+CEREG: 5")) {
		t.Fatal("repeated single-field registration URC should not be logged")
	}
	if !m.shouldLogURC(m.formatURC("+CEREG: 3")) {
		t.Fatal("changed single-field registration URC should be logged")
	}

	if !m.shouldLogURC(m.formatURC("+CPIN: READY")) {
		t.Fatal("first +CPIN state should be logged")
	}
	if m.shouldLogURC(m.formatURC("+CPIN: READY")) {
		t.Fatal("repeated +CPIN state should not be logged")
	}
	if !m.shouldLogURC(m.formatURC("+CPIN: SIM PIN")) {
		t.Fatal("changed +CPIN state should be logged")
	}

	if !m.shouldLogURC(m.formatURC("+QSIMSTAT: 1,1")) {
		t.Fatal("first +QSIMSTAT state should be logged")
	}
	if m.shouldLogURC(m.formatURC("+QSIMSTAT: 1,1")) {
		t.Fatal("repeated +QSIMSTAT state should not be logged")
	}
	if !m.shouldLogURC(m.formatURC("+QSIMSTAT: 1,0")) {
		t.Fatal("changed +QSIMSTAT state should be logged")
	}
}

func TestShouldLogURCKeepsEventNotifications(t *testing.T) {
	m := &Manager{}

	if !m.shouldLogURC(m.formatURC(`+CMTI: "ME",7`)) {
		t.Fatal("first +CMTI event should be logged")
	}
	if !m.shouldLogURC(m.formatURC(`+CMTI: "ME",7`)) {
		t.Fatal("repeated +CMTI event should still be logged")
	}
}

func TestShouldDispatchRDYForCPINReadyOnlyOnReadyTransition(t *testing.T) {
	m := &Manager{}

	if !m.shouldDispatchRDYForCPIN("READY") {
		t.Fatal("first READY should dispatch RDY")
	}
	if m.shouldDispatchRDYForCPIN("READY") {
		t.Fatal("repeated READY should not dispatch RDY")
	}
	if m.shouldDispatchRDYForCPIN("SIM PIN") {
		t.Fatal("non-READY state should not dispatch RDY")
	}
	if !m.shouldDispatchRDYForCPIN("READY") {
		t.Fatal("transition back to READY should dispatch RDY")
	}
}
