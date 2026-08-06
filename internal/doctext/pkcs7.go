package doctext

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Italian procurement runs on digitally signed files. The capitolato is not
// published as capitolato.pdf but as capitolato.pdf.p7m: the PDF sealed inside
// a CAdES envelope, which is a CMS SignedData structure. The signature is what
// makes the document legally the buyer's; the document itself is still in
// there, whole, as the encapsulated content.
//
// This file opens the envelope and returns what was signed. It deliberately
// does not verify the signature: whether the seal is valid is a legal question
// about a party's identity, and answering it would need a trust store, current
// revocation data, and a timestamp — none of which a document reader should
// pretend to have. Extracting the payload to read it makes no claim either way.

var (
	oidSignedData = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02}
	oidData       = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x01}
)

// unwrapPKCS7 returns the file that was signed.
func unwrapPKCS7(data []byte) ([]byte, error) {
	der, err := toDER(data)
	if err != nil {
		return nil, err
	}

	root, _, err := readTLV(der)
	if err != nil {
		return nil, fmt.Errorf("not a signature envelope: %w", err)
	}
	if !root.isSequence() {
		return nil, errors.New("not a signature envelope: no outer sequence")
	}

	payload, found := findPayload(root, 0)
	if !found {
		// A detached signature travels beside the file it signs rather than
		// around it, so there is genuinely nothing here to read.
		if hasOID(root, oidSignedData, 0) {
			return nil, errors.New("this is a detached signature: it signs a document stored separately, " +
				"so the document itself is not in this file")
		}
		return nil, errors.New("no signed content found inside the envelope")
	}
	if len(payload) == 0 {
		return nil, errors.New("the envelope carries an empty document")
	}
	return payload, nil
}

// toDER accepts the two forms these files arrive in: raw DER, and the same DER
// in base64, which is what a mail client or a portal export usually produces.
func toDER(data []byte) ([]byte, error) {
	if len(data) > 0 && data[0] == 0x30 {
		return data, nil
	}

	text := string(bytes.TrimSpace(data))
	if !looksTextual(data) {
		return nil, errors.New("not a signature envelope: neither DER nor base64")
	}
	// Strip PEM armour if present, then all whitespace: base64 in these files
	// is wrapped at 64 or 76 columns.
	if i := strings.Index(text, "-----BEGIN"); i >= 0 {
		if j := strings.Index(text[i:], "\n"); j >= 0 {
			text = text[i+j+1:]
		}
		if k := strings.Index(text, "-----END"); k >= 0 {
			text = text[:k]
		}
	}
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, text)

	der, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("not a signature envelope: %w", err)
	}
	if len(der) == 0 || der[0] != 0x30 {
		return nil, errors.New("not a signature envelope: decoded bytes are not DER")
	}
	return der, nil
}

// maxEnvelopeDepth bounds the walk. Real envelopes nest a handful of levels;
// anything deeper is a malformed or hostile file rather than a document.
const maxEnvelopeDepth = 24

// findPayload looks for the EncapsulatedContentInfo — a sequence whose first
// element is the "data" OID and whose second holds the bytes that were signed.
// It searches rather than following a fixed path, because the envelope's shape
// varies with the signature profile and the search is cheap.
func findPayload(t tlv, depth int) ([]byte, bool) {
	if depth > maxEnvelopeDepth || !t.constructed {
		return nil, false
	}

	kids, err := t.children()
	if err != nil {
		return nil, false
	}

	if t.isSequence() && len(kids) >= 2 && kids[0].isOID(oidData) {
		// eContent is [0] EXPLICIT: a context-tagged wrapper around the octet
		// string that holds the document.
		if c := kids[1]; c.class == classContext && c.tag == 0 {
			if inner, err := c.children(); err == nil && len(inner) > 0 {
				return octets(inner[0]), true
			}
			return c.content, true
		}
	}

	for _, k := range kids {
		if payload, ok := findPayload(k, depth+1); ok {
			return payload, ok
		}
	}
	return nil, false
}

