import com.sap.gateway.ip.core.customdev.util.Message
import groovy.json.JsonSlurper

// cpictl XSLT test flow (CPICTL_Test_XSLT), installed by `cpictl test tools install`.
//
// `cpictl test xslt --online` uploads the stylesheet under test as the Partner Directory binary
// parameter CPICTL_Test/<id> and sends the case's input to /cpictl/xslt with the headers
// CpictlXsltId = <id> and CpictlXsltParams = {"headers":{...},"properties":{...}}. This script
// points the XSLT mapping step at that parameter (header CpictlXslt = pd:CPICTL_Test:<id>:Binary;
// only PID CPICTL_Test can be used) and sets the case's headers and properties with their exact
// names (HTTP header names lose their case on the way), so that the stylesheet receives them as
// xsl:param like in a real flow. The response is the output.
//
// Script API version 1 (com.sap.gateway.ip.core.customdev.util.Message).

def Message processData(Message message) {
    def headers = message.getHeaders()
    def id = (headers.get("CpictlXsltId") ?: "").toString()
    if (!(id ==~ /[A-Za-z0-9_.\-]{1,60}/)) {
        throw new IllegalArgumentException("cpictl XSLT test: header CpictlXsltId is missing or invalid (letters, digits, '_', '.', '-'; at most 60)")
    }
    message.setHeader("CpictlXslt", "pd:CPICTL_Test:" + id + ":Binary")
    def params = headers.get("CpictlXsltParams")?.toString()
    if (params) {
        def p = new JsonSlurper().parseText(params)
        (p.headers ?: [:]).each { k, v -> message.setHeader(k.toString(), v == null ? "" : v.toString()) }
        (p.properties ?: [:]).each { k, v -> message.setProperty(k.toString(), v == null ? "" : v.toString()) }
    }
    return message
}
