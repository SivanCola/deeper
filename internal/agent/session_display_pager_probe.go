package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"deepzora/internal/historywork"
)

// Native writers put identifying fields first. Legacy writers may reorder JSON
// fields, so a bounded-prefix miss falls back to a cancellable token walk, not
// a Decode of the entire event (which may contain all session messages).
func probeDisplayEventHeader(ctx context.Context, file *os.File) (int, string, bool, error) {
	schema, kind, known := probeSessionEventHeader(&historywork.Reader{Context: ctx, Source: file})
	if known || ctx.Err() != nil {
		return schema, kind, known, ctx.Err()
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", false, err
	}
	decoder := json.NewDecoder(&historywork.Reader{Context: ctx, Source: file})
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return 0, "", false, ctx.Err()
	}
	haveSchema, haveType := false, false
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return 0, "", false, ctx.Err()
		}
		switch name {
		case "schema_version":
			err = decoder.Decode(&schema)
			haveSchema = err == nil
		case "type":
			err = decoder.Decode(&kind)
			haveType = err == nil
		default:
			err = skipDisplayJSONValue(ctx, decoder)
		}
		if err != nil {
			return 0, "", false, ctx.Err()
		}
		if haveSchema && haveType {
			return schema, kind, true, nil
		}
	}
	return 0, "", false, ctx.Err()
}

// Unknown event fields are skipped token by token rather than buffering the
// entire field/record in json.RawMessage. json.Decoder still validates nesting.
func skipDisplayJSONValue(ctx context.Context, decoder *json.Decoder) error {
	depth := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth <= 0 {
			return nil
		}
	}
}
