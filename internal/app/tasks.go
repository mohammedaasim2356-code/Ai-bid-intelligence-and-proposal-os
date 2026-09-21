package app

import (
	"context"
	"strings"
	"time"

	"bidos/internal/store"
)

// RouteOwner resolves the SME for a category: org routing settings → pack routing map →
// pack category owner role → any proposal manager.
func (a *App) RouteOwner(orgID, category string) (store.User, bool) {
	pack := a.Pack(orgID)
	if email := a.Setting(orgID, "routing."+category, ""); email != "" {
		if u, err := a.DB.UserByEmail(orgID, email); err == nil {
			return u, true
		}
	}
	if email, ok := pack.Demo.Company.Routing[category]; ok {
		if u, err := a.DB.UserByEmail(orgID, email); err == nil {
			return u, true
		}
	}
	role := pack.Category(category).OwnerRole
	if role != "" {
		if users, _ := a.DB.UsersByRole(orgID, role); len(users) > 0 {
			return users[0], true
		}
	}
	if users, _ := a.DB.UsersByRole(orgID, store.RoleProposalManager); len(users) > 0 {
		return users[0], true
	}
	return store.User{}, false
}

// CreateSMETask opens (or reuses) the SME task for a requirement with the generated question.
func (a *App) CreateSMETask(orgID string, req store.Requirement, question, requestedBy, bidDeadline string) (store.Task, error) {
	open, _ := a.DB.OpenTasksForRequirement(req.ID)
	if len(open) > 0 {
		t := open[0]
		if question != "" && t.Question != question {
			t.Question, t.UpdatedAt = question, a.Now()
			_ = a.DB.UpdateTask(t)
		}
		return t, nil
	}
	owner, _ := a.RouteOwner(orgID, req.Category)
	due := ""
	if t, ok := store.ParseTime(bidDeadline); ok {
		d := t.Add(-7 * 24 * time.Hour)
		if d.Before(time.Now()) {
			d = time.Now().Add(48 * time.Hour)
		}
		due = store.FormatTime(d)
	} else {
		due = store.FormatTime(time.Now().Add(5 * 24 * time.Hour))
	}
	now := a.Now()
	task := store.Task{ID: store.NewID(), BidID: req.BidID, RequirementID: req.ID, AssigneeID: owner.ID, RequestedBy: requestedBy, Type: "sme", Status: "open", DueAt: due, Description: "Evidence needed for " + req.Code + ": " + req.Text, Question: question, CreatedAt: now, UpdatedAt: now}
	if err := a.DB.CreateTask(task); err != nil {
		return task, err
	}
	_ = a.DB.SetRequirementStatus(req.ID, requirementStatusForTask(req), now)
	a.Activity(orgID, requestedBy, "task.created", "task", task.ID, map[string]any{"requirement": req.Code, "assignee": owner.Name})
	if owner.ID != "" {
		a.Notify(orgID, owner.ID, "SME question: "+req.Code, question, "/tasks/"+task.ID)
	}
	if a.Hooks.TaskCreated != nil {
		a.Hooks.TaskCreated(task)
	}
	return task, nil
}

func requirementStatusForTask(req store.Requirement) string {
	if req.Status == store.StatusNeedsEvidence {
		return store.StatusNeedsEvidence
	}
	return store.StatusNeedsSME
}

// AssignTask changes the assignee (and optionally the requirement owner).
func (a *App) AssignTask(orgID, userID, taskID, assigneeID, due string) error {
	t, err := a.taskInOrg(orgID, taskID)
	if err != nil {
		return err
	}
	if _, err := a.DB.GetUser(assigneeID); err != nil {
		return userErr("Assignee not found.")
	}
	if due != "" {
		if _, ok := store.ParseTime(due); !ok {
			return userErr("Due date %q is not valid.", due)
		}
		t.DueAt = due
	}
	t.AssigneeID, t.UpdatedAt = assigneeID, a.Now()
	if err := a.DB.UpdateTask(t); err != nil {
		return err
	}
	a.Activity(orgID, userID, "task.assigned", "task", t.ID, map[string]any{"assignee": assigneeID})
	a.Notify(orgID, assigneeID, "Task assigned to you", t.Description, "/tasks/"+t.ID)
	return nil
}

// AssignRequirement creates an SME task for a requirement directly from the workspace.
func (a *App) AssignRequirement(orgID, userID, reqID, assigneeID, question, due string) (store.Task, error) {
	req, bid, err := a.requirementInOrg(orgID, reqID)
	if err != nil {
		return store.Task{}, err
	}
	if question == "" {
		question = "Please review the draft for " + req.Code + " and provide or confirm the facts we should cite."
	}
	task, err := a.CreateSMETask(orgID, req, question, userID, bid.Deadline)
	if err != nil {
		return task, err
	}
	if assigneeID != "" && assigneeID != task.AssigneeID {
		if err := a.AssignTask(orgID, userID, task.ID, assigneeID, due); err != nil {
			return task, err
		}
		task.AssigneeID = assigneeID
	}
	return task, nil
}

