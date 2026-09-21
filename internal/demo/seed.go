// Package demo seeds the synthetic workspace deterministically: fictional company,
// users per role, knowledge documents with expiry, the synthetic RFP (with traps and one
// injection line), the security questionnaire, an expired library entry, and settings.
// Seeding twice yields identical rows (fixed ids and timestamps).
package demo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bidos/internal/app"
	"bidos/internal/docs"
	"bidos/internal/store"
	"bidos/internal/verticals"
)

const (
	DemoOrgID = "org-demo"
	SeedTime  = "2026-09-01T09:00:00Z"
	BidTime   = "2026-09-08T10:00:00Z"
	rfpBidID  = "bid-rfp"
	vsaBidID  = "bid-questionnaire"
)

// Seeder seeds and resets demo organizations.
type Seeder struct{ App *app.App }

// Seed creates the demo organization for a pack. orgID "" uses DemoOrgID.
func (s *Seeder) Seed(ctx context.Context, packID, orgID, expiresAt string) (store.Organization, error) {
	if orgID == "" {
		orgID = DemoOrgID
	}
	pack := s.App.PackByID(packID)
	prev := s.App.NowFunc
	s.App.NowFunc = func() string { return SeedTime }
	defer func() { s.App.NowFunc = prev }()
	if _, err := s.App.DB.GetOrg(orgID); err == nil {
		return store.Organization{}, fmt.Errorf("organization %s already exists; reset first", orgID)
	}
	org := store.Organization{ID: orgID, Name: pack.Demo.Company.Name, Mode: "demo", VerticalPackID: packID, DataSensitivity: "synthetic", ExpiresAt: expiresAt, CreatedAt: SeedTime}
	if err := s.App.DB.CreateOrg(org); err != nil {
		return org, err
	}
	for k, v := range pack.Demo.Company.Settings {
		_ = s.App.DB.SetSetting(orgID, k, v)
	}
	_ = s.App.DB.SetSetting(orgID, "library.auto_promote", "true")
	_ = s.App.DB.SetSetting(orgID, "pipeline.auto_gates", "true")
	users := map[string]store.User{}
	for i, u := range pack.Demo.Company.Users {
		user := store.User{ID: fmt.Sprintf("%s-u%02d", orgID, i+1), OrgID: orgID, Name: u.Name, Email: u.Email, Role: u.Role, CreatedAt: SeedTime}
		if err := s.App.DB.CreateUser(user); err != nil {
			return org, err
		}
		users[u.Email] = user
	}
	pm := firstByRole(users, store.RoleProposalManager)
	reviewer := firstByRole(users, store.RoleReviewer)
	// knowledge documents
	for _, k := range pack.Demo.Knowledge {
		meta := map[string]any{"environment": "demo", "synthetic": true, "title": k.Title}
		if k.ClientName != "" {
			meta["clientName"] = k.ClientName
		}
		_, _, err := s.App.IngestDocument(ctx, app.IngestInput{ID: orgID + "-doc-" + k.Key, OrgID: orgID, Name: k.File, Data: []byte(k.Content), Category: k.Category, ApprovalState: k.Approval, ExpiresAt: k.ExpiresAt, ClientDisclosure: k.ClientDisclosure, Trust: "internal", SourceType: "seed", Version: k.Version, Tags: k.Tags, Metadata: meta, CreatedAt: SeedTime})
		if err != nil {
			return org, fmt.Errorf("knowledge %s: %w", k.Key, err)
		}
	}
	// library seed (e.g. expired legacy entry)
	for i, l := range pack.Demo.Library {
		var chunkIDs []string
		for _, ref := range l.Evidence {
			if id, ok := s.ResolveChunk(orgID, ref); ok {
				chunkIDs = append(chunkIDs, id)
			}
		}
		if chunkIDs == nil {
			chunkIDs = []string{}
		}
		entry := store.LibraryEntry{ID: fmt.Sprintf("%s-lib%02d", orgID, i+1), OrgID: orgID, CanonicalQuestion: l.Question, AnswerText: l.Answer, CitationChunkIDs: chunkIDs, Tags: l.Tags, Category: l.Category, OwnerID: pm.ID, ApprovedByID: reviewer.ID, ApprovedAt: l.ApprovedAt, ReviewBy: l.ReviewBy, SourceBidID: "", CreatedAt: SeedTime}
		if vecs := s.App.Embed(ctx, []string{l.Question}); len(vecs) == 1 {
			entry.Embedding = vecs[0]
		}
		if err := s.App.DB.CreateLibraryEntry(entry); err != nil {
			return org, err
		}
	}
	// the synthetic RFP bid
	rfp := pack.Demo.RFP
	bid, err := s.App.CreateBid(orgID, pm.ID, app.BidInput{ID: orgID + "-" + rfpBidID, Name: rfp.Title + " (" + rfp.ID + ")", BuyerName: rfp.Buyer, BuyerURL: rfp.BuyerURL, OwnerID: pm.ID, EstimatedValue: rfp.EstimatedValue, Deadline: rfp.Deadline, Status: store.BidIntake, Description: rfp.Description, CreatedAt: BidTime})
	if err != nil {
		return org, err
	}
	rfpBytes, err := RenderRFP(rfp)
	if err != nil {
		return org, err
	}
	rfpDoc, _, err := s.App.IngestDocument(ctx, app.IngestInput{ID: orgID + "-doc-rfp", OrgID: orgID, BidID: bid.ID, Name: rfp.FileName, Data: rfpBytes, Category: "rfp", ApprovalState: "n/a", Trust: "untrusted_external", SourceType: "seed", Metadata: map[string]any{"environment": "demo", "synthetic": true}, CreatedAt: BidTime})
	if err != nil {
		return org, err
	}
	traps := map[string]bool{}
	for _, it := range rfp.AllItems() {
		if it.Trap {
			traps[it.Code] = true
		}
	}
	if _, err := s.App.ExtractRequirements(ctx, orgID, bid.ID, rfpDoc.ID, app.ExtractOptions{IDPrefix: orgID + "-req-rfp-", Now: BidTime, TrapCodes: traps}); err != nil {
		return org, err
	}
	s.assignOwners(orgID, bid.ID, pack, users, reviewer)
	// the questionnaire bid (second synthetic RFP, XLSX)
	if q := pack.Demo.Questionnaire; len(q.Rows) > 0 {
		qbid, err := s.App.CreateBid(orgID, pm.ID, app.BidInput{ID: orgID + "-" + vsaBidID, Name: q.Title, BuyerName: rfp.Buyer, BuyerURL: rfp.BuyerURL, OwnerID: pm.ID, EstimatedValue: 0, Deadline: rfp.Deadline, Status: store.BidIntake, Description: "Vendor security questionnaire received as an XLSX workbook (Attachment E). Answers are written back into the original cells.", CreatedAt: BidTime})
		if err != nil {
			return org, err
		}
		qBytes, err := RenderQuestionnaire(q)
		if err != nil {
			return org, err
		}
		qDoc, _, err := s.App.IngestDocument(ctx, app.IngestInput{ID: orgID + "-doc-questionnaire", OrgID: orgID, BidID: qbid.ID, Name: q.FileName, Data: qBytes, Category: "questionnaire", ApprovalState: "n/a", Trust: "untrusted_external", SourceType: "seed", Metadata: map[string]any{"environment": "demo", "synthetic": true}, CreatedAt: BidTime})
		if err != nil {
			return org, err
		}
		qtraps := map[string]bool{}
		for _, r := range q.Rows {
			if r.Trap {
				qtraps[r.ID] = true
			}
		}
		if _, err := s.App.ExtractRequirements(ctx, orgID, qbid.ID, qDoc.ID, app.ExtractOptions{IDPrefix: orgID + "-req-q-", Now: BidTime, TrapCodes: qtraps}); err != nil {
			return org, err
		}
		s.assignOwners(orgID, qbid.ID, pack, users, reviewer)
	}
	// seeding is not user activity: keep the dataset byte-identical across runs
	_ = s.App.DB.Exec("DELETE FROM activity_log WHERE org_id = ?", orgID)
	_ = s.App.DB.Exec("DELETE FROM notifications WHERE org_id = ?", orgID)
	return org, nil
}

