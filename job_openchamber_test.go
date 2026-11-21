package main

import (
	"net/http/httptest"
	"testing"
)

func TestSetOpenChamberDirectoryHeaderUsesEncodedPath(t *testing.T) {
	request := httptest.NewRequest("GET", "http://127.0.0.1/api/session/test", nil)
	setOpenChamberDirectoryHeader(request, `C:\Users\Example User\Documents\Projects\Nala Ctrl Usage Monitor`)

	const want = `C%3A%5CUsers%5CExample%20User%5CDocuments%5CProjects%5CNala%20Ctrl%20Usage%20Monitor`
	if got := request.Header.Get(openChamberDirectoryHeader); got != want {
		t.Fatalf("directory header = %q, want %q", got, want)
	}
}
