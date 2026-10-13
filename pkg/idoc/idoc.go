// Package idoc reads and writes IDoc XML as SAP systems exchange it with
// SAP Cloud Integration's IDoc adapter: the control record EDI_DC40 of each
// IDoc, with or without a SOAP envelope, and a skeleton to start a test
// message from.
package idoc

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// ContentType is the content type of IDoc XML (the IDoc receiver's
// IDocContentType in SAP's content).
const ContentType = "application/x-sap.idoc"

// Control is the control record (EDI_DC40) of one IDoc, with the fields
// that identify it.
type Control struct {
	DOCNUM  string `json:"DOCNUM,omitempty"`
	DIRECT  string `json:"DIRECT,omitempty"`
	IDOCTYP string `json:"IDOCTYP,omitempty"`
	CIMTYP  string `json:"CIMTYP,omitempty"`
	MESTYP  string `json:"MESTYP,omitempty"`
	SNDPOR  string `json:"SNDPOR,omitempty"`
	SNDPRT  string `json:"SNDPRT,omitempty"`
	SNDPRN  string `json:"SNDPRN,omitempty"`
	RCVPOR  string `json:"RCVPOR,omitempty"`
	RCVPRT  string `json:"RCVPRT,omitempty"`
	RCVPRN  string `json:"RCVPRN,omitempty"`
}

// Document is a parsed IDoc XML message.
type Document struct {
	// XML is the IDoc XML without a SOAP envelope.
	XML []byte `json:"-"`
	// SOAP reports that the message came in a SOAP envelope.
	SOAP bool `json:"soap,omitempty"`
	// Root is the root element of the IDoc XML (the IDoc type or the
	// message of an IDoc SOAP request).
	Root string `json:"root"`
	// IDocs are the control records, one per IDoc in the message.
	IDocs []Control `json:"idocs"`
}

// First is the control record of the first IDoc.
func (d *Document) First() Control { return d.IDocs[0] }

// ErrNoControlRecord is returned for XML without an EDI_DC40 control record.
var ErrNoControlRecord = errors.New("no IDoc control record (EDI_DC40) found")

// Parse reads IDoc XML, unwrapping a SOAP envelope (the IDoc is the first
// element in the SOAP Body). It fails when the XML is not well-formed or
// holds no EDI_DC40 control record.
func Parse(data []byte) (*Document, error) {
	doc := &Document{XML: data}
	inner, soap, err := unwrapSOAP(data)
	if err != nil {
		return nil, err
	}
	if soap {
		doc.XML, doc.SOAP = inner, true
	}
	d := xml.NewDecoder(bytes.NewReader(doc.XML))
	var cur *Control
	var field string
	var text strings.Builder
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid IDoc XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case doc.Root == "":
				doc.Root = t.Name.Local
			case t.Name.Local == "EDI_DC40" && cur == nil:
				doc.IDocs = append(doc.IDocs, Control{})
				cur = &doc.IDocs[len(doc.IDocs)-1]
			case cur != nil:
				field = t.Name.Local
				text.Reset()
			}
		case xml.CharData:
			if field != "" {
				text.Write(t)
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "EDI_DC40":
				cur = nil
			case cur != nil && t.Name.Local == field:
				cur.set(field, strings.TrimSpace(text.String()))
				field = ""
			}
		}
	}
	if len(doc.IDocs) == 0 {
		return nil, ErrNoControlRecord
	}
	return doc, nil
}

func (c *Control) set(field, v string) {
	switch field {
	case "DOCNUM":
		c.DOCNUM = v
	case "DIRECT":
		c.DIRECT = v
	case "IDOCTYP":
		c.IDOCTYP = v
	case "CIMTYP":
		c.CIMTYP = v
	case "MESTYP":
		c.MESTYP = v
	case "SNDPOR":
		c.SNDPOR = v
	case "SNDPRT":
		c.SNDPRT = v
	case "SNDPRN":
		c.SNDPRN = v
	case "RCVPOR":
		c.RCVPOR = v
	case "RCVPRT":
		c.RCVPRT = v
	case "RCVPRN":
		c.RCVPRN = v
	}
}

