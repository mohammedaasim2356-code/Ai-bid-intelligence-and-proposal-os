package app

import (
	"fmt"
	"sort"
	"time"

	"bidos/internal/store"
)

// ROI is the transparent, editable savings model. Every number is an estimate.
type ROI struct {
	BaselineHoursPerQuestion float64
	LoadedHourlyRate         float64
	DraftSavingFraction      float64
	ReuseSavingFraction      float64
	HoursPerRFPAdmin         float64
	DraftedAnswers           int
	ReusedAnswers            int
	Bids                     int
	EstimatedHours           float64
	EstimatedSavings         float64
	Formula                  string
}

// Dashboard is the portfolio view.
type Dashboard struct {
	ActiveBids        int
	TotalRequirements int
	ApprovedAnswers   int
	Unresolved        int
	NeedsEvidence     int
	InReview          int
	CoveragePct       int
	AutomationPct     int
	ReusePct          int
	Blockers          int
	OpenTasks         int
	Bids              []BidSummary
	Deadlines         []BidSummary
	StageBoard        map[string]int
	StageOrder        []string
	ReviewerLoad      []NamedCount
	Labels            []NamedCount
	StatusSeries      []NamedCount
	CategorySeries    []NamedCount
	Activity          []store.Activity
	ActivityByDay     []NamedCount
	CycleTimeDays     float64
	CycleTimeMeasured bool
	ROI               ROI
	LibraryEntries    int
	LibraryExpired    int
	ProviderMode      string
}

type NamedCount struct {
	Name  string
	Value int
}

func (a *App) roi(orgID string, drafted, reused, bids int) ROI {
	r := ROI{
		BaselineHoursPerQuestion: a.SettingFloat(orgID, "roi.baseline_hours_per_question", 1.5),
		LoadedHourlyRate:         a.SettingFloat(orgID, "roi.loaded_hourly_rate", 95),
		DraftSavingFraction:      a.SettingFloat(orgID, "roi.draft_saving_fraction", 0.6),
		ReuseSavingFraction:      a.SettingFloat(orgID, "roi.reuse_saving_fraction", 0.9),
		HoursPerRFPAdmin:         a.SettingFloat(orgID, "roi.hours_per_rfp_admin", 12),
		DraftedAnswers:           drafted, ReusedAnswers: reused, Bids: bids,
	}
	r.EstimatedHours = float64(drafted)*r.BaselineHoursPerQuestion*r.DraftSavingFraction + float64(reused)*r.BaselineHoursPerQuestion*r.ReuseSavingFraction + float64(bids)*r.HoursPerRFPAdmin*0.5
	r.EstimatedSavings = r.EstimatedHours * r.LoadedHourlyRate
	r.Formula = fmt.Sprintf("hours = drafted(%d) × %.2g h × %.0f%% + reused(%d) × %.2g h × %.0f%% + bids(%d) × %.2g h admin × 50%%; savings = hours × %.0f/h", drafted, r.BaselineHoursPerQuestion, r.DraftSavingFraction*100, reused, r.BaselineHoursPerQuestion, r.ReuseSavingFraction*100, bids, r.HoursPerRFPAdmin, r.LoadedHourlyRate)
	return r
}

