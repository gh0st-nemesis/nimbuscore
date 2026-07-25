package agent

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const maxLogReadBytes = 1 << 20

func NewLogHandler(logDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		namespace := r.URL.Query().Get("namespace")
		name := r.URL.Query().Get("name")
		if namespace == "" || name == "" {
			http.Error(w, "namespace and name query parameters are required", http.StatusBadRequest)
			return
		}

		tail := 200
		if v := r.URL.Query().Get("tail"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				tail = n
			}
		}

		id := containerNameRaw(namespace, name)
		path := logFilePath(logDir, id)

		lines, err := tailFile(path, tail)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "no logs found for this pod yet", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(lines) //nolint:errcheck
	})
	return mux
}

func tailFile(path string, maxLines int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := info.Size()
	readSize := int64(maxLogReadBytes)
	if size < readSize {
		readSize = size
	}
	if _, err := f.Seek(-readSize, io.SeekEnd); err != nil {
		return nil, err
	}

	buf := make([]byte, readSize)
	if _, err := io.ReadFull(f, buf); err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func StartLogServer(addr, logDir string) error {
	srv := &http.Server{Addr: addr, Handler: NewLogHandler(logDir)}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("agent: log server: %w", err)
	}
	return nil
}
