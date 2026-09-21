package verticals

import "testing"

func TestLoadSaasIT(t *testing.T) {
	p, err := Load("saas-it")
	if err != nil {
		t.Fatal(err)
	}
	items := p.Demo.RFP.AllItems()
	traps := 0
	for _, it := range items {
		if it.Trap {
			traps++
		}
	}
	if len(items) < 60 || len(items) > 100 {
		t.Fatalf("expected 60-100 requirements, got %d", len(items))
	}
	if traps < 10 {
		t.Fatalf("expected >= 10 traps, got %d", traps)
	}
	if len(p.Demo.Knowledge) < 10 || p.Demo.Knowledge[0].Content == "" {
		t.Fatal("knowledge docs not loaded")
	}
	if p.Classify("Describe your encryption key management and MFA") != "security" {
		t.Fatalf("classify failed: %s", p.Classify("Describe your encryption key management and MFA"))
	}
	if !p.IsInstruction("submission_instructions") {
		t.Fatal("instruction flag lost")
	}
	if len(p.Demo.Questionnaire.Rows) < 20 || p.Demo.Addendum.Title == "" {
		t.Fatal("questionnaire/addendum missing")
	}
}
