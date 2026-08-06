package modem

import (
	"reflect"
	"testing"
)

func TestParseCNUM(t *testing.T) {
	tests := []struct {
		name string
		resp string
		want string
	}{
		{
			name: "quoted tel field",
			resp: "\r\n+CNUM: \"My Number\",\"+8613800138000\",145\r\n\r\nOK\r\n",
			want: "+8613800138000",
		},
		{
			name: "quoted without plus",
			resp: "\r\n+CNUM: \"Own Number\",\"13800138000\",129\r\n\r\nOK\r\n",
			want: "13800138000",
		},
		{
			name: "multi line prefers first valid number",
			resp: "\r\n+CNUM: \"Own Number\",\"\",129\r\n+CNUM: \"Line 1\",\"+8613900139000\",145\r\n\r\nOK\r\n",
			want: "+8613900139000",
		},
		{
			name: "placeholder value ignored",
			resp: "\r\n+CNUM: \"Own Number\",\"FFFFFFFF\",129\r\n\r\nOK\r\n",
			want: "",
		},
		{
			name: "missing cnum line",
			resp: "\r\nOK\r\n",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCNUM(tt.resp)
			if got != tt.want {
				t.Fatalf("parseCNUM()=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestQueryMSISDNFallsBackToEFMSISDNWhenCNUMEmpty(t *testing.T) {
	m := newRunningTestManager(t)
	commands := make(chan string, 2)
	go func() {
		req := <-m.cmdChan
		commands <- req.cmd
		req.respChan <- "\r\nOK\r\n"
		req = <-m.cmdChan
		commands <- req.cmd
		req.respChan <- "+CRSM: 144,0,\"" + efMSISDNRecordHexForTest("8613800100500") + "\"\r\nOK\r\n"
	}()

	got, err := m.QueryMSISDN()
	if err != nil {
		t.Fatalf("QueryMSISDN() error = %v", err)
	}
	if got != "8613800100500" {
		t.Fatalf("QueryMSISDN() = %q, want 8613800100500", got)
	}

	close(commands)
	if gotCommands := drainCommands(commands); !reflect.DeepEqual(gotCommands, []string{"AT+CNUM", "AT+CRSM=178,28480,1,4,28"}) {
		t.Fatalf("commands = %#v", gotCommands)
	}
}

func efMSISDNRecordHexForTest(number string) string {
	rec := make([]byte, 28)
	for i := range rec {
		rec[i] = 0xFF
	}
	tail := rec[len(rec)-14:]
	bcd := encodeSwappedBCDForPhoneTest(number)
	tail[0] = byte(len(bcd) + 1)
	tail[1] = 0x91
	copy(tail[2:12], bcd)
	return bytesToUpperHexForPhoneTest(rec)
}

func encodeSwappedBCDForPhoneTest(num string) []byte {
	digits := []byte(num)
	out := make([]byte, (len(digits)+1)/2)
	for i := 0; i < len(digits); i++ {
		nib := digits[i] - '0'
		if i%2 == 0 {
			out[i/2] = nib
		} else {
			out[i/2] |= nib << 4
		}
	}
	if len(digits)%2 == 1 {
		out[len(out)-1] |= 0xF0
	}
	return out
}

func bytesToUpperHexForPhoneTest(in []byte) string {
	const table = "0123456789ABCDEF"
	out := make([]byte, 0, len(in)*2)
	for _, b := range in {
		out = append(out, table[b>>4], table[b&0x0F])
	}
	return string(out)
}
