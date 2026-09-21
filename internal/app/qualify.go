package app

import (
	"fmt"
	"math"
	"strings"

	"bidos/internal/store"
	"bidos/internal/verticals"
)

// QualificationView is the scorecard rendered on the bid overview (radar + flags).
type QualificationView struct {
	Qualification store.Qualification
	Criteria      []verticals.Criterion
	Weighted      float64
	Priority      string
}

// Qualify computes structured bid/no-bid signals from database state. It presents
// evidence and flags; the decision is human (Gate 2).
func (a *App) Qualify(orgID, bidID string) (QualificationView, error) {
	bid, err := a.DB.GetBid(orgID, bidID)
	if err != nil {
		return QualificationView{}, err
	}
	pack := a.Pack(orgID)
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	chunks, _ := a.DB.KnowledgeChunks(orgID)
	coveredCats := map[string]bool{}
	for _, c := range chunks {
		if c.DocApproval == "approved" {
			coveredCats[c.DocCategory] = true
		}
	}
	var evidenceBearing, catCovered, strongModerate, mandatory, mandatoryGap, unknowns, commercial, commercialGap, attachments int
	var flags []string
	for _, r := range reqs {
		if r.ChangeState == "removed" {
			continue
		}
		if pack.IsInstruction(r.Category) {
			if strings.Contains(strings.ToLower(r.Text), "attach") {
				attachments++
			}
			continue
		}
		evidenceBearing++
		if len(coveredCats) > 0 && categoryHasKnowledge(r.Category, coveredCats) {
			catCovered++
		}
		ans, has := answers[r.ID]
		if has && (ans.ConfidenceLabel == store.LabelStrong || ans.ConfidenceLabel == store.LabelModerate) {
			strongModerate++
		}
		if r.Mandatory {
			mandatory++
			if r.Status == store.StatusNeedsEvidence || r.Status == store.StatusNeedsSME {
				mandatoryGap++
			}
		}
		if r.AnchorStatus == "unanchored" || r.Status == store.StatusNeedsEvidence || r.Status == store.StatusNeedsSME || !r.Confirmed {
			unknowns++
		}
		if r.Category == "commercial_legal" {
			commercial++
			if r.Status == store.StatusNeedsEvidence || r.Status == store.StatusNeedsSME {
				commercialGap++
			}
		}
	}
	ratio := func(n, d int) float64 {
		if d == 0 {
			return 1
		}
		return float64(n) / float64(d)
	}
	scores := map[string]float64{}
	scores["capability_fit"] = ratio(catCovered, evidenceBearing)
	scores["evidence_availability"] = ratio(strongModerate, evidenceBearing)
	days := DaysUntil(bid.Deadline)
	workdays := math.Max(0, float64(days)*5/7)
	need := float64(len(reqs)) / 8
	if need == 0 {
		scores["deadline_feasibility"] = 1
	} else {
		scores["deadline_feasibility"] = math.Min(1, workdays/need)
	}
	scores["compliance_gaps"] = 1 - ratio(mandatoryGap, mandatory)
	if mandatory == 0 {
		scores["compliance_gaps"] = 1
	}
	scores["major_unknowns"] = 1 - ratio(unknowns, max(len(reqs), 1))
	scores["commercial_constraints"] = 1 - ratio(commercialGap, commercial)
	if commercial == 0 {
		scores["commercial_constraints"] = 1
	}
	if days < 0 {
		flags = append(flags, "Deadline has passed")
	} else if days <= 14 {
		flags = append(flags, fmt.Sprintf("Deadline in %d days", days))
	}
	if mandatoryGap > 0 {
		flags = append(flags, fmt.Sprintf("%d mandatory requirements lack approved evidence", mandatoryGap))
	}
	if commercialGap > 0 {
		flags = append(flags, fmt.Sprintf("%d commercial/legal items need a human position", commercialGap))
	}
	if attachments > 0 {
		flags = append(flags, fmt.Sprintf("%d submission instructions require attachments", attachments))
	}
	if unknowns > 0 {
		flags = append(flags, fmt.Sprintf("%d items unconfirmed, unanchored or unresolved", unknowns))
	}
	if evidenceBearing > 0 && strongModerate == 0 {
		flags = append(flags, "No answers generated yet; evidence availability is unmeasured")
	}
	weighted := 0.0
	for _, c := range pack.Qualification {
		weighted += c.Weight * scores[c.ID]
	}
	priority := "Low"
	switch {
	case weighted >= 0.75:
		priority = "High"
	case weighted >= 0.5:
		priority = "Medium"
	}
	q := store.Qualification{BidID: bidID, Scores: scores, Flags: flags, Priority: priority, Summary: fmt.Sprintf("Weighted signal %.0f%% across %d criteria. Human decision required.", weighted*100, len(pack.Qualification)), UpdatedAt: a.Now()}
	if err := a.DB.UpsertQualification(q); err != nil {
		return QualificationView{}, err
	}
	if bid.Status == store.BidIntake {
		a.AdvanceBidStatus(bid.ID, store.BidQualification)
	}
	return QualificationView{Qualification: q, Criteria: pack.Qualification, Weighted: weighted, Priority: priority}, nil
}

func categoryHasKnowledge(reqCategory string, covered map[string]bool) bool {
	// coarse mapping requirement category → knowledge categories with evidence
	m := map[string][]string{
		"company_overview":      {"company_overview", "standard_qa"},
		"product_functionality": {"product_documentation", "standard_qa"},
		"architecture_hosting":  {"product_documentation", "security_policy"},
		"security":              {"security_policy", "certification"},
		"privacy_compliance":    {"privacy_policy"},
		"implementation":        {"implementation_methodology"},
		"support_sla":           {"standard_qa", "product_documentation"},
		"commercial_legal":      {"commercial", "company_overview"},
		"experience_references": {"case_study", "customer_reference", "company_overview"},
		"team_staffing":         {"employee_bio"},
		"integration_data":      {"product_documentation"},
	}
	cats, ok := m[reqCategory]
	if !ok {
		return len(covered) > 0
	}
	for _, c := range cats {
		if covered[c] {
			return true
		}
	}
	return false
}
