package doctext

import (
	"encoding/base64"
	"strings"
	"testing"
)

// der builds one definite-length element.
func der(tag byte, content []byte) []byte {
	out := []byte{tag}
	switch n := len(content); {
	case n < 0x80:
		out = append(out, byte(n))
	case n < 1<<8:
		out = append(out, 0x81, byte(n))
	case n < 1<<16:
		out = append(out, 0x82, byte(n>>8), byte(n))
	default:
		out = append(out, 0x83, byte(n>>16), byte(n>>8), byte(n))
	}
	return append(out, content...)
}

// ber builds one indefinite-length constructed element, the form a signing tool
// produces when it streams a document into the envelope.
func ber(tag byte, content []byte) []byte {
	out := append([]byte{tag | 0x20, 0x80}, content...)
	return append(out, 0x00, 0x00)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// signedEnvelope wraps a payload the way a CAdES signature does.
func signedEnvelope(payload []byte) []byte {
	encap := der(0x30, concat(
		der(0x06, oidData),
		der(0xa0, der(0x04, payload)),
	))
	signedData := der(0x30, concat(
		der(0x02, []byte{0x01}), // version
		der(0x31, nil),          // digestAlgorithms
		encap,
		der(0x31, nil), // signerInfos
	))
	return der(0x30, concat(
		der(0x06, oidSignedData),
		der(0xa0, signedData),
	))
}

func TestUnwrapPKCS7(t *testing.T) {
	payload := []byte("Capitolato speciale d'appalto")

	got, err := unwrapPKCS7(signedEnvelope(payload))
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload = %q, want %q", got, payload)
	}
}

func TestUnwrapPKCS7Base64(t *testing.T) {
	payload := []byte("Disciplinare di gara")
	armoured := base64.StdEncoding.EncodeToString(signedEnvelope(payload))

	// Signing tools wrap base64 at a fixed column; the newlines must not matter.
	var wrapped strings.Builder
	for i := 0; i < len(armoured); i += 64 {
		wrapped.WriteString(armoured[i:min(i+64, len(armoured))] + "\n")
	}

	got, err := unwrapPKCS7([]byte(wrapped.String()))
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload = %q, want %q", got, payload)
	}
}

func TestUnwrapPKCS7IndefiniteLengthAndSegmentedContent(t *testing.T) {
	// A long document is streamed in as chunks inside a constructed octet
	// string, under indefinite-length wrappers. encoding/asn1 rejects both,
	// which is why this package carries its own reader.
	chunks := concat(
		der(0x04, []byte("Parte prima. ")),
		der(0x04, []byte("Parte seconda.")),
	)
	encap := ber(0x30, concat(
		der(0x06, oidData),
		ber(0xa0, ber(0x04, chunks)),
	))
	signedData := ber(0x30, concat(
		der(0x02, []byte{0x01}),
		der(0x31, nil),
		encap,
		der(0x31, nil),
	))
	envelope := ber(0x30, concat(der(0x06, oidSignedData), ber(0xa0, signedData)))

	got, err := unwrapPKCS7(envelope)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if want := "Parte prima. Parte seconda."; string(got) != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

func TestUnwrapPKCS7DetachedSignatureSaysSo(t *testing.T) {
	// A detached signature signs a file stored beside it, so there is nothing
	// inside to read. Saying "damaged" would send someone looking for a bug.
	encap := der(0x30, der(0x06, oidData))
	signedData := der(0x30, concat(
		der(0x02, []byte{0x01}),
		der(0x31, nil),
		encap,
		der(0x31, nil),
	))
	envelope := der(0x30, concat(der(0x06, oidSignedData), der(0xa0, signedData)))

	_, err := unwrapPKCS7(envelope)
	if err == nil {
		t.Fatal("expected an error for a detached signature")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("error = %q, should name the detached signature", err)
	}
}

func TestExtractP7MReadsTheDocumentInside(t *testing.T) {
	inner := buildPDF(t, []string{textStream([]struct {
		X, Y float64
		S    string
	}{{72, 720, "Capitolato firmato digitalmente"}})})

	docs := Extract("capitolato.pdf.p7m", "application/pkcs7-mime", signedEnvelope(inner), Options{})
	if len(docs) != 1 {
		t.Fatalf("got %d documents, want 1", len(docs))
	}
	d := docs[0]

	if d.Kind != KindPDF {
		t.Errorf("kind = %q, want %q — the envelope should be transparent", d.Kind, KindPDF)
	}
	// The name a person would quote is the one the file had before signing.
	if d.Name != "capitolato.pdf" {
		t.Errorf("name = %q, want capitolato.pdf", d.Name)
	}
	if !strings.Contains(d.Text, "Capitolato firmato digitalmente") {
		t.Errorf("did not read the signed document:\n%s", d.Text)
	}
}

func TestExtractP7MGarbageIsReportedNotCrashed(t *testing.T) {
	d := Extract("allegato.pdf.p7m", "", []byte{0x30, 0x82, 0xff, 0xff, 0x01, 0x02}, Options{})[0]

	if d.Status != StatusDamaged {
		t.Errorf("status = %q, want %q", d.Status, StatusDamaged)
	}
	if d.Reason == "" {
		t.Error("a damaged envelope must say what went wrong")
	}
}