// RespondTask handles the SME action. "answer" turns the response into a citable SME
// statement, re-drafts and re-verifies the requirement and moves it to In Review.
func (a *App) RespondTask(ctx context.Context, orgID, userID, taskID, action, response string) error {
	t, err := a.taskInOrg(orgID, taskID)
	if err != nil {
		return err
	}
	now := a.Now()
	switch action {
	case "answer":
		if strings.TrimSpace(response) == "" {
			return userErr("Write the statement or fact the proposal can cite.")
		}
		req, err := a.DB.GetRequirement(t.RequirementID)
		if err != nil {
			return err
		}
		user, _ := a.DB.GetUser(userID)
		content := "# SME statement for requirement " + req.Code + "\n\n## 1 Statement\n\n" + strings.TrimSpace(response) + "\n"
		_, _, err = a.IngestDocument(ctx, IngestInput{OrgID: orgID, UserID: userID, Name: "SME-statement-" + SafeName(req.Code) + "-" + store.NewID()[:6] + ".md", Data: []byte(content), Category: "standard_qa", ApprovalState: "approved", ExpiresAt: a.DefaultExpiry(orgID, "standard_qa"), SourceType: "sme", Tags: []string{"sme", req.Category}, Metadata: map[string]any{"author": user.Name, "requirement": req.Code, "task": t.ID}})
		if err != nil {
			return err
		}
		t.Response, t.Status, t.UpdatedAt = strings.TrimSpace(response), "done", now
		if err := a.DB.UpdateTask(t); err != nil {
			return err
		}
		res, err := a.GenerateAnswer(ctx, orgID, req.ID, GenerateOptions{UserID: userID, Force: true, Mode: "sme_update"})
		if err != nil {
			return err
		}
		a.Activity(orgID, userID, "task.answered", "task", t.ID, map[string]any{"requirement": req.Code, "newStatus": res.Answer.Status})
		if t.RequestedBy != "" {
			a.Notify(orgID, t.RequestedBy, "SME answered "+req.Code, "New status: "+res.Answer.Status, "/bids/"+req.BidID+"/requirements/"+req.ID)
		}
		if req.ReviewerID != "" && res.Answer.Status == store.StatusInReview {
			a.Notify(orgID, req.ReviewerID, "Answer ready for review: "+req.Code, "", "/bids/"+req.BidID+"/requirements/"+req.ID)
		}
	case "request_more_info":
		if strings.TrimSpace(response) == "" {
			return userErr("Say what information you need.")
		}
		t.Comment, t.Status, t.UpdatedAt = strings.TrimSpace(response), "waiting", now
		if err := a.DB.UpdateTask(t); err != nil {
			return err
		}
		a.Activity(orgID, userID, "task.more_info", "task", t.ID, nil)
		if t.RequestedBy != "" {
			a.Notify(orgID, t.RequestedBy, "SME needs more information", t.Comment, "/tasks/"+t.ID)
		}
	case "reject":
		t.Comment, t.Status, t.UpdatedAt = strings.TrimSpace(response), "rejected", now
		if err := a.DB.UpdateTask(t); err != nil {
			return err
		}
		a.Activity(orgID, userID, "task.rejected", "task", t.ID, nil)
	case "comment":
		t.Comment, t.UpdatedAt = strings.TrimSpace(response), now
		if err := a.DB.UpdateTask(t); err != nil {
			return err
		}
	case "start":
		t.Status, t.UpdatedAt = "in_progress", now
		if err := a.DB.UpdateTask(t); err != nil {
			return err
		}
	default:
		return userErr("Unknown task action.")
	}
	return nil
}

func (a *App) taskInOrg(orgID, taskID string) (store.Task, error) {
	t, err := a.DB.GetTask(taskID)
	if err != nil {
		return t, err
	}
	if _, err := a.DB.GetBid(orgID, t.BidID); err != nil {
		return t, store.ErrNotFound
	}
	return t, nil
}

// TaskView joins a task with its requirement, bid and people.
type TaskView struct {
	store.Task
	Requirement store.Requirement
	Bid         store.Bid
	Assignee    store.User
	Requester   store.User
	Overdue     bool
	Draft       string
}

func (a *App) TaskViews(orgID string, tasks []store.Task) []TaskView {
	users, _ := a.DB.UserMap(orgID)
	now := a.Now()
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		v := TaskView{Task: t, Assignee: users[t.AssigneeID], Requester: users[t.RequestedBy]}
		v.Requirement, _ = a.DB.GetRequirement(t.RequirementID)
		v.Bid, _ = a.DB.GetBidByID(t.BidID)
		v.Overdue = t.DueAt != "" && t.DueAt < now && (t.Status == "open" || t.Status == "in_progress")
		if ans, err := a.DB.LatestAnswer(t.RequirementID); err == nil {
			v.Draft = ans.FinalText
			if v.Draft == "" {
				v.Draft = ans.DraftText
			}
		}
		out = append(out, v)
	}
	return out
}
