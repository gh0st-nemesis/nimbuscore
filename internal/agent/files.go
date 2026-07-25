package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxFileWriteBytes = 8 << 20

type fileEntry struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"isDir"`
	Size        int64  `json:"size"`
	ModTimeUnix int64  `json:"modTimeUnix"`
}

// resolveVolumePath maps a namespace/name volume identity plus a relative
// path within it to an absolute path, rejecting anything that would escape
// the volume's own directory.
func resolveVolumePath(volumesDir, namespace, name, relPath string) (string, bool) {
	if namespace == "" || name == "" {
		return "", false
	}
	volDir := filepath.Join(volumesDir, namespace, name)
	full := filepath.Join(volDir, relPath)

	cleanVolDir := filepath.Clean(volDir)
	cleanFull := filepath.Clean(full)
	rel, err := filepath.Rel(cleanVolDir, cleanFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return cleanFull, true
}

func NewFilesHandler(volumesDir string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/files/list", func(w http.ResponseWriter, r *http.Request) {
		namespace := r.URL.Query().Get("namespace")
		name := r.URL.Query().Get("name")
		volDir, ok := resolveVolumePath(volumesDir, namespace, name, ".")
		if !ok {
			http.Error(w, "invalid namespace/name", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(volDir, 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		entries, err := os.ReadDir(volDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]fileEntry, 0, len(entries))
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fileEntry{
				Name:        e.Name(),
				IsDir:       e.IsDir(),
				Size:        info.Size(),
				ModTimeUnix: info.ModTime().Unix(),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out) //nolint:errcheck
	})

	mux.HandleFunc("/files/read", func(w http.ResponseWriter, r *http.Request) {
		namespace := r.URL.Query().Get("namespace")
		name := r.URL.Query().Get("name")
		path := r.URL.Query().Get("path")
		full, ok := resolveVolumePath(volumesDir, namespace, name, path)
		if !ok || path == "" {
			http.Error(w, "invalid namespace/name/path", http.StatusBadRequest)
			return
		}
		b, err := os.ReadFile(full)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "file not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b) //nolint:errcheck
	})

	mux.HandleFunc("/files/write", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		namespace := r.URL.Query().Get("namespace")
		name := r.URL.Query().Get("name")
		path := r.URL.Query().Get("path")
		full, ok := resolveVolumePath(volumesDir, namespace, name, path)
		if !ok || path == "" {
			http.Error(w, "invalid namespace/name/path", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxFileWriteBytes+1))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(body) > maxFileWriteBytes {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/files/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		namespace := r.URL.Query().Get("namespace")
		name := r.URL.Query().Get("name")
		path := r.URL.Query().Get("path")
		full, ok := resolveVolumePath(volumesDir, namespace, name, path)
		if !ok || path == "" {
			http.Error(w, "invalid namespace/name/path", http.StatusBadRequest)
			return
		}
		info, err := os.Stat(full)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "file not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if info.IsDir() {
			http.Error(w, "cannot delete a directory", http.StatusBadRequest)
			return
		}
		if err := os.Remove(full); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	return mux
}

func StartFilesServer(addr, volumesDir string) error {
	srv := &http.Server{Addr: addr, Handler: NewFilesHandler(volumesDir)}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
