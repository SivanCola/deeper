package provider

import (
	"encoding/json"
	"fmt"

	"deepzora/internal/attachment"
)

// Images is a transient provider encoding. Durable messages always use ImageInputs.
func (m Message) MarshalJSON() ([]byte, error) {
	type stored Message
	m, err := m.StorageView()
	if err != nil {
		return nil, err
	}
	return json.Marshal(stored(m))
}

// StorageView selects the single durable representation of current wire inputs.
func (m Message) StorageView() (Message, error) {
	if m.ReasoningSignature != "" && len(m.ThinkingBlocks) == 0 {
		m.ThinkingBlocks = []ThinkingBlock{{Type: "thinking", Thinking: m.ReasoningContent, Signature: m.ReasoningSignature}}
	}
	if m.ReasoningState == "" && (m.ReasoningContent != "" || len(m.ThinkingBlocks) > 0 || m.ReasoningID != "") {
		m.ReasoningState = ReasoningComplete
		if m.LocalOnly || m.ReasoningStatus == "in_progress" || m.ReasoningStatus == "incomplete" {
			m.ReasoningState = ReasoningIncomplete
		}
	}
	m.ReasoningSignature = ""
	if len(m.Images) > 0 {
		if len(m.ImageInputs) > 0 {
			return Message{}, fmt.Errorf("message cannot combine Images with ImageInputs")
		}
		m.ImageInputs = make([]attachment.ImageInput, 0, len(m.Images))
		for _, image := range m.Images {
			input := attachment.ImageInput{Kind: attachment.KindURL, URL: image}
			if ClassifyImage(image) == ImageFileID {
				input = attachment.ImageInput{Kind: attachment.KindFiles, FilesID: image}
			}
			m.ImageInputs = append(m.ImageInputs, input)
		}
	}
	m.Images = nil
	return m, nil
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type stored Message
	var raw struct {
		*stored
		Images             json.RawMessage `json:"images"`
		ReasoningSignature json.RawMessage `json:"reasoning_signature"`
	}
	var value stored
	raw.stored = &value
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Images) > 0 {
		return fmt.Errorf("legacy message images field is unsupported; use image_inputs")
	}
	if len(raw.ReasoningSignature) > 0 {
		return fmt.Errorf("legacy message reasoning_signature is unsupported; use thinking_blocks")
	}
	if value.ReasoningState == "" && (value.ReasoningContent != "" || len(value.ThinkingBlocks) > 0 || value.ReasoningID != "") {
		return fmt.Errorf("stored reasoning_state is required")
	}
	for _, input := range value.ImageInputs {
		if err := input.Validate(); err != nil {
			return err
		}
	}
	*m = Message(value)
	return nil
}
