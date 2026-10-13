package idoc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orders = `<?xml version="1.0" encoding="UTF-8"?>
<ORDERS05>
  <IDOC BEGIN="1">
    <EDI_DC40 SEGMENT="1">
      <TABNAM>EDI_DC40</TABNAM>
      <DOCNUM>0000000000004711</DOCNUM>
      <IDOCTYP>ORDERS05</IDOCTYP>
      <MESTYP>ORDERS</MESTYP>
      <SNDPRN>S4HCLNT100</SNDPRN>
      <RCVPRN>CPI</RCVPRN>
    </EDI_DC40>
    <E1EDK01 SEGMENT="1"><BELNR>4500017001</BELNR></E1EDK01>
  </IDOC>
  <IDOC BEGIN="1">
    <EDI_DC40 SEGMENT="1"><DOCNUM>0000000000004712</DOCNUM><IDOCTYP>ORDERS05</IDOCTYP></EDI_DC40>
  </IDOC>
</ORDERS05>`

func TestParse(t *testing.T) {
	doc, err := Parse([]byte(orders))
	require.NoError(t, err)
	assert.False(t, doc.SOAP)
	assert.Equal(t, "ORDERS05", doc.Root)
	require.Len(t, doc.IDocs, 2)
	assert.Equal(t, Control{DOCNUM: "0000000000004711", IDOCTYP: "ORDERS05", MESTYP: "ORDERS", SNDPRN: "S4HCLNT100", RCVPRN: "CPI"}, doc.First())
	assert.Equal(t, "0000000000004712", doc.IDocs[1].DOCNUM)
	assert.Equal(t, orders, string(doc.XML))
}

// The IDoc SOAP form (as in SAP's IDoc Splitter flow): the IDoc is the
// first element of the SOAP Body.
func TestParseSOAP(t *testing.T) {
	soap := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body>
<ORDERS05 xmlns="urn:sap-com:document:sap:idoc:soap:messages"><IDOC BEGIN="1"><EDI_DC40 SEGMENT="1"><DOCNUM>42</DOCNUM><IDOCTYP>ORDERS05</IDOCTYP></EDI_DC40></IDOC></ORDERS05>
</soapenv:Body></soapenv:Envelope>`
	doc, err := Parse([]byte(soap))
	require.NoError(t, err)
	assert.True(t, doc.SOAP)
	assert.Equal(t, "ORDERS05", doc.Root)
	assert.Equal(t, "42", doc.First().DOCNUM)
	assert.Equal(t, `<ORDERS05 xmlns="urn:sap-com:document:sap:idoc:soap:messages"><IDOC BEGIN="1"><EDI_DC40 SEGMENT="1"><DOCNUM>42</DOCNUM><IDOCTYP>ORDERS05</IDOCTYP></EDI_DC40></IDOC></ORDERS05>`, string(doc.XML))
}

func TestParseErrors(t *testing.T) {
	_, err := Parse([]byte(`<Order><No>1</No></Order>`))
	assert.ErrorIs(t, err, ErrNoControlRecord)
	_, err = Parse([]byte(`<ORDERS05><IDOC>`))
	assert.ErrorContains(t, err, "invalid IDoc XML")
	_, err = Parse([]byte(`<e:Envelope xmlns:e="x"><e:Body></e:Body></e:Envelope>`))
	assert.ErrorContains(t, err, "Body is empty")
	_, err = Parse([]byte(""))
	assert.Error(t, err)
}

func TestTemplate(t *testing.T) {
	created := time.Date(2026, 10, 13, 9, 30, 5, 0, time.UTC)
	data, err := Template(TemplateOptions{IDocType: "ORDERS05", MessageType: "ORDERS", SenderPartner: "S4H&CO", ReceiverPartner: "CPI", Created: created})
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "<ORDERS05>\n  <IDOC BEGIN=\"1\">\n    <EDI_DC40 SEGMENT=\"1\">\n      <TABNAM>EDI_DC40</TABNAM>")
	assert.Contains(t, text, "<SNDPRN>S4H&amp;CO</SNDPRN>")
	assert.Contains(t, text, "<SNDPOR/>")
	assert.Contains(t, text, "<CREDAT>20261013</CREDAT>")
	assert.Contains(t, text, "<CRETIM>093005</CRETIM>")
	assert.Contains(t, text, "<!-- Data segments of ORDERS05 go here")
	doc, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, Control{DOCNUM: "0000000000000001", DIRECT: "1", IDOCTYP: "ORDERS05", MESTYP: "ORDERS", SNDPRT: "LS", SNDPRN: "S4H&CO", RCVPRT: "LS", RCVPRN: "CPI"}, doc.First())

	data, err = Template(TemplateOptions{IDocType: "ORDERS05", Extension: "ZORDERS05", MessageType: "ORDERS", Direct: "2", DocNum: "7", Created: created})
	require.NoError(t, err)
	doc, err = Parse(data)
	require.NoError(t, err)
	assert.Equal(t, "ZORDERS05", doc.Root)
	assert.Equal(t, "ZORDERS05", doc.First().CIMTYP)
	assert.Equal(t, "2", doc.First().DIRECT)

	_, err = Template(TemplateOptions{IDocType: "ORDERS05"})
	assert.Error(t, err)
	_, err = Template(TemplateOptions{IDocType: "<x>", MessageType: "ORDERS"})
	assert.Error(t, err)
	_, err = Template(TemplateOptions{IDocType: "ORDERS05", MessageType: "ORDERS", Direct: "3"})
	assert.Error(t, err)
}
