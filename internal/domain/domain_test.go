package domain

import "testing"

func TestAgentKindValidate(t *testing.T) {
	for _, k := range AllAgentKinds() {
		if err := k.Validate(); err != nil {
			t.Errorf("expected %q to be valid: %v", k, err)
		}
	}
	if err := AgentKind("gpt-99").Validate(); err == nil {
		t.Error("expected unknown kind to be rejected")
	}
}

func TestRunModeValidate(t *testing.T) {
	for _, m := range []RunMode{ModeSingle, ModeWriterReviewer, ModeCompetitive} {
		if err := m.Validate(); err != nil {
			t.Errorf("%q: %v", m, err)
		}
	}
	if err := RunMode("yolo").Validate(); err == nil {
		t.Error("expected unknown mode to be rejected")
	}
}

func TestRunStatusValidate(t *testing.T) {
	all := []RunStatus{
		StatusCreated, StatusPreparing, StatusRunning, StatusVerifying,
		StatusCompleted, StatusFailed, StatusCancelled,
	}
	for _, s := range all {
		if err := s.Validate(); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if err := RunStatus("zombie").Validate(); err == nil {
		t.Error("expected unknown status to be rejected")
	}
}

func TestAgentRoleValidate(t *testing.T) {
	for _, r := range []AgentRole{RoleWriter, RoleReviewer, RoleCompetitor} {
		if err := r.Validate(); err != nil {
			t.Errorf("%q: %v", r, err)
		}
	}
	if err := AgentRole("manager").Validate(); err == nil {
		t.Error("expected unknown role to be rejected")
	}
}

func TestRecommendationValidate(t *testing.T) {
	all := []Recommendation{
		RecReadyForHumanReview, RecNeedsRevision, RecFailedVerification,
		RecNeedsHumanAttention, RecTooLargeForAutoReview, RecNoRecommendation, "",
	}
	for _, r := range all {
		if err := r.Validate(); err != nil {
			t.Errorf("%q: %v", r, err)
		}
	}
	if err := Recommendation("ship_it").Validate(); err == nil {
		t.Error("expected unknown recommendation to be rejected")
	}
}
