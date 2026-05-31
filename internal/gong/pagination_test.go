package gong

import "testing"

func TestPageRecordsFromBody(t *testing.T) {
	records, err := PageRecordsFromBody([]byte(`{
		"records": {
			"totalRecords": 8366,
			"currentPageSize": 100,
			"currentPageNumber": 0,
			"cursor": "next-page-token"
		},
		"calls": []
	}`))
	if err != nil {
		t.Fatalf("PageRecordsFromBody returned error: %v", err)
	}
	if records.TotalRecords != 8366 {
		t.Fatalf("TotalRecords = %d, want 8366", records.TotalRecords)
	}
	if records.CurrentPageSize != 100 {
		t.Fatalf("CurrentPageSize = %d, want 100", records.CurrentPageSize)
	}
	if records.CurrentPageNumber != 0 {
		t.Fatalf("CurrentPageNumber = %d, want 0", records.CurrentPageNumber)
	}
	if records.Cursor != "next-page-token" {
		t.Fatalf("Cursor = %q, want next-page-token", records.Cursor)
	}
}

func TestPageRecordsFromBodyWithoutCursor(t *testing.T) {
	records, err := PageRecordsFromBody([]byte(`{
		"records": {
			"totalRecords": 88,
			"currentPageSize": 88,
			"currentPageNumber": 0
		},
		"users": []
	}`))
	if err != nil {
		t.Fatalf("PageRecordsFromBody returned error: %v", err)
	}
	if records.Cursor != "" {
		t.Fatalf("Cursor = %q, want empty", records.Cursor)
	}
}
