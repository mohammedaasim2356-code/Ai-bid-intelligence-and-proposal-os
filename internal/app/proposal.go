package app

import (
	"fmt"
	"regexp"
	"strings"

	"bidos/internal/store"
)

// EnsureSections creates the pack's default section template for a bid when missing.
func (a *App) EnsureSections(orgID, bidID string) ([]store.ProposalSection, error) {
	existing, err := a.DB.ListSections(bidID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return existing, nil
	}
	pack := a.Pack(orgID)
	now := a.Now()
	for i, t := range pack.Sections {
		content := ""
		if strings.Contains(t.Hint, "PLACEHOLDER") {
			content = t.Hint
		}
		if err := a.DB.CreateSection(store.ProposalSection{ID: bidID + "-s" + fmt.Sprintf("%02d", i), BidID: bidID, Key: t.Key, Title: t.Title, SortOrder: i, Content: content, AnswerIDs: []string{}, UpdatedAt: now}); err != nil {
			return nil, err
		}
	}
	return a.DB.ListSections(bidID)
}

func (a *App) sectionInOrg(orgID, sectionID string) (store.ProposalSection, error) {
	s, err := a.DB.GetSection(sectionID)
	if err != nil {
		return s, err
	}
	if _, err := a.DB.GetBid(orgID, s.BidID); err != nil {
		return s, store.ErrNotFound
	}
	return s, nil
}

func (a *App) UpdateSection(orgID, userID, sectionID, title, content string) error {
	s, err := a.sectionInOrg(orgID, sectionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(title) != "" {
		s.Title = strings.TrimSpace(title)
	}
	s.Content, s.UpdatedAt = strings.TrimRight(content, " \n"), a.Now()
	if err := a.DB.UpdateSection(s); err != nil {
		return err
	}
	a.Activity(orgID, userID, "proposal.section_updated", "section", s.ID, map[string]any{"title": s.Title})
	return nil
}

func (a *App) AddSection(orgID, userID, bidID, title string) (store.ProposalSection, error) {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return store.ProposalSection{}, err
	}
	if strings.TrimSpace(title) == "" {
		return store.ProposalSection{}, userErr("Section title is required.")
	}
	existing, _ := a.DB.ListSections(bidID)
	s := store.ProposalSection{ID: store.NewID(), BidID: bidID, Key: "custom-" + store.NewID()[:6], Title: strings.TrimSpace(title), SortOrder: len(existing), AnswerIDs: []string{}, UpdatedAt: a.Now()}
	if err := a.DB.CreateSection(s); err != nil {
		return s, err
	}
	a.Activity(orgID, userID, "proposal.section_added", "section", s.ID, nil)
	return s, nil
}

// ReorderSections applies a new order of section ids.
func (a *App) ReorderSections(orgID, userID, bidID string, ids []string) error {
	if _, err := a.DB.GetBid(orgID, bidID); err != nil {
		return err
	}
	sections, err := a.DB.ListSections(bidID)
	if err != nil {
		return err
	}
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	for _, s := range sections {
		if p, ok := pos[s.ID]; ok {
			s.SortOrder = p
		} else {
			s.SortOrder = len(ids) + s.SortOrder
		}
		s.UpdatedAt = a.Now()
		if err := a.DB.UpdateSection(s); err != nil {
			return err
		}
	}
	a.Activity(orgID, userID, "proposal.reordered", "bid", bidID, nil)
	return nil
}

// MoveSection moves a section up or down by one.
func (a *App) MoveSection(orgID, userID, sectionID string, delta int) error {
	s, err := a.sectionInOrg(orgID, sectionID)
	if err != nil {
		return err
	}
	sections, _ := a.DB.ListSections(s.BidID)
	ids := make([]string, len(sections))
	idx := -1
	for i, x := range sections {
		ids[i] = x.ID
		if x.ID == s.ID {
			idx = i
		}
	}
	j := idx + delta
	if idx < 0 || j < 0 || j >= len(ids) {
		return nil
	}
	ids[idx], ids[j] = ids[j], ids[idx]
	return a.ReorderSections(orgID, userID, s.BidID, ids)
}