func (s *Seeder) assignOwners(orgID, bidID string, pack *verticals.Pack, users map[string]store.User, reviewer store.User) {
	reqs, _ := s.App.DB.ListRequirements(bidID)
	for _, r := range reqs {
		if email, ok := pack.Demo.Company.Routing[r.Category]; ok {
			r.OwnerID = users[email].ID
		}
		r.ReviewerID = reviewer.ID
		r.Confirmed = r.AnchorStatus == "anchored"
		_ = s.App.DB.UpdateRequirement(r)
	}
}

func firstByRole(users map[string]store.User, role string) store.User {
	best := store.User{}
	for _, u := range users {
		if u.Role == role && (best.ID == "" || u.ID < best.ID) {
			best = u
		}
	}
	return best
}

// ResolveChunk maps "doc-key:4.2" to the chunk id whose section starts with "4.2 ".
func (s *Seeder) ResolveChunk(orgID, ref string) (string, bool) {
	key, sec, _ := strings.Cut(ref, ":")
	chunks, err := s.App.DB.ChunksByDocument(orgID + "-doc-" + key)
	if err != nil {
		return "", false
	}
	for _, c := range chunks {
		parts := strings.Split(c.SectionPath, " > ")
		last := parts[len(parts)-1]
		if strings.HasPrefix(last, sec+" ") || strings.HasPrefix(last, sec+".") || last == sec {
			return c.ID, true
		}
	}
	return "", false
}

