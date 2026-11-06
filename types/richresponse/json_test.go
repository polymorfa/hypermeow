// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package richresponse

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestPrimitiveRoundTripPreservesRegisteredTypes(t *testing.T) {
	for name, typ := range primitiveTypes {
		t.Run(name, func(t *testing.T) {
			var pc PrimitiveContainer
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"__typename":%q}`, name)), &pc); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(pc)
			if err != nil {
				t.Fatal(err)
			}
			var next PrimitiveContainer
			if err = json.Unmarshal(data, &next); err != nil {
				t.Fatal(err)
			}
			if reflect.TypeOf(next.Value) != reflect.PointerTo(typ) {
				t.Fatalf("round trip changed %s to %T: %s", name, next.Value, data)
			}
		})
	}
}

func TestRichResponseRoundTripPreservesPrimitiveAndUnknownObjects(t *testing.T) {
	input := []byte(`{"response_id":"test","sections":[{"view_model":{"__typename":"GenAISingleLayoutViewModel","primitive":{"__typename":"GenAIMarkdownTextUXPrimitive","text":"hello","inline_entities":[{"key":"x","metadata":{"__typename":"FutureMetadata","value":42}}]}}},{"view_model":{"__typename":"GenAISingleLayoutViewModel","primitive":{"__typename":"FuturePrimitive","value":[1,2]}}},{"view_model":{"__typename":"FutureViewModel","value":{"nested":true}}}]}`)
	var response RichResponse
	if err := json.Unmarshal(input, &response); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err = json.Unmarshal(input, &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(output, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("JSON changed during round trip:\n%s", output)
	}
	var next RichResponse
	if err = json.Unmarshal(output, &next); err != nil {
		t.Fatal(err)
	}
	if got := next.Sections[0].Model.String(); got != "hello" {
		t.Fatalf("decoded text = %q", got)
	}
}

func TestRichResponseRejectsMalformedKnownValues(t *testing.T) {
	for _, input := range []string{
		`{"sections":[{"view_model":{"__typename":"GenAISingleLayoutViewModel","primitive":{"__typename":"FOATextPrimitive","text":{}}}}]}`,
		`{"sections":[{"view_model":{"__typename":"GenAIGridLayoutViewModel","primitives":{}}}]}`,
	} {
		var response RichResponse
		if err := json.Unmarshal([]byte(input), &response); err == nil {
			t.Fatalf("accepted malformed response: %s", input)
		}
	}
}