// InsertAnswer places an approved answer block in a section.
func (a *App) InsertAnswer(orgID, userID, sectionID, answerID string) error {
	s, err := a.sectionInOrg(orgID, sectionID)
	if err != nil {
		return err
	}
	ans, req, err := a.answerInOrg(orgID, answerID)
	if err != nil {
		return err
	}
	if ans.Status != store.StatusApproved {
		return userErr("Only approved answers can be inserted into the proposal (%s is %s).", req.Code, ans.Status)
	}
	for _, id := range s.AnswerIDs {
		if id == ans.ID {
			return nil
		}
	}
	s.AnswerIDs = append(s.AnswerIDs, ans.ID)
	s.UpdatedAt = a.Now()
	if err := a.DB.UpdateSection(s); err != nil {
		return err
	}
	a.Activity(orgID, userID, "proposal.answer_inserted", "section", s.ID, map[string]any{"requirement": req.Code})
	return nil
}

func (a *App) RemoveAnswer(orgID, userID, sectionID, answerID string) error {
	s, err := a.sectionInOrg(orgID, sectionID)
	if err != nil {
		return err
	}
	var keep []string
	for _, id := range s.AnswerIDs {
		if id != answerID {
			keep = append(keep, id)
		}
	}
	s.AnswerIDs, s.UpdatedAt = keep, a.Now()
	if s.AnswerIDs == nil {
		s.AnswerIDs = []string{}
	}
	return a.DB.UpdateSection(s)
}

// AutoPlaceAnswers inserts every approved answer into the section matching its category.
func (a *App) AutoPlaceAnswers(orgID, userID, bidID string) (int, error) {
	sections, err := a.EnsureSections(orgID, bidID)
	if err != nil {
		return 0, err
	}
	byKey := map[string]store.ProposalSection{}
	for _, s := range sections {
		byKey[s.Key] = s
	}
	placed := map[string]bool{}
	for _, s := range sections {
		for _, id := range s.AnswerIDs {
			placed[id] = true
		}
	}
	reqs, _ := a.DB.ListRequirements(bidID)
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	pack := a.Pack(orgID)
	n := 0
	for _, r := range reqs {
		ans, ok := answers[r.ID]
		if !ok || ans.Status != store.StatusApproved || placed[ans.ID] || pack.IsInstruction(r.Category) || r.ChangeState == "removed" {
			continue
		}
		key := sectionKeyFor(r.Category)
		s, ok := byKey[key]
		if !ok {
			s = byKey["solution"]
		}
		if s.ID == "" {
			continue
		}
		s.AnswerIDs = append(s.AnswerIDs, ans.ID)
		s.UpdatedAt = a.Now()
		if err := a.DB.UpdateSection(s); err != nil {
			return n, err
		}
		byKey[s.Key] = s
		n++
	}
	a.Activity(orgID, userID, "proposal.auto_placed", "bid", bidID, map[string]any{"count": n})
	return n, nil
}

func sectionKeyFor(category string) string {
	switch category {
	case "company_overview":
		return "company_overview"
	case "security", "privacy_compliance":
		return "security"
	case "implementation", "support_sla":
		return "implementation"
	case "team_staffing":
		return "team"
	case "experience_references":
		return "experience"
	case "commercial_legal":
		return "assumptions"
	case "firm_overview":
		return "company_overview"
	case "practice_capability", "relevant_matters":
		return "experience"
	case "team_bios":
		return "team"
	case "rates_fees", "billing_guidelines", "insurance":
		return "assumptions"
	case "information_security", "conflicts":
		return "security"
	}
	return "solution"
}

// Assembled proposal ------------------------------------------------------------------

type Footnote struct {
	Number  int
	Doc     string
	Section string
	Locator string
	ChunkID string
	Excerpt string
}

type AssembledBlock struct {
	Requirement store.Requirement
	Answer      store.Answer
	Text        string // markers replaced with [n]
	Notes       []Footnote
}

