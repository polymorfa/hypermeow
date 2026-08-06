package sqlstore

import "testing"

func TestBuildSharedMassInsertQuery(t *testing.T) {
	query := buildSharedMassInsertQuery("INSERT VALUES ", " ON CONFLICT", 2, 2)
	want := "INSERT VALUES ($1,$2,$3),($1,$4,$5) ON CONFLICT"
	if query != want {
		t.Fatalf("unexpected query:\n%s\nwant:\n%s", query, want)
	}
}
