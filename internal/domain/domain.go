// Package domain holds AWO's core, dependency-free domain types: enums for
// agent kinds, run modes, run statuses, agent roles, and recommendations,
// plus the run-shape structs (RunSpec, AgentRunResult, VerificationResult,
// RunReport) that flow between orchestrator, artifacts, and reports.
package domain

import (
	"fmt"
	"time"
)

// AgentKind identifies a supported coding-agent backend.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

// AllAgentKinds returns the set of valid AgentKind values.
func AllAgentKinds() []AgentKind { return []AgentKind{AgentClaude, AgentCodex} }

// Validate returns an error if k is not a known agent kind.
func (k AgentKind) Validate() error {
	switch k {
	case AgentClaude, AgentCodex:
		return nil
	}
	return fmt.Errorf("invalid AgentKind %q", string(k))
}

// RunMode is the orchestration strategy for a run.
type RunMode string

const (
	ModeSingle         RunMode = "single"
	ModeWriterReviewer RunMode = "writer-reviewer"
	ModeCompetitive    RunMode = "competitive"
)

func (m RunMode) Validate() error {
	switch m {
	case ModeSingle, ModeWriterReviewer, ModeCompetitive:
		return nil
	}
	return fmt.Errorf("invalid RunMode %q", string(m))
}

// RunStatus is the lifecycle state of a run.
type RunStatus string

const (
	StatusCreated   RunStatus = "created"
	StatusPreparing RunStatus = "preparing"
	StatusRunning   RunStatus = "running"
	StatusVerifying RunStatus = "verifying"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
	StatusCancelled RunStatus = "cancelled"
)

func (s RunStatus) Validate() error {
	switch s {
	case StatusCreated, StatusPreparing, StatusRunning, StatusVerifying,
		StatusCompleted, StatusFailed, StatusCancelled:
		return nil
	}
	return fmt.Errorf("invalid RunStatus %q", string(s))
}

// AgentRole is the role an agent plays inside a run.
type AgentRole string

const (
	RoleWriter     AgentRole = "writer"
	RoleReviewer   AgentRole = "reviewer"
	RoleCompetitor AgentRole = "competitor"
)

func (r AgentRole) Validate() error {
	switch r {
	case RoleWriter, RoleReviewer, RoleCompetitor:
		return nil
	}
	return fmt.Errorf("invalid AgentRole %q", string(r))
}

// Recommendation is the orchestrator's summary verdict for a run.
type Recommendation string

const (
	RecReadyForHumanReview   Recommendation = "ready_for_human_review"
	RecNeedsRevision         Recommendation = "needs_revision"
	RecFailedVerification    Recommendation = "failed_verification"
	RecNeedsHumanAttention   Recommendation = "needs_human_attention"
	RecTooLargeForAutoReview Recommendation = "too_large_for_auto_review"
	RecNoRecommendation      Recommendation = "no_recommendation"
)

func (r Recommendation) Validate() error {
	switch r {
	case RecReadyForHumanReview, RecNeedsRevision, RecFailedVerification,
		RecNeedsHumanAttention, RecTooLargeForAutoReview, RecNoRecommendation, "":
		return nil
	}
	return fmt.Errorf("invalid Recommendation %q", string(r))
}

// ParsedAgentResult captures structured information distilled from an
// agent's raw stdout. Fields are best-effort; consumers must treat them as
// untrusted advisory data.
type ParsedAgentResult struct {
	Summary             string   `json:"summary,omitempty"`
	FilesTouched        []string `json:"filesTouched,omitempty"`
	SelfReportedSuccess *bool    `json:"selfReportedSuccess,omitempty"`
	Notes               []string `json:"notes,omitempty"`
}

// ReviewFindings is the orchestrator-level view of a reviewer agent's
// AWO_REVIEW_JSON block. Like ParsedAgentResult, it is advisory and must
// never override deterministic verification.
type ReviewFindings struct {
	Blocking       []string `json:"blocking,omitempty"`
	NonBlocking    []string `json:"nonBlocking,omitempty"`
	SuggestedTests []string `json:"suggestedTests,omitempty"`
	RiskSummary    string   `json:"riskSummary,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

// RunSpec is the user-facing description of a run before it starts.
type RunSpec struct {
	Task            string      `json:"task"`
	Mode            RunMode     `json:"mode"`
	Primary         *AgentKind  `json:"primary,omitempty"`
	Agent           *AgentKind  `json:"agent,omitempty"`
	Reviewer        *AgentKind  `json:"reviewer,omitempty"`
	Reviewers       []AgentKind `json:"reviewers,omitempty"`
	Competitors     []AgentKind `json:"competitors,omitempty"`
	VerifyCommands  []string    `json:"verifyCommands,omitempty"`
	BaseBranch      string      `json:"baseBranch,omitempty"`
	KeepWorktrees   bool        `json:"keepWorktrees,omitempty"`
	DryRun          bool        `json:"dryRun,omitempty"`
	MaxChangedFiles int         `json:"maxChangedFiles,omitempty"`
}

// AgentRunResult records what one agent did during a run.
type AgentRunResult struct {
	Agent        AgentKind          `json:"agent"`
	Role         AgentRole          `json:"role"`
	WorktreePath string             `json:"worktreePath"`
	BranchName   string             `json:"branchName"`
	Status       string             `json:"status"`
	StartedAt    time.Time          `json:"startedAt"`
	FinishedAt   time.Time          `json:"finishedAt"`
	ExitCode     int                `json:"exitCode"`
	StdoutPath   string             `json:"stdoutPath"`
	StderrPath   string             `json:"stderrPath"`
	SummaryPath  string             `json:"summaryPath,omitempty"`
	DiffPath     string             `json:"diffPath,omitempty"`
	ChangedFiles []string           `json:"changedFiles,omitempty"`
	ParsedResult *ParsedAgentResult `json:"parsedResult,omitempty"`
	Review       *ReviewFindings    `json:"review,omitempty"`
	Warnings     []string           `json:"warnings,omitempty"`
}

// VerificationResult records the outcome of one deterministic verification
// command. The exit code is the only trusted signal.
type VerificationResult struct {
	Command        string    `json:"command"`
	ExitCode       int       `json:"exitCode"`
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	DurationMillis int64     `json:"durationMillis"`
	StdoutPath     string    `json:"stdoutPath"`
	StderrPath     string    `json:"stderrPath"`
	Passed         bool      `json:"passed"`
}

// RunReport is the canonical artifact written for every run.
type RunReport struct {
	RunID               string               `json:"runId"`
	Spec                RunSpec              `json:"spec"`
	Status              RunStatus            `json:"status"`
	StartedAt           time.Time            `json:"startedAt"`
	FinishedAt          time.Time            `json:"finishedAt,omitempty"`
	AgentResults        []AgentRunResult     `json:"agentResults,omitempty"`
	VerificationResults []VerificationResult `json:"verificationResults,omitempty"`
	Recommendation      Recommendation       `json:"recommendation,omitempty"`
	ProofPackPath       string               `json:"proofPackPath,omitempty"`
	Warnings            []string             `json:"warnings,omitempty"`
}