type AssembledSection struct {
	Section     store.ProposalSection
	Blocks      []AssembledBlock
	Placeholder bool
	Empty       bool
}

type Assembled struct {
	Bid       store.Bid
	Org       store.Organization
	Sections  []AssembledSection
	Footnotes []Footnote
	Words     int
	Unplaced  []AssembledBlock
	Answered  []AssembledBlock // every requirement with its latest answer (matrix appendix)
}

var rePlaceholder = regexp.MustCompile(`(?i)\[placeholder[^\]]*\]|\bTBD\b|\bTODO\b|lorem ipsum|\bXXX+\b|\[insert[^\]]*\]`)

// Assemble resolves sections, inserted answers and citation footnotes.
func (a *App) Assemble(orgID, bidID string) (Assembled, error) {
	bid, err := a.DB.GetBid(orgID, bidID)
	if err != nil {
		return Assembled{}, err
	}
	org, _ := a.DB.GetOrg(orgID)
	sections, err := a.EnsureSections(orgID, bidID)
	if err != nil {
		return Assembled{}, err
	}
	out := Assembled{Bid: bid, Org: org}
	reqByID := map[string]store.Requirement{}
	reqs, _ := a.DB.ListRequirements(bidID)
	for _, r := range reqs {
		reqByID[r.ID] = r
	}
	placed := map[string]bool{}
	noteNo := 0
	makeBlock := func(ans store.Answer) AssembledBlock {
		text := ans.FinalText
		if text == "" {
			text = ans.DraftText
		}
		b := AssembledBlock{Requirement: reqByID[ans.RequirementID], Answer: ans}
		cites, _ := a.DB.CitationsForAnswer(ans.ID)
		local := map[int]int{}
		for _, c := range cites {
			noteNo++
			loc := c.SectionPath
			if c.SheetName != "" {
				loc = c.SheetName + "!" + c.CellRange
			} else if c.PageNumber > 0 {
				loc = fmt.Sprintf("page %d", c.PageNumber)
			}
			fn := Footnote{Number: noteNo, Doc: DocDisplayName(c.DocName, c.DocVersion), Section: c.SectionPath, Locator: loc, ChunkID: c.ChunkID, Excerpt: truncateRunes(c.Text, 200)}
			out.Footnotes = append(out.Footnotes, fn)
			b.Notes = append(b.Notes, fn)
			local[c.Marker] = noteNo
		}
		b.Text = reMarkerAll.ReplaceAllStringFunc(text, func(m string) string {
			n := 0
			fmt.Sscanf(m, "[c%d]", &n)
			if g, ok := local[n]; ok {
				return fmt.Sprintf("[%d]", g)
			}
			return ""
		})
		return b
	}
	for _, s := range sections {
		as := AssembledSection{Section: s, Placeholder: rePlaceholder.MatchString(s.Content)}
		for _, id := range s.AnswerIDs {
			ans, err := a.DB.GetAnswer(id)
			if err != nil {
				continue
			}
			placed[ans.ID] = true
			as.Blocks = append(as.Blocks, makeBlock(ans))
		}
		as.Empty = strings.TrimSpace(s.Content) == "" && len(as.Blocks) == 0
		out.Words += len(strings.Fields(s.Content))
		for _, b := range as.Blocks {
			out.Words += len(strings.Fields(b.Text))
		}
		out.Sections = append(out.Sections, as)
	}
	answers, _ := a.DB.LatestAnswersByBid(bidID)
	for _, r := range reqs {
		if r.ChangeState == "removed" {
			continue
		}
		ans, ok := answers[r.ID]
		if !ok {
			out.Answered = append(out.Answered, AssembledBlock{Requirement: r})
			continue
		}
		b := makeBlock(ans)
		out.Answered = append(out.Answered, b)
		if ans.Status == store.StatusApproved && !placed[ans.ID] && !a.Pack(orgID).IsInstruction(r.Category) {
			out.Unplaced = append(out.Unplaced, b)
		}
	}
	return out, nil
}

var reMarkerAll = regexp.MustCompile(`\s?\[c\d+\]`)
