package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Parsed
	}{
		{
			name: "классический alert",
			line: `alert tcp $HOME_NET any -> $EXTERNAL_NET $HTTP_PORTS (msg:"ET MALWARE Possible Trojan Download"; flow:established,to_server; content:"GET"; http_method; classtype:trojan-activity; sid:2001234; rev:5;)`,
			want: Parsed{Action: "alert", Protocol: "tcp", SrcAddr: "$HOME_NET", SrcPort: "any",
				Direction: "->", DstAddr: "$EXTERNAL_NET", DstPort: "$HTTP_PORTS",
				SID: 2001234, Rev: 5, Msg: "ET MALWARE Possible Trojan Download", Classtype: "trojan-activity"},
		},
		{
			name: "drop с rev по умолчанию и списком адресов",
			line: `drop udp [1.2.3.4, 5.6.7.8] [53,5353] <> any any (msg:"Test DNS"; sid:1;)`,
			want: Parsed{Action: "drop", Protocol: "udp", SrcAddr: "[1.2.3.4, 5.6.7.8]", SrcPort: "[53,5353]",
				Direction: "<>", DstAddr: "any", DstPort: "any", SID: 1, Rev: 1, Msg: "Test DNS"},
		},
		{
			name: "msg с экранированной точкой с запятой и кавычкой",
			line: `alert http any any -> any any (msg:"Semi\;colon and \"quote\""; sid:42; rev:2; priority:3; reference:url,example.com; metadata:severity high;)`,
			want: Parsed{Action: "alert", Protocol: "http", SrcAddr: "any", SrcPort: "any",
				Direction: "->", DstAddr: "any", DstPort: "any", SID: 42, Rev: 2,
				Msg: `Semi;colon and "quote"`, Priority: 3,
				Reference: []string{"url,example.com"}, Metadata: []string{"severity high"}},
		},
		{
			name: "pass и log допустимы",
			line: `pass ip any any -> any any (msg:"pass"; sid:2;)`,
			want: Parsed{Action: "pass", Protocol: "ip", SrcAddr: "any", SrcPort: "any",
				Direction: "->", DstAddr: "any", DstPort: "any", SID: 2, Rev: 1, Msg: "pass"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.line)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got == nil {
				t.Fatal("Parse вернул nil для валидного правила")
			}
			if got.Action != c.want.Action || got.Protocol != c.want.Protocol ||
				got.SrcAddr != c.want.SrcAddr || got.SrcPort != c.want.SrcPort ||
				got.Direction != c.want.Direction || got.DstAddr != c.want.DstAddr ||
				got.DstPort != c.want.DstPort || got.SID != c.want.SID || got.Rev != c.want.Rev ||
				got.Msg != c.want.Msg || got.Classtype != c.want.Classtype ||
				got.Priority != c.want.Priority {
				t.Errorf("Parse = %+v, ожидалось %+v", got, c.want)
			}
			if len(got.Reference) != len(c.want.Reference) || len(got.Metadata) != len(c.want.Metadata) {
				t.Errorf("reference/metadata = %v/%v", got.Reference, got.Metadata)
			}
			if got.Raw != strings.TrimSpace(c.line) {
				t.Errorf("Raw не совпадает с исходной строкой")
			}
		})
	}
}

func TestParseSkipCommentsAndEmpty(t *testing.T) {
	for _, line := range []string{"", "   ", "# comment", "  # indented comment"} {
		p, err := Parse(line)
		if err != nil || p != nil {
			t.Errorf("Parse(%q) = (%v, %v), ожидалось (nil, nil)", line, p, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`alert tcp any any -> any any msg:"no parens"`, "скобк"},
		{`alert tcp any any -> any (msg:"short header"; sid:1;)`, "7 полей"},
		{`block tcp any any -> any any (msg:"bad action"; sid:1;)`, "action"},
		{`alert tcp any any => any any (msg:"bad dir"; sid:1;)`, "направление"},
		{`alert tcp any any -> any any (msg:"no sid";)`, "sid"},
		{`alert tcp any any -> any any (msg:"bad sid"; sid:abc;)`, "sid"},
		{`alert tcp any any -> any any (msg:"neg sid"; sid:-5;)`, "sid"},
		{`alert tcp any any -> any any (sid:1;)`, "msg"},
		{`alert tcp any any -> any any (msg:""; sid:1;)`, "msg"},
		{`alert tcp any any -> any any (msg:"bad rev"; sid:1; rev:0;)`, "rev"},
	}
	for _, c := range cases {
		_, err := Parse(c.line)
		if err == nil {
			t.Errorf("Parse(%q): ожидалась ошибка %q", c.line, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q): ошибка %q не содержит %q", c.line, err, c.want)
		}
	}
}

func TestParseReaderMixed(t *testing.T) {
	file := `# заголовок файла
alert tcp any any -> any any (msg:"ok one"; sid:100; rev:1;)

alert tcp any any -> any any (msg:"broken — no sid";)
not a rule at all
alert udp any any -> any any (msg:"ok two"; sid:101;)
`
	res := ParseReader(strings.NewReader(file), 0)
	if res.TotalLines != 6 {
		t.Errorf("TotalLines = %d, ожидалось 6", res.TotalLines)
	}
	if len(res.Rules) != 2 {
		t.Errorf("Rules = %d, ожидалось 2", len(res.Rules))
	}
	if len(res.Errors) != 2 {
		t.Fatalf("Errors = %d, ожидалось 2", len(res.Errors))
	}
	if res.Errors[0].Line != 4 || res.Errors[1].Line != 5 {
		t.Errorf("номера строк ошибок = %d, %d; ожидалось 4, 5", res.Errors[0].Line, res.Errors[1].Line)
	}
}

func TestParseReaderMaxErrors(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10; i++ {
		b.WriteString("broken line\n")
	}
	res := ParseReader(strings.NewReader(b.String()), 3)
	if len(res.Errors) != 3 {
		t.Errorf("Errors = %d, ожидалось 3 (maxErrors)", len(res.Errors))
	}
}

func TestParseReaderContinuation(t *testing.T) {
	file := "alert tcp any any -> any any (msg:\"multi \\\nline\"; sid:7;)\n"
	res := ParseReader(strings.NewReader(file), 0)
	if len(res.Rules) != 1 || res.Rules[0].SID != 7 {
		t.Errorf("continuation: Rules = %+v, Errors = %+v", res.Rules, res.Errors)
	}
}

// TestParseReader10K — 10 000 правил: корректность и время разбора.
func TestParseReader10K(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10000; i++ {
		fmt.Fprintf(&b, "alert tcp $HOME_NET any -> $EXTERNAL_NET any (msg:\"BENCH rule %d\"; content:\"abc\"; classtype:trojan-activity; sid:%d; rev:1;)\n", i, 9000000+i)
	}
	start := time.Now()
	res := ParseReader(strings.NewReader(b.String()), 0)
	dur := time.Since(start)
	if len(res.Rules) != 10000 {
		t.Errorf("Rules = %d, ожидалось 10000 (errors: %v)", len(res.Rules), res.Errors[:min(3, len(res.Errors))])
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %d, ожидалось 0", len(res.Errors))
	}
	t.Logf("10k правил разобрано за %s (%.0f правил/с)", dur, 10000/dur.Seconds())
	if dur > 5*time.Second {
		t.Errorf("разбор 10k правил занял %s — слишком долго", dur)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