// Dashboard computes portfolio metrics from database state.
func (a *App) Dashboard(orgID string) (Dashboard, error) {
	d := Dashboard{StageBoard: map[string]int{}, StageOrder: []string{store.BidIntake, store.BidQualification, store.BidDrafting, store.BidReview, store.BidFinalQA, store.BidReady, store.BidClosedWon, store.BidClosedLost}}
	sums, err := a.BidSummaries(orgID)
	if err != nil {
		return d, err
	}
	d.Bids = sums
	labels := map[string]int{}
	statuses := map[string]int{}
	categories := map[string]int{}
	evidenceBearing, covered := 0, 0
	drafted, reused, manual, total := 0, 0, 0, 0
	var cycle []float64
	for _, s := range sums {
		d.StageBoard[s.Bid.Status]++
		if s.Bid.Status != store.BidClosedLost && s.Bid.Status != store.BidClosedWon && s.Bid.Status != store.BidArchived {
			d.ActiveBids++
			d.Deadlines = append(d.Deadlines, s)
		}
		d.TotalRequirements += s.Total
		d.ApprovedAnswers += s.Approved
		d.NeedsEvidence += s.NeedsEvidence + s.NeedsSME
		d.InReview += s.InReview
		d.Blockers += s.Blockers
		d.OpenTasks += s.OpenTasks
		for k, v := range s.ByStatus {
			statuses[k] += v
		}
		for k, v := range s.ByCategory {
			categories[k] += v
		}
		for k, v := range s.Labels {
			labels[k] += v
			if k == store.LabelStrong || k == store.LabelModerate {
				covered += v
			}
			evidenceBearing += v
		}
		answers, _ := a.DB.LatestAnswersByBid(s.Bid.ID)
		for _, ans := range answers {
			total++
			switch ans.GenerationMode {
			case "reused":
				reused++
			case "edited":
				manual++
			default:
				drafted++
			}
		}
		if s.Bid.Status == store.BidReady || s.Bid.Status == store.BidClosedWon || s.Bid.Status == store.BidClosedLost {
			if start, ok := store.ParseTime(s.Bid.CreatedAt); ok {
				if end, ok := store.ParseTime(s.Bid.UpdatedAt); ok && end.After(start) {
					cycle = append(cycle, end.Sub(start).Hours()/24)
				}
			}
		}
	}
	d.Unresolved = d.TotalRequirements - d.ApprovedAnswers
	if evidenceBearing > 0 {
		d.CoveragePct = covered * 100 / evidenceBearing
	}
	if total > 0 {
		d.AutomationPct = (drafted + reused) * 100 / total
		d.ReusePct = reused * 100 / total
	}
	sort.Slice(d.Deadlines, func(i, j int) bool { return d.Deadlines[i].Bid.Deadline < d.Deadlines[j].Bid.Deadline })
	users, _ := a.DB.UserMap(orgID)
	load := map[string]int{}
	tasks, _ := a.DB.TasksForOrg(orgID)
	for _, t := range tasks {
		if t.Status == "open" || t.Status == "in_progress" {
			load[users[t.AssigneeID].Name]++
		}
	}
	for _, s := range sums {
		reqs, _ := a.DB.ListRequirements(s.Bid.ID)
		for _, r := range reqs {
			if r.Status == store.StatusInReview && r.ReviewerID != "" {
				load[users[r.ReviewerID].Name+" (review)"]++
			}
		}
	}
	d.ReviewerLoad = sortedCounts(load)
	d.Labels = sortedCounts(labels)
	d.StatusSeries = sortedCounts(statuses)
	d.CategorySeries = sortedCounts(categories)
	d.Activity, _ = a.DB.ListActivity(orgID, 12)
	d.ActivityByDay = a.activityByDay(orgID, 14)
	if len(cycle) > 0 {
		sum := 0.0
		for _, c := range cycle {
			sum += c
		}
		d.CycleTimeDays, d.CycleTimeMeasured = sum/float64(len(cycle)), true
	}
	d.ROI = a.roi(orgID, drafted, reused, len(sums))
	lib, _ := a.DB.ListLibrary(orgID)
	d.LibraryEntries = len(lib)
	today := time.Now().UTC().Format("2006-01-02")
	for _, e := range lib {
		if e.ReviewBy != "" && e.ReviewBy < today {
			d.LibraryExpired++
		}
	}
	if a.AI != nil && a.AI.LiveAvailable() {
		d.ProviderMode = "live"
	} else {
		d.ProviderMode = "demo"
	}
	return d, nil
}

func sortedCounts(m map[string]int) []NamedCount {
	out := make([]NamedCount, 0, len(m))
	for k, v := range m {
		if k == "" {
			k = "Unassigned"
		}
		out = append(out, NamedCount{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value == out[j].Value {
			return out[i].Name < out[j].Name
		}
		return out[i].Value > out[j].Value
	})
	return out
}

func (a *App) activityByDay(orgID string, days int) []NamedCount {
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	acts, _ := a.DB.ActivitySince(orgID, since)
	counts := map[string]int{}
	for _, x := range acts {
		counts[x.CreatedAt[:10]]++
	}
	var out []NamedCount
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		out = append(out, NamedCount{Name: day[5:], Value: counts[day]})
	}
	return out
}

// UpdateROISettings stores the editable model inputs.
func (a *App) UpdateROISettings(orgID, userID string, values map[string]string) error {
	for k, v := range values {
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err != nil || f < 0 {
			return userErr("%s must be a non-negative number.", k)
		}
		if err := a.DB.SetSetting(orgID, "roi."+k, v); err != nil {
			return err
		}
	}
	a.Activity(orgID, userID, "settings.roi", "org", orgID, nil)
	return nil
}
