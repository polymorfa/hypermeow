package appstate

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestBuildLabelChatChangesUsesOnePatch(t *testing.T) {
	target := types.NewJID("15551234567", types.DefaultUserServer)
	patch := BuildLabelChatChanges(target, []LabelChatChange{
		{LabelID: "1", Labeled: false},
		{LabelID: "2", Labeled: true},
	})
	if patch.Type != WAPatchRegular || len(patch.Mutations) != 2 {
		t.Fatalf("patch = %#v", patch)
	}
	for index, want := range []struct {
		labelID string
		labeled bool
	}{{"1", false}, {"2", true}} {
		mutation := patch.Mutations[index]
		if mutation.Version != 3 || len(mutation.Index) != 3 || mutation.Index[1] != want.labelID || mutation.Index[2] != target.String() {
			t.Fatalf("mutation %d = %#v", index, mutation)
		}
		if mutation.Value.GetLabelAssociationAction().GetLabeled() != want.labeled {
			t.Fatalf("mutation %d labeled = %v", index, mutation.Value.GetLabelAssociationAction().GetLabeled())
		}
	}
}

func TestBuildLabelChatChangesDeduplicatesLabelIDs(t *testing.T) {
	target := types.NewJID("15551234567", types.DefaultUserServer)
	patch := BuildLabelChatChanges(target, []LabelChatChange{
		{LabelID: "1", Labeled: false},
		{LabelID: "2", Labeled: true},
		{LabelID: "1", Labeled: true},
	})
	if len(patch.Mutations) != 2 {
		t.Fatalf("mutation count = %d, want 2", len(patch.Mutations))
	}
	if patch.Mutations[0].Index[1] != "1" || !patch.Mutations[0].Value.GetLabelAssociationAction().GetLabeled() {
		t.Fatalf("duplicate label did not preserve its final state: %#v", patch.Mutations[0])
	}
}
