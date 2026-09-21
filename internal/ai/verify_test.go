package ai

import "testing"

func TestVerify(t *testing.T) {
	chunks := map[int]string{
		1: "All customer data is encrypted at rest using AES-256 and in transit using TLS 1.2 or higher. Key rotation frequency is every 90 days.",
	}
	draft := "Northstar addresses this requirement as follows. All customer data is encrypted at rest using AES-256 and in transit using TLS 1.2 or higher [c1]. Keys are rotated every 30 days [c1]. We look forward to discussing further."
	v := Verify(draft, chunks, 0.5, []string{"Northstar"})
	if v.Factual != 2 || v.Unsupported != 1 {
		t.Fatalf("unexpected verification: %+v", v)
	}
	var bad Sentence
	for _, s := range v.Sentences {
		if s.Status == "unsupported" {
			bad = s
		}
	}
	if len(bad.Missing) == 0 || bad.Missing[0] != "30" {
		t.Fatalf("false number not flagged: %+v", bad)
	}
	if Label(v, 1, true, false, false, true) != "Weak evidence" {
		t.Fatal("label should be weak with unsupported sentences")
	}
	v2 := Verify("All customer data is encrypted at rest using AES-256 [c1].", chunks, 0.5, nil)
	if v2.Unsupported != 0 || Label(v2, 1, true, false, false, true) != "Strong evidence" || Label(v2, 1, false, false, false, true) != "Moderate evidence" {
		t.Fatalf("labels wrong: %+v", v2)
	}
	if Label(v2, 0, false, false, false, false) != "Insufficient evidence" {
		t.Fatal("insufficient label wrong")
	}
	v3 := Verify("Northstar is ISO 27001 certified.", chunks, 0.5, []string{"Northstar"})
	if v3.Unsupported != 1 {
		t.Fatalf("uncited claim not flagged: %+v", v3)
	}
}
