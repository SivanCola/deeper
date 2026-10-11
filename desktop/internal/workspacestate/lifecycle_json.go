package workspacestate

import "encoding/json"

func (v *PendingCreate) UnmarshalJSON(body []byte) error {
	type plain PendingCreate
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	extra, err := unknownFields(body, "operationId", "workspaceId", "sessionId", "createdAt", "archiveSource", "presentation", "parentSessionId")
	if err != nil {
		return err
	}
	*v = PendingCreate(decoded)
	v.extra = extra
	return nil
}
func (v PendingCreate) MarshalJSON() ([]byte, error) {
	type plain PendingCreate
	body, err := json.Marshal(plain(v))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(body, v.extra)
}

func (v *SessionState) UnmarshalJSON(body []byte) error {
	type plain SessionState
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	extra, err := unknownFields(body, "lifecycle", "generation", "archivedAt")
	if err != nil {
		return err
	}
	*v = SessionState(decoded)
	v.extra = extra
	return nil
}
func (v SessionState) MarshalJSON() ([]byte, error) {
	type plain SessionState
	body, err := json.Marshal(plain(v))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(body, v.extra)
}

func (v *Presentation) UnmarshalJSON(body []byte) error {
	type plain Presentation
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	extra, err := unknownFields(body, "topicId", "title", "pinned", "sortOrder")
	if err != nil {
		return err
	}
	*v = Presentation(decoded)
	v.extra = extra
	return nil
}
func (v Presentation) MarshalJSON() ([]byte, error) {
	type plain Presentation
	body, err := json.Marshal(plain(v))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(body, v.extra)
}

func (v *Operation) UnmarshalJSON(body []byte) error {
	type plain Operation
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	extra, err := unknownFields(body, "id", "kind", "phase", "sessionIds", "workspaceId", "lifecycle", "expectedGeneration", "resultGeneration", "recoveryEntryId", "mapping", "presentation", "dependencies", "requestFingerprint", "request", "result")
	if err != nil {
		return err
	}
	*v = Operation(decoded)
	v.extra = extra
	return nil
}
func (v Operation) MarshalJSON() ([]byte, error) {
	type plain Operation
	body, err := json.Marshal(plain(v))
	if err != nil {
		return nil, err
	}
	return mergeUnknown(body, v.extra)
}
