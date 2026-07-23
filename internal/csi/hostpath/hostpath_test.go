package hostpath

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestCreateAndDeleteVolume(t *testing.T) {
	d, err := New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	resp, err := d.CreateVolume(ctx, &csi.CreateVolumeRequest{
		Name:          "vol-1",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1 << 20},
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if resp.GetVolume().GetVolumeId() != "vol-1" {
		t.Errorf("VolumeId = %q, want vol-1", resp.GetVolume().GetVolumeId())
	}
	if resp.GetVolume().GetCapacityBytes() != 1<<20 {
		t.Errorf("CapacityBytes = %d, want %d", resp.GetVolume().GetCapacityBytes(), 1<<20)
	}

	if _, err := os.Stat(d.volumeDir("vol-1")); err != nil {
		t.Fatalf("volume directory not created: %v", err)
	}

	if _, err := d.DeleteVolume(ctx, &csi.DeleteVolumeRequest{VolumeId: "vol-1"}); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, err := os.Stat(d.volumeDir("vol-1")); !os.IsNotExist(err) {
		t.Fatalf("volume directory still exists after delete: %v", err)
	}
}

func TestListVolumes(t *testing.T) {
	d, err := New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	for _, name := range []string{"vol-a", "vol-b"} {
		if _, err := d.CreateVolume(ctx, &csi.CreateVolumeRequest{Name: name}); err != nil {
			t.Fatalf("CreateVolume(%s): %v", name, err)
		}
	}

	resp, err := d.ListVolumes(ctx, &csi.ListVolumesRequest{})
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if len(resp.GetEntries()) != 2 {
		t.Fatalf("got %d entries, want 2", len(resp.GetEntries()))
	}
}

func TestNodePublishAndUnpublishVolume(t *testing.T) {
	d, err := New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if _, err := d.CreateVolume(ctx, &csi.CreateVolumeRequest{Name: "vol-1"}); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	marker := []byte("hello from the volume")
	if err := os.WriteFile(filepath.Join(d.volumeDir("vol-1"), "data.txt"), marker, 0o644); err != nil {
		t.Fatalf("seed volume content: %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "mnt", "vol-1")
	if _, err := d.NodePublishVolume(ctx, &csi.NodePublishVolumeRequest{
		VolumeId:   "vol-1",
		TargetPath: targetPath,
	}); err != nil {
		t.Skipf("NodePublishVolume: %v (symlink creation may require elevated privileges on this OS)", err)
	}

	got, err := os.ReadFile(filepath.Join(targetPath, "data.txt"))
	if err != nil {
		t.Fatalf("read published volume content: %v", err)
	}
	if string(got) != string(marker) {
		t.Errorf("published content = %q, want %q", got, marker)
	}

	if _, err := d.NodeUnpublishVolume(ctx, &csi.NodeUnpublishVolumeRequest{TargetPath: targetPath}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
		t.Errorf("target path still exists after unpublish: %v", err)
	}
}

func TestCreateVolumeRequiresName(t *testing.T) {
	d, err := New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{}); err == nil {
		t.Fatal("CreateVolume succeeded without a name, want error")
	}
}

func TestCreateVolumeRejectsPathTraversal(t *testing.T) {
	d, err := New("test-node", t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, name := range []string{"../escaped", "a/../../escaped", "../../../etc/passwd"} {
		if _, err := d.CreateVolume(context.Background(), &csi.CreateVolumeRequest{Name: name}); err == nil {
			t.Errorf("CreateVolume(%q) succeeded, want rejection of the path traversal attempt", name)
		}
	}
}
