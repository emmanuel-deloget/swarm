package hook

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceRedactsCredentials(t *testing.T) {
	dir := t.TempDir()
	lg, err := OpenLog(filepath.Join(dir, "w.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })

	tr := &Trace{Header: http.Header{}, Outcome: "accepted"}
	tr.Header.Set("Authorization", "Bearer hunter2")
	tr.Header.Set("X-Swarm-Token", "hunter2")
	tr.Header.Set("X-Reqwire-Event", "push")
	lg.Write(tr)

	raw, _ := os.ReadFile(filepath.Join(dir, "w.log"))
	got := string(raw)
	if strings.Contains(got, "hunter2") {
		t.Errorf("a credential reached the log:\n%s", got)
	}
	if !strings.Contains(got, "X-Reqwire-Event: push") {
		t.Errorf("the event header should be kept:\n%s", got)
	}
}
