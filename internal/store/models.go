package store

// Roles.
const (
	RoleAdmin           = "admin"
	RoleProposalManager = "proposal_manager"
	RoleWriter          = "writer"
	RoleSME             = "sme"
	RoleReviewer        = "reviewer"
	RoleViewer          = "viewer"
)

// Bid statuses.
const (
	BidIntake        = "intake"
	BidQualification = "qualification"
	BidDrafting      = "drafting"
	BidReview        = "review"
	BidFinalQA       = "final_qa"
	BidReady         = "ready"
	BidClosedWon     = "closed_won"
	BidClosedLost    = "closed_lost"
	BidArchived      = "archived"
)

// Requirement / answer response statuses.
const (
	StatusNotStarted    = "not_started"
	StatusDrafted       = "drafted"
	StatusNeedsSME      = "needs_sme"
	StatusInReview      = "in_review"
	StatusApproved      = "approved"
	StatusNeedsEvidence = "needs_evidence"
	StatusRejected      = "rejected"
	StatusNeedsReReview = "needs_re_review"
	StatusRemoved       = "removed"
)

// Evidence labels (criteria implemented in ai.Label).
const (
	LabelStrong       = "Strong evidence"
	LabelModerate     = "Moderate evidence"
	LabelWeak         = "Weak evidence"
	LabelInsufficient = "Insufficient evidence"
)

// Job statuses.
const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobRetryable = "retryable"
	JobCancelled = "cancelled"
	JobWaiting   = "waiting_for_human"
)

type Organization struct {
	ID, Name, Mode, VerticalPackID, DataSensitivity, ExpiresAt, CreatedAt string
}

type User struct {
	ID, OrgID, Name, Email, Role, PasswordHash, CreatedAt string
}

type Session struct{ ID, UserID, OrgID, ExpiresAt string }

type Bid struct {
	ID, OrgID, Name, BuyerName, BuyerURL, OwnerID string
	EstimatedValue                                float64
	Deadline, Status, Description, Decision       string
	CreatedAt, UpdatedAt                          string
}

type Document struct {
	ID, OrgID, BidID, Name, MimeType, Category string
	Version                                    int
	ApprovalState, SourceType, FilePath, Hash  string
	ExpiresAt, ClientDisclosure, Trust         string
	Tags                                       []string
	Metadata                                   map[string]any
	ParseError, CreatedAt                      string
}

type Chunk struct {
	ID, DocumentID, SectionPath, Text string
	PageNumber                        int
	SheetName, CellRange              string
	ChunkIndex                        int
	TextHash                          string
	Embedding                         []float32
	EmbeddingModelID                  string
}

// ChunkInfo is a chunk joined with its document's retrieval-relevant fields.
type ChunkInfo struct {
	Chunk
	DocName, DocCategory, DocApproval, DocExpiresAt, DocClientDisclosure, DocTrust, DocBidID string
	DocVersion                                                                               int
}

type Requirement struct {
	ID, BidID, Code, Section, Text, Category string
	Mandatory                                bool
	SourceDocumentID, SourceLocator          string
	SourceSpan, AnchorStatus, ChangeState    string
	Status, OwnerID, ReviewerID              string
	SortOrder                                int
	Confirmed, IsTrap                        bool
	CreatedAt, UpdatedAt                     string
}

type Evidence struct {
	ID, RequirementID, ChunkID string
	Score                      float64
	Excerpt                    string
	Approved                   bool
	Rank                       int
}

type Answer struct {
	ID, RequirementID, DraftText, FinalText, GenerationMode, ConfidenceLabel, Status string
	Version                                                                          int
	ApprovedBy, ApprovedAt, LibraryEntryID                                           string
	UnsupportedCount                                                                 int
	VerifiedAt, Verification, SMEQuestion, EvidenceGaps, ProviderID, Model           string
	CreatedAt, UpdatedAt                                                             string
}

type Citation struct {
	ID, AnswerID string
	Marker       int
	ChunkID      string
}

// CitationInfo is a citation joined with its chunk and document.
type CitationInfo struct {
	Citation
	Text, SectionPath, SheetName, CellRange, DocName, DocID, DocExpiresAt, DocApproval string
	PageNumber, DocVersion                                                             int
}

type Task struct {
	ID, BidID, RequirementID, AssigneeID, RequestedBy, Type, Status, DueAt string
	Description, Question, Comment, Response, CreatedAt, UpdatedAt         string
}

type Review struct{ ID, AnswerID, ReviewerID, Status, Comment, CreatedAt string }

type QAResult struct {
	ID, BidID, CheckType, Severity, Message, EntityType, EntityID, Status, Resolution, Fingerprint, CreatedAt string
}

type ExportJob struct{ ID, BidID, Format, Status, FilePath, CreatedBy, CreatedAt string }

type LibraryEntry struct {
	ID, OrgID, CanonicalQuestion, AnswerText string
	CitationChunkIDs, Tags                   []string
	Category, OwnerID, ApprovedByID          string
	ApprovedAt, ReviewBy, SourceAnswerID     string
	SourceBidID                              string
	ReuseCount                               int
	Embedding                                []float32
	CreatedAt                                string
}

type Addendum struct{ ID, BidID, DocumentID, ReceivedAt, DiffSummary string }

type Clarification struct {
	ID, BidID, RequirementID, Question, SentAt, DueAt, BuyerResponse, Status, CreatedAt string
}

type PipelineRun struct {
	ID, BidID, Status, Mode, CurrentStage, Gate, GateOwner, GateDue string
	AutoGates                                                       bool
	OnlyRequirements                                                []string
	Error, StartedAt, FinishedAt, UpdatedAt                         string
}

type PipelineStep struct {
	ID, RunID, BidID, Stage, EntityType, EntityID, InputHash, Status string
	Attempts                                                         int
	OutputRef, Error, ProviderMode, Note, StartedAt, FinishedAt      string
}

type Job struct {
	ID, OrgID, Type, Payload, Status, RunAt, LeaseUntil string
	Attempts, MaxAttempts                               int
	IdempotencyKey                                      string
	Progress                                            int
	Log, Error, CreatedAt, UpdatedAt                    string
}

type ProviderCall struct {
	ID, OrgID, TaskKind, PromptID, ProviderID, Model, Mode string
	Cached                                                 bool
	Attempts, LatencyMs, InputTokens, OutputTokens         int
	Outcome, ErrorClass                                    string
	Fallback, Repaired                                     bool
	CreatedAt                                              string
}

type EvalRun struct {
	ID, Suite, ProviderMode, Metrics string
	Passed                           bool
	ReportPath, CreatedAt            string
}

type Activity struct {
	ID, OrgID, UserID, EventType, EntityType, EntityID, Metadata, CreatedAt string
}

type Notification struct {
	ID, OrgID, UserID, Title, Body, Link string
	Read                                 bool
	CreatedAt                            string
}

type ProposalSection struct {
	ID, BidID, Key, Title string
	SortOrder             int
	Content               string
	AnswerIDs             []string
	UpdatedAt             string
}

type Qualification struct {
	BidID     string
	Scores    map[string]float64
	Flags     []string
	Priority  string
	Summary   string
	UpdatedAt string
}

// ProviderStat is an aggregate row for the AI providers page.
type ProviderStat struct {
	ProviderID, Mode                                    string
	Calls, Cached, Fallbacks, Repaired, Errors, Latency int
}
