package main

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestOpenChamberUsageProbePermissionsKeepReadToolWithoutProjectAccess(t *testing.T) {
	permissions, readResource, err := openChamberUsageProbePermissions()
	if err != nil {
		t.Fatalf("create probe permissions: %v", err)
	}
	if len(permissions) != 2 {
		t.Fatalf("permission count = %d, want 2", len(permissions))
	}
	if !hasOpenChamberUsageProbePermissions(permissions, readResource) {
		t.Fatal("probe permissions do not keep read advertised while denying other paths")
	}
	if permissions[0] != (openChamberPermissionRule{Action: "*", Resource: "*", Effect: "deny"}) {
		t.Fatalf("blanket permission = %#v, want deny all", permissions[0])
	}
	if permissions[1] != (openChamberPermissionRule{Action: "read", Resource: readResource, Effect: "allow"}) {
		t.Fatalf("read permission = %#v, want the isolated allow path", permissions[1])
	}
	if !strings.HasPrefix(readResource, filepath.Join(os.TempDir(), "nala-ctrl-openchamber-no-read-")) {
		t.Fatalf("read resource = %q, want a unique temp path", readResource)
	}
	if _, err := os.Lstat(filepath.Dir(readResource)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read allow directory exists or cannot be checked: %v", err)
	}
}
