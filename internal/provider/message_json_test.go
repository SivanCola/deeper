package provider

import (
	"deepzora/internal/attachment"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestModelMessagesRetainReferencesForControllerResolution(t *testing.T) {
	m := Message{Role: RoleUser, ImageInputs: []attachment.ImageInput{{Kind: attachment.KindURL, URL: "https://example.test/image.png"}, {Kind: attachment.KindAttachment, Attachment: &attachment.AttachmentRef{}}}}
	wire := ModelMessages([]Message{m})
	if len(wire) != 1 || len(wire[0].ImageInputs) != 2 || len(wire[0].Images) != 0 {
		t.Fatalf("references lost before controller resolution: %+v", wire)
	}
}

func TestMessageImagesUseCurrentDurableInputs(t *testing.T) {
	images := []string{"https://example.test/image.png", "file-api-0a1b2c3d4e5f60718293a4b5c6d7e8f9"}
	data, err := json.Marshal(Message{Role: RoleUser, Content: "describe", Images: images})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"images"`) || !strings.Contains(string(data), `"image_inputs"`) {
		t.Fatalf("durable image encoding = %s", data)
	}
	var loaded Message
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Images) != 0 || len(loaded.ImageInputs) != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
	wire := ModelMessages([]Message{loaded})
	if !reflect.DeepEqual(wire[0].Images, images) || len(wire[0].ImageInputs) != 0 {
		t.Fatalf("wire = %+v", wire)
	}
	if len(loaded.ImageInputs) != 2 {
		t.Fatal("wire projection mutated stored inputs")
	}
}

func TestMessageRejectsOldImagesField(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"role":"user","images":["https://example.test/image.png"]}`), &m); err == nil {
		t.Fatal("old durable images field was accepted")
	}
}

func TestCurrentWireProofPersistsAsThinkingBlock(t *testing.T) {
	m := Message{Role: RoleAssistant, ReasoningContent: "thought", ReasoningSignature: "proof"}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"reasoning_signature"`) {
		t.Fatalf("old mirror written: %s", raw)
	}
	var loaded Message
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ReasoningState != ReasoningComplete || len(loaded.ThinkingBlocks) != 1 || loaded.ThinkingBlocks[0].Signature != "proof" {
		t.Fatalf("proof lost: %+v", loaded)
	}
	for _, old := range []string{`{"role":"assistant","reasoning_content":"thought"}`, `{"role":"assistant","reasoning_content":"thought","reasoning_signature":"proof"}`} {
		if err := json.Unmarshal([]byte(old), &loaded); err == nil {
			t.Fatal("old reasoning format accepted")
		}
	}
}
