// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

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

func TestBuildQuickReplyPreservesAssociatedLabels(t *testing.T) {
	build := reflect.ValueOf(BuildQuickReply)
	if !build.Type().IsVariadic() {
		t.Fatal("BuildQuickReply does not accept associated label IDs")
	}
	result := build.CallSlice([]reflect.Value{
		reflect.ValueOf("1700000000"),
		reflect.ValueOf("hours"),
		reflect.ValueOf("We are open until 18:00."),
		reflect.ValueOf([]string{"open", "hours"}),
		reflect.ValueOf(int32(7)),
		reflect.ValueOf([]string{"label-1", "label-2"}),
	})[0].Interface().(PatchInfo)
	action := result.Mutations[0].Value.GetQuickReplyAction()
	if !reflect.DeepEqual(action.GetAssociatedLabelIDs(), []string{"label-1", "label-2"}) {
		t.Fatalf("associated labels = %#v", action.GetAssociatedLabelIDs())
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
