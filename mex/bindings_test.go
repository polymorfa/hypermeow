package mex

import "testing"

func TestCatalogBindingsMatchPinnedSpec(t *testing.T) {
	if SourceRevision != "74509efe262b37b26bed05486c9f4160db5e841b" {
		t.Fatalf("source revision = %q", SourceRevision)
	}
	tests := map[OperationName]string{
		DeleteNewsletter:              "30062808666639665",
		QueryCatalog:                 "30445081048424116",
		QueryCatalogProduct:          "9660926520672123",
		QueryProductCollections:      "9430970660362540",
		QueryProductListCatalog:      "30125049463760630",
		QueryProductSingleCollection: "9546992575408789",
		BizCreateOrder:               "26486627094287046",
		BizQueryOrder:                "26593811266898374",
	}
	for name, wantID := range tests {
		binding, ok := Lookup(name)
		if !ok || binding.DocumentID != wantID {
			t.Errorf("%s = %#v, %t; want document ID %s", name, binding, ok, wantID)
		}
	}
}

func TestLookupRejectsUnknownOperation(t *testing.T) {
	if _, ok := Lookup(OperationName("unknown")); ok {
		t.Fatal("unknown operation unexpectedly resolved")
	}
}