// Reset deletes the demo organization (rows + files) and seeds it again.
func (s *Seeder) Reset(ctx context.Context, packID, orgID string) (store.Organization, error) {
	if orgID == "" {
		orgID = DemoOrgID
	}
	if err := s.App.DB.DeleteOrg(orgID); err != nil {
		return store.Organization{}, err
	}
	_ = os.RemoveAll(filepath.Join(s.App.Files.Root, app.SafeName(orgID)))
	return s.Seed(ctx, packID, orgID, "")
}

// Ensure seeds the demo org when absent.
func (s *Seeder) Ensure(ctx context.Context, packID string) (store.Organization, error) {
	if org, err := s.App.DB.GetOrg(DemoOrgID); err == nil {
		return org, nil
	}
	return s.Seed(ctx, packID, DemoOrgID, "")
}

// RenderRFP writes the synthetic RFP as a DOCX so the real parser and extractor run on it.
func RenderRFP(r verticals.RFP) ([]byte, error) {
	var blocks []docs.Block
	blocks = append(blocks, docs.Block{Kind: "title", Text: r.Title + " — " + r.ID})
	blocks = append(blocks, docs.Block{Kind: "meta", Text: "Issued by " + r.Buyer + ". Synthetic demo document; no real procurement."})
	for _, p := range r.Intro {
		blocks = append(blocks, docs.Block{Kind: "p", Text: p})
	}
	for _, s := range r.Sections {
		blocks = append(blocks, docs.Block{Kind: "h1", Text: s.Number + " " + s.Title})
		for _, p := range s.Intro {
			blocks = append(blocks, docs.Block{Kind: "p", Text: p})
		}
		for _, it := range s.Items {
			blocks = append(blocks, docs.Block{Kind: "p", Text: it.Code + " " + it.Text})
		}
		for _, p := range s.Outro {
			blocks = append(blocks, docs.Block{Kind: "p", Text: p})
		}
	}
	if r.Injection != "" {
		blocks = append(blocks, docs.Block{Kind: "meta", Text: r.Injection})
	}
	return docs.BuildDocx(blocks)
}

// RenderRFPMarkdown gives a readable preview of the same content.
func RenderRFPMarkdown(r verticals.RFP) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", r.Title, r.ID)
	for _, p := range r.Intro {
		b.WriteString(p + "\n\n")
	}
	for _, s := range r.Sections {
		fmt.Fprintf(&b, "## %s %s\n\n", s.Number, s.Title)
		for _, p := range s.Intro {
			b.WriteString(p + "\n\n")
		}
		for _, it := range s.Items {
			fmt.Fprintf(&b, "%s %s\n\n", it.Code, it.Text)
		}
	}
	return b.String()
}

// RenderQuestionnaire writes the security questionnaire as an XLSX workbook.
func RenderQuestionnaire(q verticals.Questionnaire) ([]byte, error) {
	rows := [][]string{q.Columns}
	for _, r := range q.Rows {
		rows = append(rows, []string{r.ID, r.Domain, r.Question, "", ""})
	}
	return docs.BuildXlsx([]docs.SheetData{{Name: q.Sheet, Rows: rows}, {Name: "Instructions", Rows: [][]string{{"Complete the Vendor Response column for every question. Do not modify other columns."}, {"Synthetic demo workbook."}}}})
}

// RenderAddendum writes the synthetic addendum as a DOCX.
func RenderAddendum(a verticals.Addendum) ([]byte, error) {
	blocks := []docs.Block{{Kind: "title", Text: a.Title}}
	for _, p := range a.Paragraphs {
		blocks = append(blocks, docs.Block{Kind: "p", Text: p})
	}
	return docs.BuildDocx(blocks)
}
