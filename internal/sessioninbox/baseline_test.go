package sessioninbox

import (
	"bytes"
	"deepzora/internal/store"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOldInboxSchemaRejectedWithoutRewrite(t *testing.T) {
	for _, schema := range []int{0, 1, 2} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		dir := store.SessionInboxDir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(manifest{SchemaVersion: schema, Items: []InboxItemMeta{}})
		file := filepath.Join(dir, manifestName)
		if err := os.WriteFile(file, body, 0600); err != nil {
			t.Fatal(err)
		}
		if st, err := Open(path, Limits{}); !errors.Is(err, ErrUnsupportedSchema) {
			if st != nil {
				st.Close()
			}
			t.Fatalf("schema %d: %v", schema, err)
		}
		got, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(body, got) {
			t.Fatalf("schema %d rewritten", schema)
		}
	}
}
