package agent

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveVolumePathRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	cases := []string{
		"../../etc/passwd",
		"../sibling-volume/secret",
		"a/../../b",
	}
	for _, rel := range cases {
		if _, ok := resolveVolumePath(base, "default", "data", rel); ok {
			t.Errorf("resolveVolumePath allowed escaping path %q", rel)
		}
	}
}

func TestResolveVolumePathAllowsNestedPaths(t *testing.T) {
	base := t.TempDir()
	full, ok := resolveVolumePath(base, "default", "data", "css/style.css")
	if !ok {
		t.Fatal("resolveVolumePath rejected a legitimate nested path")
	}
	want := filepath.Join(base, "default", "data", "css", "style.css")
	if full != want {
		t.Errorf("resolveVolumePath = %q, want %q", full, want)
	}
}

func TestResolveVolumePathRejectsMissingIdentity(t *testing.T) {
	base := t.TempDir()
	if _, ok := resolveVolumePath(base, "", "data", "index.html"); ok {
		t.Error("resolveVolumePath allowed an empty namespace")
	}
	if _, ok := resolveVolumePath(base, "default", "", "index.html"); ok {
		t.Error("resolveVolumePath allowed an empty name")
	}
}

func TestFilesHandlerWriteReadDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	handler := NewFilesHandler(dir)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	writeReq, _ := http.NewRequest(http.MethodPut, srv.URL+"/files/write?namespace=default&name=data&path=index.html", bytes.NewBufferString("hello volume"))
	resp, err := http.DefaultClient.Do(writeReq)
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write status = %d, want 204", resp.StatusCode)
	}

	readResp, err := http.Get(srv.URL + "/files/read?namespace=default&name=data&path=index.html")
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	defer readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("read status = %d, want 200", readResp.StatusCode)
	}
	buf := make([]byte, 64)
	n, _ := readResp.Body.Read(buf)
	if string(buf[:n]) != "hello volume" {
		t.Errorf("read content = %q, want %q", string(buf[:n]), "hello volume")
	}

	listResp, err := http.Get(srv.URL + "/files/list?namespace=default&name=data")
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/files/delete?namespace=default&name=data&path=index.html", nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	if _, err := os.Stat(filepath.Join(dir, "default", "data", "index.html")); !os.IsNotExist(err) {
		t.Error("file still exists on disk after delete")
	}
}

func TestFilesHandlerReadRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	// Write a file outside the volumes dir entirely, to make sure it can't be read through it.
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("top secret"), 0o644); err != nil {
		t.Fatalf("seed secret file: %v", err)
	}

	handler := NewFilesHandler(dir)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rel := filepath.ToSlash(filepath.Join("..", "..", filepath.Base(secretDir), "secret.txt"))
	resp, err := http.Get(srv.URL + "/files/read?namespace=default&name=data&path=" + rel)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatal("read succeeded on a path-traversal attempt, want rejection")
	}
}
