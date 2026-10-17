package main

import (
	"bytes"
	"encoding/json"
)

// Reject retired selectors rather than silently treating them as focus-only.
func (o *RemoteTabOpenOptions) UnmarshalJSON(data []byte) error {
	type options RemoteTabOpenOptions
	var current options
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&current); err != nil {
		return err
	}
	*o = RemoteTabOpenOptions(current)
	return nil
}
