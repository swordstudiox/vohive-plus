package modem

import "testing"

func TestBuildCSCACommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "international", in: "+8613800250500", want: `AT+CSCA="8613800250500",145`},
		{name: "national", in: "10086", want: `AT+CSCA="10086",129`},
		{name: "spaced", in: "+86 138-0025-0500", want: `AT+CSCA="8613800250500",145`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCSCACommand(tt.in)
			if err != nil {
				t.Fatalf("buildCSCACommand() error=%v", err)
			}
			if got != tt.want {
				t.Fatalf("buildCSCACommand()=%q want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCSCACommandRejectsInvalidValue(t *testing.T) {
	if _, err := buildCSCACommand("+86138abc"); err == nil {
		t.Fatal("buildCSCACommand() err=nil, want invalid SMSC error")
	}
}

func TestPreferredSMSCForSendFallsBackToConfiguredValue(t *testing.T) {
	if got := preferredSMSCForSend("", " +8613800250500 "); got != "+8613800250500" {
		t.Fatalf("preferredSMSCForSend()=%q want configured fallback", got)
	}
}

func TestPreferredSMSCForSendKeepsQueriedValueFirst(t *testing.T) {
	if got := preferredSMSCForSend(" +8613010112500 ", "+8613800250500"); got != "+8613010112500" {
		t.Fatalf("preferredSMSCForSend()=%q want queried SMSC first", got)
	}
}