// octets returns an OCTET STRING's bytes. BER allows a long one to be split
// into chunks, and signing tools do split them, so a constructed string has to
// be stitched back together.
func octets(t tlv) []byte {
	if !t.constructed {
		return t.content
	}
	kids, err := t.children()
	if err != nil {
		return t.content
	}
	var out []byte
	for _, k := range kids {
		out = append(out, octets(k)...)
	}
	return out
}

// hasOID reports whether an OID appears anywhere in the structure.
func hasOID(t tlv, oid []byte, depth int) bool {
	if depth > maxEnvelopeDepth {
		return false
	}
	if t.isOID(oid) {
		return true
	}
	kids, err := t.children()
	if err != nil {
		return false
	}
	for _, k := range kids {
		if hasOID(k, oid, depth+1) {
			return true
		}
	}
	return false
}

// --- a small BER reader ---
//
// encoding/asn1 is not usable here: it parses DER, and CMS envelopes are BER.
// Signing tools stream the document into the envelope, which means indefinite
// lengths and segmented octet strings — both legal BER, both rejected outright
// by a DER parser. Only enough BER is implemented to walk the structure and
// find the payload.

const (
	classUniversal = 0
	classContext   = 2

	tagOID         = 6
	tagSequence    = 16
	tagOctetString = 4
)

type tlv struct {
	class       byte
	constructed bool
	tag         int
	content     []byte
}

func (t tlv) isSequence() bool {
	return t.class == classUniversal && t.tag == tagSequence && t.constructed
}

func (t tlv) isOID(oid []byte) bool {
	return t.class == classUniversal && t.tag == tagOID && bytes.Equal(t.content, oid)
}

var errTruncated = errors.New("truncated")

// children parses an element's content as a sequence of elements.
func (t tlv) children() ([]tlv, error) {
	if !t.constructed {
		return nil, nil
	}
	var out []tlv
	for i := 0; i < len(t.content); {
		child, n, err := readTLV(t.content[i:])
		if err != nil {
			return out, err
		}
		out = append(out, child)
		i += n
	}
	return out, nil
}

// readTLV parses one BER element, returning it and how many bytes it occupied.
func readTLV(data []byte) (tlv, int, error) {
	if len(data) < 2 {
		return tlv{}, 0, errTruncated
	}

	b := data[0]
	t := tlv{class: b >> 6, constructed: b&0x20 != 0, tag: int(b & 0x1f)}
	i := 1

	if t.tag == 0x1f { // tag number continues in the following bytes
		t.tag = 0
		for {
			if i >= len(data) {
				return tlv{}, 0, errTruncated
			}
			c := data[i]
			i++
			t.tag = t.tag<<7 | int(c&0x7f)
			if c&0x80 == 0 {
				break
			}
			if t.tag > 1<<20 {
				return tlv{}, 0, errors.New("implausible tag number")
			}
		}
	}

	if i >= len(data) {
		return tlv{}, 0, errTruncated
	}
	l := data[i]
	i++

	switch {
	case l == 0x80:
		// Indefinite length: the content runs to an end-of-contents marker, so
		// the children have to be walked to find where this element stops.
		if !t.constructed {
			return tlv{}, 0, errors.New("indefinite length on a primitive element")
		}
		start := i
		for {
			if i+1 >= len(data) {
				return tlv{}, 0, errTruncated
			}
			if data[i] == 0x00 && data[i+1] == 0x00 {
				t.content = data[start:i]
				return t, i + 2, nil
			}
			_, n, err := readTLV(data[i:])
			if err != nil {
				return tlv{}, 0, err
			}
			i += n
		}

	case l&0x80 == 0:
		length := int(l)
		if i+length > len(data) {
			return tlv{}, 0, errTruncated
		}
		t.content = data[i : i+length]
		return t, i + length, nil

	default:
		n := int(l & 0x7f)
		if n > 8 || i+n > len(data) {
			return tlv{}, 0, errTruncated
		}
		length := 0
		for j := 0; j < n; j++ {
			length = length<<8 | int(data[i])
			i++
			if length > 1<<40 {
				return tlv{}, 0, errors.New("implausible length")
			}
		}
		if length < 0 || i+length > len(data) {
			return tlv{}, 0, errTruncated
		}
		t.content = data[i : i+length]
		return t, i + length, nil
	}
}
