package transport

import (
	"bytes"
	"io"
	"testing"
)

func TestEnvelope_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		env  Envelope
	}{
		{
			name: "basic payload",
			env: Envelope{
				SessionID:  "abc123",
				Seq:        42,
				TargetAddr: "example.com:443",
				Payload:    []byte("hello world"),
				Close:      false,
			},
		},
		{
			name: "close with empty payload",
			env: Envelope{
				SessionID:  "sess-close",
				Seq:        0,
				TargetAddr: "",
				Payload:    nil,
				Close:      true,
			},
		},
		{
			name: "large sequence number",
			env: Envelope{
				SessionID:  "big-seq",
				Seq:        ^uint64(0),
				TargetAddr: "10.0.0.1:80",
				Payload:    []byte{0x00, 0xFF, 0x01},
				Close:      false,
			},
		},
		{
			name: "empty everything",
			env: Envelope{
				SessionID:  "",
				Seq:        0,
				TargetAddr: "",
				Payload:    nil,
				Close:      false,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.env.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}

			var decoded Envelope
			n, err := decoded.UnmarshalBinary(data)
			if err != nil {
				t.Fatalf("UnmarshalBinary failed: %v", err)
			}
			if n != len(data) {
				t.Errorf("consumed %d bytes, expected %d", n, len(data))
			}

			if decoded.SessionID != tc.env.SessionID {
				t.Errorf("SessionID mismatch: got %q, want %q", decoded.SessionID, tc.env.SessionID)
			}
			if decoded.Seq != tc.env.Seq {
				t.Errorf("Seq mismatch: got %d, want %d", decoded.Seq, tc.env.Seq)
			}
			if decoded.TargetAddr != tc.env.TargetAddr {
				t.Errorf("TargetAddr mismatch: got %q, want %q", decoded.TargetAddr, tc.env.TargetAddr)
			}
			if decoded.Close != tc.env.Close {
				t.Errorf("Close mismatch: got %v, want %v", decoded.Close, tc.env.Close)
			}
			if !bytes.Equal(decoded.Payload, tc.env.Payload) {
				t.Errorf("Payload mismatch: got %v, want %v", decoded.Payload, tc.env.Payload)
			}
		})
	}
}

func TestEnvelope_EncodeDecode(t *testing.T) {
	original := Envelope{
		SessionID:  "stream-test",
		Seq:        7,
		TargetAddr: "host.example:8080",
		Payload:    []byte("streaming data test"),
		Close:      false,
	}

	var buf bytes.Buffer
	if err := original.Encode(&buf); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	var decoded Envelope
	if err := decoded.Decode(&buf); err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded.SessionID != original.SessionID {
		t.Errorf("SessionID: got %q, want %q", decoded.SessionID, original.SessionID)
	}
	if decoded.Seq != original.Seq {
		t.Errorf("Seq: got %d, want %d", decoded.Seq, original.Seq)
	}
	if decoded.TargetAddr != original.TargetAddr {
		t.Errorf("TargetAddr: got %q, want %q", decoded.TargetAddr, original.TargetAddr)
	}
	if decoded.Close != original.Close {
		t.Errorf("Close: got %v, want %v", decoded.Close, original.Close)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Errorf("Payload: got %v, want %v", decoded.Payload, original.Payload)
	}
}

func TestEnvelope_MultipleEncodeDecode(t *testing.T) {
	envs := []Envelope{
		{SessionID: "s1", Seq: 0, Payload: []byte("first")},
		{SessionID: "s2", Seq: 1, Payload: []byte("second"), TargetAddr: "a:1"},
		{SessionID: "s1", Seq: 1, Close: true},
	}

	var buf bytes.Buffer
	for _, e := range envs {
		if err := e.Encode(&buf); err != nil {
			t.Fatalf("Encode failed: %v", err)
		}
	}

	for i, expected := range envs {
		var decoded Envelope
		if err := decoded.Decode(&buf); err != nil {
			t.Fatalf("Decode[%d] failed: %v", i, err)
		}
		if decoded.SessionID != expected.SessionID {
			t.Errorf("[%d] SessionID: got %q, want %q", i, decoded.SessionID, expected.SessionID)
		}
		if decoded.Seq != expected.Seq {
			t.Errorf("[%d] Seq: got %d, want %d", i, decoded.Seq, expected.Seq)
		}
		if decoded.Close != expected.Close {
			t.Errorf("[%d] Close: got %v, want %v", i, decoded.Close, expected.Close)
		}
	}

	// Should now get EOF
	var extra Envelope
	if err := extra.Decode(&buf); err != io.EOF {
		t.Errorf("expected EOF after all envelopes, got: %v", err)
	}
}

func TestEnvelope_InvalidMagicByte(t *testing.T) {
	data := []byte{0xAA, 0x03, 'a', 'b', 'c'}
	var e Envelope
	_, err := e.UnmarshalBinary(data)
	if err == nil {
		t.Fatal("expected error for invalid magic byte")
	}
}

func TestEnvelope_TruncatedData(t *testing.T) {
	env := Envelope{SessionID: "test", Seq: 1, Payload: []byte("hello")}
	data, _ := env.MarshalBinary()

	// Try progressively shorter slices
	for i := 0; i < len(data)-1; i++ {
		var decoded Envelope
		_, err := decoded.UnmarshalBinary(data[:i])
		if err == nil {
			t.Errorf("expected error at truncation point %d/%d", i, len(data))
		}
	}
}

func TestEnvelope_DecodePayloadTooLarge(t *testing.T) {
	// Craft an envelope claiming a payload > 10MB
	env := Envelope{SessionID: "x", Seq: 0, Payload: []byte("y")}
	var buf bytes.Buffer
	env.Encode(&buf)

	// Manually patch the payload length in the buffer to exceed 10MB
	data := buf.Bytes()
	// Find the payload length field (last 4 bytes before the actual payload)
	// The payload "y" is 1 byte, so last 5 bytes are: [paylen(4)] [payload(1)]
	payLenOffset := len(data) - 5
	data[payLenOffset] = 0x01 // 16MB+
	data[payLenOffset+1] = 0x00
	data[payLenOffset+2] = 0x00
	data[payLenOffset+3] = 0x00

	var decoded Envelope
	err := decoded.Decode(bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}
