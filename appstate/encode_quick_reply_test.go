package appstate

import (
	"reflect"
	"testing"
)

func TestBuildQuickReply(t *testing.T) {
	patch := BuildQuickReply("1700000000", "hours", "We are open until 18:00.", []string{"open", "hours"}, 7)
	if patch.Type != WAPatchRegular || len(patch.Mutations) != 1 {
		t.Fatalf("patch = %#v", patch)
	}
	mutation := patch.Mutations[0]
	if mutation.Version != 2 || !reflect.DeepEqual(mutation.Index, []string{IndexQuickReply, "1700000000"}) {
		t.Fatalf("mutation = %#v", mutation)
	}
	action := mutation.Value.GetQuickReplyAction()
	if action.GetDeleted() || action.GetShortcut() != "hours" || action.GetMessage() != "We are open until 18:00." || action.GetCount() != 7 {
		t.Fatalf("action = %#v", action)
	}
	if !reflect.DeepEqual(action.GetKeywords(), []string{"open", "hours"}) || len(action.GetAssociatedLabelIDs()) != 0 {
		t.Fatalf("action lists = %#v", action)
	}
}

func TestBuildQuickReplyDeleteUsesTombstone(t *testing.T) {
	patch := BuildQuickReplyDelete("1700000000")
	if patch.Type != WAPatchRegular || len(patch.Mutations) != 1 {
		t.Fatalf("patch = %#v", patch)
	}
	mutation := patch.Mutations[0]
	if mutation.Version != 2 || !reflect.DeepEqual(mutation.Index, []string{IndexQuickReply, "1700000000"}) {
		t.Fatalf("mutation = %#v", mutation)
	}
	action := mutation.Value.GetQuickReplyAction()
	if !action.GetDeleted() || action.GetShortcut() != "" || action.GetMessage() != "" || action.GetCount() != 0 {
		t.Fatalf("action = %#v", action)
	}
	if len(action.GetKeywords()) != 0 || len(action.GetAssociatedLabelIDs()) != 0 {
		t.Fatalf("action lists = %#v", action)
	}
}
