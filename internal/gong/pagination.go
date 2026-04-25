package gong

import "encoding/json"

type PageRecords struct {
	TotalRecords      int    `json:"totalRecords"`
	CurrentPageSize   int    `json:"currentPageSize"`
	CurrentPageNumber int    `json:"currentPageNumber"`
	Cursor            string `json:"cursor"`
}

func PageRecordsFromBody(body []byte) (PageRecords, error) {
	var payload struct {
		Records PageRecords `json:"records"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PageRecords{}, err
	}
	return payload.Records, nil
}