// unwrapSOAP returns the first element inside the Body of a SOAP envelope
// (soap true), or the data unchanged when its root is no Envelope.
func unwrapSOAP(data []byte) (inner []byte, soap bool, err error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, bodyDepth := 0, -1
	for {
		off := d.InputOffset()
		tok, err := d.RawToken()
		if err == io.EOF {
			return nil, false, fmt.Errorf("invalid IDoc XML: no element")
		}
		if err != nil {
			return nil, false, fmt.Errorf("invalid IDoc XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1 && t.Name.Local != "Envelope":
				return data, false, nil
			case depth == 2 && t.Name.Local == "Body":
				bodyDepth = depth
			case bodyDepth > 0 && depth == bodyDepth+1:
				for level := 1; level > 0; { // to the element's end
					tok, err := d.RawToken()
					if err != nil {
						return nil, false, fmt.Errorf("invalid IDoc XML in the SOAP Body: %w", err)
					}
					switch tok.(type) {
					case xml.StartElement:
						level++
					case xml.EndElement:
						level--
					}
				}
				return bytes.TrimSpace(data[off:d.InputOffset()]), true, nil
			}
		case xml.EndElement:
			if depth == bodyDepth {
				return nil, false, fmt.Errorf("invalid IDoc XML: the SOAP Body is empty")
			}
			depth--
		}
	}
}

// TemplateOptions are the control record values of a template.
type TemplateOptions struct {
	// IDocType (IDOCTYP, e.g. ORDERS05) and MessageType (MESTYP, e.g.
	// ORDERS) are required.
	IDocType, MessageType string
	// Extension is the IDoc extension (CIMTYP), if any.
	Extension string
	// Sender and receiver: partner number, partner type (default LS) and port.
	SenderPartner, SenderPartnerType, SenderPort       string
	ReceiverPartner, ReceiverPartnerType, ReceiverPort string
	// DocNum is the IDoc number (default 0000000000000001); Direct is 1
	// (outbound from the SAP system, the default) or 2 (inbound).
	DocNum, Direct string
	// Created is the creation time (CREDAT, CRETIM; default now).
	Created time.Time
}

var reName = regexp.MustCompile(`^[A-Za-z_/][A-Za-z0-9_/.-]*$`)

// Template returns an IDoc XML skeleton: the control record EDI_DC40 with
// the standard fields and a comment where the data segments of the IDoc
// type go. It invents no segments: fill them in from the IDoc type's
// definition (transaction WE60 on the SAP system).
func Template(o TemplateOptions) ([]byte, error) {
	if o.IDocType == "" || o.MessageType == "" {
		return nil, errors.New("the IDoc type (IDOCTYP) and the message type (MESTYP) are required")
	}
	root := o.IDocType
	if o.Extension != "" {
		root = o.Extension
	}
	if !reName.MatchString(root) || strings.Contains(root, "/") {
		return nil, fmt.Errorf("invalid IDoc type %q", root)
	}
	if o.Direct == "" {
		o.Direct = "1"
	}
	if o.Direct != "1" && o.Direct != "2" {
		return nil, fmt.Errorf("invalid direction %q (1 outbound, 2 inbound)", o.Direct)
	}
	if o.DocNum == "" {
		o.DocNum = "0000000000000001"
	}
	if o.SenderPartnerType == "" {
		o.SenderPartnerType = "LS"
	}
	if o.ReceiverPartnerType == "" {
		o.ReceiverPartnerType = "LS"
	}
	if o.Created.IsZero() {
		o.Created = time.Now()
	}
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<%s>\n  <IDOC BEGIN=\"1\">\n    <EDI_DC40 SEGMENT=\"1\">\n", root)
	field := func(name, v string) {
		if v == "" {
			fmt.Fprintf(&b, "      <%s/>\n", name)
			return
		}
		fmt.Fprintf(&b, "      <%s>", name)
		_ = xml.EscapeText(&b, []byte(v))
		fmt.Fprintf(&b, "</%s>\n", name)
	}
	field("TABNAM", "EDI_DC40")
	field("DOCNUM", o.DocNum)
	field("DIRECT", o.Direct)
	field("IDOCTYP", o.IDocType)
	if o.Extension != "" {
		field("CIMTYP", o.Extension)
	}
	field("MESTYP", o.MessageType)
	field("SNDPOR", o.SenderPort)
	field("SNDPRT", o.SenderPartnerType)
	field("SNDPRN", o.SenderPartner)
	field("RCVPOR", o.ReceiverPort)
	field("RCVPRT", o.ReceiverPartnerType)
	field("RCVPRN", o.ReceiverPartner)
	field("CREDAT", o.Created.Format("20060102"))
	field("CRETIM", o.Created.Format("150405"))
	b.WriteString("    </EDI_DC40>\n")
	fmt.Fprintf(&b, "    <!-- Data segments of %s go here, in the order of the IDoc type's definition (WE60), each with SEGMENT=\"1\":\n", root)
	b.WriteString("    <SEGMENT_NAME SEGMENT=\"1\">\n      <FIELD>value</FIELD>\n    </SEGMENT_NAME>\n    -->\n")
	fmt.Fprintf(&b, "  </IDOC>\n</%s>\n", root)
	return b.Bytes(), nil
}
