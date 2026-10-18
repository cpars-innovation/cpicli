package ops

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

// PDInspectOptions bound InspectPDBinary. Zero values take the defaults.
type PDInspectOptions struct {
	// MaxBytes limits decompressed content and a zip entry read (default
	// 16 MiB). Larger content is cut and marked Truncated.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// MaxEntries limits the zip entries listed (default 1000).
	MaxEntries int `json:"maxEntries,omitempty"`
	// MaxDepth limits nested content (a gz in a zip in a gz; default 3).
	MaxDepth int `json:"maxDepth,omitempty"`
	// TextBytes limits Text (default 1 MiB).
	TextBytes int `json:"textBytes,omitempty"`
	// ZipEntry is the zip entry to read and inspect (Entry).
	ZipEntry string `json:"zipEntry,omitempty"`
}

func (o PDInspectOptions) withDefaults() PDInspectOptions {
	if o.MaxBytes <= 0 {
		o.MaxBytes = 16 << 20
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = 1000
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = 3
	}
	if o.TextBytes <= 0 {
		o.TextBytes = 1 << 20
	}
	return o
}

// PDInspection is what InspectPDBinary found in a binary.
type PDInspection struct {
	// DeclaredType is the parameter's content type ("xml"), DetectedType
	// what the bytes are: xml, xsd, xsl, json, txt, zip, gz, zlib, crt or
	// binary.
	DeclaredType string `json:"declaredType,omitempty"`
	DetectedType string `json:"detectedType"`
	// Mismatch: the bytes are not what the content type says (xsd and xsl
	// content are xml, and xml and json content are txt, too).
	Mismatch bool   `json:"mismatch"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	// Truncated: the content was cut at MaxBytes (decompressed content or a
	// zip entry), so Size and SHA256 are of the part read.
	Truncated bool `json:"truncated,omitempty"`
	// Text is the content of text types, cut at TextBytes (TextTruncated).
	Text          string `json:"text,omitempty"`
	TextTruncated bool   `json:"textTruncated,omitempty"`
	// Inner is the decompressed content of gz and zlib.
	Inner *PDInspection `json:"inner,omitempty"`
	// Entries are the entries of a zip (MaxEntries; EntriesTruncated).
	Entries          []PDZipEntry `json:"entries,omitempty"`
	EntriesTruncated bool         `json:"entriesTruncated,omitempty"`
	// Entry is the inspected ZipEntry.
	Entry *PDInspection `json:"entry,omitempty"`
	// Certificates of a crt (PEM may hold a chain).
	Certificates []PDCertificate `json:"certificates,omitempty"`
	Warnings     []string        `json:"warnings,omitempty"`
}

// PDZipEntry is a zip entry. Size is the uncompressed size the zip
// declares.
type PDZipEntry struct {
	Name           string    `json:"name"`
	Dir            bool      `json:"dir,omitempty"`
	Size           int64     `json:"size"`
	CompressedSize int64     `json:"compressedSize"`
	Modified       time.Time `json:"modified,omitzero"`
}

// PDCertificate is an X.509 certificate.
type PDCertificate struct {
	Subject            string    `json:"subject"`
	Issuer             string    `json:"issuer"`
	SerialNumber       string    `json:"serialNumber"`
	NotBefore          time.Time `json:"notBefore"`
	NotAfter           time.Time `json:"notAfter"`
	Expired            bool      `json:"expired"`
	DaysLeft           int       `json:"daysLeft"`
	KeyAlgorithm       string    `json:"keyAlgorithm"`
	KeyBits            int       `json:"keyBits,omitempty"`
	SignatureAlgorithm string    `json:"signatureAlgorithm"`
	IsCA               bool      `json:"isCA,omitempty"`
	SelfSigned         bool      `json:"selfSigned,omitempty"`
	DNSNames           []string  `json:"dnsNames,omitempty"`
	EmailAddresses     []string  `json:"emailAddresses,omitempty"`
	IPAddresses        []string  `json:"ipAddresses,omitempty"`
	URIs               []string  `json:"uris,omitempty"`
	SHA1               string    `json:"sha1"`
	SHA256             string    `json:"sha256"`
}

// pdNow is the clock for certificate validity (tests).
var pdNow = time.Now

// InspectPDBinary detects what a Partner Directory binary is and looks
// inside: it decompresses gz and zlib, lists zip entries and reads one
// (opts.ZipEntry), parses certificates, and returns text content. Memory
// and work are bounded by opts (zip bombs, deep nesting), and nothing is
// written anywhere. contentType is the parameter's ContentType ("" if
// unknown).
func InspectPDBinary(data []byte, contentType string, opts PDInspectOptions) (*PDInspection, error) {
	return inspectPD(data, baseContentType(contentType), false, opts.withDefaults(), 0), nil
}

func inspectPD(data []byte, declared string, truncated bool, o PDInspectOptions, depth int) *PDInspection {
	in := &PDInspection{DeclaredType: declared, Size: int64(len(data)), SHA256: sha256Hex(data), Truncated: truncated}
	in.DetectedType = detectPDType(data)
	in.Mismatch = declared != "" && !pdTypeFits(declared, in.DetectedType)
	nested := func() bool {
		if depth+1 >= o.MaxDepth {
			in.Warnings = append(in.Warnings, fmt.Sprintf("nested deeper than %d levels: not inspected", o.MaxDepth))
			return false
		}
		return true
	}
	switch in.DetectedType {
	case "xml", "xsd", "xsl", "json", "txt":
		in.Text, in.TextTruncated = pdText(data, o.TextBytes)
	case "gz", "zlib":
		if !nested() {
			break
		}
		var r io.ReadCloser
		var err error
		if in.DetectedType == "gz" {
			r, err = gzip.NewReader(bytes.NewReader(data))
		} else {
			r, err = zlib.NewReader(bytes.NewReader(data))
		}
		if err != nil {
			in.Warnings = append(in.Warnings, "not decompressible: "+err.Error())
			break
		}
		inner, cut, err := readBounded(r, o.MaxBytes)
		r.Close()
		if err != nil {
			in.Warnings = append(in.Warnings, "decompression failed after "+fmt.Sprint(len(inner))+" bytes: "+err.Error())
		}
		if cut {
			in.Warnings = append(in.Warnings, fmt.Sprintf("decompressed content larger than %d bytes: cut", o.MaxBytes))
		}
		in.Inner = inspectPD(inner, "", cut || err != nil, o, depth+1)
	case "zip":
		inspectZip(in, data, o, depth, nested)
	case "crt":
		certs, err := parseCertificates(data)
		if err != nil {
			in.Warnings = append(in.Warnings, "certificate not readable: "+err.Error())
		}
		in.Certificates = certs
	}
	return in
}

func inspectZip(in *PDInspection, data []byte, o PDInspectOptions, depth int, nested func() bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		in.Warnings = append(in.Warnings, "not a readable zip: "+err.Error())
		return
	}
	in.Entries = []PDZipEntry{}
	for i, f := range zr.File {
		if i == o.MaxEntries {
			in.EntriesTruncated = true
			break
		}
		in.Entries = append(in.Entries, PDZipEntry{Name: f.Name, Dir: f.FileInfo().IsDir(), Size: int64(f.UncompressedSize64),
			CompressedSize: int64(f.CompressedSize64), Modified: f.Modified.UTC()})
	}
	if o.ZipEntry == "" {
		return
	}
	var entry *zip.File
	for _, f := range zr.File {
		if f.Name == o.ZipEntry {
			entry = f
			break
		}
	}
	switch {
	case entry == nil:
		in.Warnings = append(in.Warnings, "no zip entry "+o.ZipEntry)
	case entry.FileInfo().IsDir():
		in.Warnings = append(in.Warnings, o.ZipEntry+" is a folder")
	case nested():
		rc, err := entry.Open()
		if err != nil {
			in.Warnings = append(in.Warnings, o.ZipEntry+": "+err.Error())
			return
		}
		content, cut, err := readBounded(rc, o.MaxBytes)
		rc.Close()
		if err != nil {
			in.Warnings = append(in.Warnings, o.ZipEntry+": "+err.Error())
		}
		if cut {
			in.Warnings = append(in.Warnings, fmt.Sprintf("%s larger than %d bytes: cut", o.ZipEntry, o.MaxBytes))
		}
		declared, _ := PDContentTypeForExtension(path.Ext(entry.Name))
		sub := o
		sub.ZipEntry = "" // the name is of this zip, not of a zip inside
		in.Entry = inspectPD(content, declared, cut || err != nil, sub, depth+1)
	}
}

// readBounded reads at most max bytes; cut reports that there was more.
func readBounded(r io.Reader, max int64) (data []byte, cut bool, err error) {
	data, err = io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(data)) > max {
		return data[:max], true, err
	}
	return data, false, err
}

// detectPDType tells the type from the bytes.
func detectPDType(data []byte) string {
	switch {
	case len(data) == 0:
		return "txt"
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		return "gz"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")), bytes.HasPrefix(data, []byte("PK\x05\x06")):
		return "zip"
	case isZlib(data):
		return "zlib"
	}
	text := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	if bytes.HasPrefix(text, []byte("-----BEGIN CERTIFICATE-----")) {
		return "crt"
	}
	if data[0] == 0x30 {
		if _, err := x509.ParseCertificate(data); err == nil {
			return "crt"
		}
	}
	sample := data[:min(len(data), 1<<20)]
	if len(sample) < len(data) {
		sample = trimIncompleteRune(sample)
	}
	if !utf8.Valid(sample) || bytes.IndexByte(sample, 0) >= 0 {
		return "binary"
	}
	if len(text) > 0 && (text[0] == '{' || text[0] == '[') && json.Valid(text) {
		return "json"
	}
	if len(text) > 0 && text[0] == '<' {
		return xmlKind(text)
	}
	return "txt"
}

// isZlib: a zlib header (deflate, check bits) and a stream that starts to
// inflate; the header alone matches text such as "x^".
func isZlib(data []byte) bool {
	if len(data) < 3 || data[0]&0x0f != 8 || data[0]>>4 > 7 || (uint16(data[0])<<8|uint16(data[1]))%31 != 0 {
		return false
	}
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return false
	}
	defer r.Close()
	_, err = r.Read(make([]byte, 1))
	return err == nil || err == io.EOF
}

// xmlKind is xsd or xsl for a schema or stylesheet root, else xml.
func xmlKind(data []byte) string {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	for range 64 { // prolog, comments, doctype
		tok, err := d.Token()
		if err != nil {
			return "xml"
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch {
			case se.Name.Local == "schema" && se.Name.Space == "http://www.w3.org/2001/XMLSchema":
				return "xsd"
			case (se.Name.Local == "stylesheet" || se.Name.Local == "transform") && se.Name.Space == "http://www.w3.org/1999/XSL/Transform":
				return "xsl"
			}
			return "xml"
		}
	}
	return "xml"
}

// pdTypeFits: content of type detected may be declared as declared.
func pdTypeFits(declared, detected string) bool {
	switch {
	case declared == detected:
		return true
	case declared == "xml":
		return detected == "xsd" || detected == "xsl"
	case declared == "txt":
		return detected == "xml" || detected == "xsd" || detected == "xsl" || detected == "json"
	case declared == "xsd" || declared == "xsl":
		// an empty or a generic XML root (e.g. a schema with a prefix the
		// decoder could not resolve) is still plausible
		return detected == "xml"
	}
	return false
}

// pdText returns data as text, cut at max bytes on a rune boundary.
func pdText(data []byte, max int) (string, bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(data) <= max {
		return string(data), false
	}
	return string(trimIncompleteRune(data[:max])), true
}

// parseCertificates parses PEM (every CERTIFICATE block) or DER.
func parseCertificates(data []byte) ([]PDCertificate, error) {
	var ders [][]byte
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			ders = append(ders, block.Bytes)
		}
	}
	if len(ders) == 0 {
		ders = [][]byte{data}
	}
	var out []PDCertificate
	for _, der := range ders {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return out, err
		}
		out = append(out, pdCertificate(c))
	}
	return out, nil
}

func pdCertificate(c *x509.Certificate) PDCertificate {
	now := pdNow()
	s1, s256 := sha1.Sum(c.Raw), sha256.Sum256(c.Raw)
	pc := PDCertificate{
		Subject: c.Subject.String(), Issuer: c.Issuer.String(), SerialNumber: strings.ToUpper(c.SerialNumber.Text(16)),
		NotBefore: c.NotBefore.UTC(), NotAfter: c.NotAfter.UTC(), Expired: now.After(c.NotAfter),
		DaysLeft:     int(c.NotAfter.Sub(now).Hours() / 24),
		KeyAlgorithm: c.PublicKeyAlgorithm.String(), SignatureAlgorithm: c.SignatureAlgorithm.String(),
		IsCA: c.IsCA, SelfSigned: bytes.Equal(c.RawSubject, c.RawIssuer),
		DNSNames: c.DNSNames, EmailAddresses: c.EmailAddresses,
		SHA1: hex.EncodeToString(s1[:]), SHA256: hex.EncodeToString(s256[:]),
	}
	pc.KeyBits = publicKeyBits(c.PublicKey)
	for _, ip := range c.IPAddresses {
		pc.IPAddresses = append(pc.IPAddresses, ip.String())
	}
	for _, u := range c.URIs {
		pc.URIs = append(pc.URIs, u.String())
	}
	return pc
}

// publicKeyBits is the key size of RSA and ECDSA keys (0 otherwise).
func publicKeyBits(k any) int {
	switch k := k.(type) {
	case *rsa.PublicKey:
		return k.N.BitLen()
	case *ecdsa.PublicKey:
		return k.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 256
	}
	return 0
}
