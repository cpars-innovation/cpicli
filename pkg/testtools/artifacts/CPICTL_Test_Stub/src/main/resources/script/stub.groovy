import com.sap.gateway.ip.core.customdev.util.Message
import com.sap.it.api.ITApiFactory
import com.sap.it.api.pd.PartnerDirectoryService
import groovy.json.JsonOutput
import groovy.json.JsonSlurper

// cpictl receiver stub (CPICTL_Test_Stub), installed by `cpictl test tools install`.
//
// Every request to .../http/cpictl/stub/<rule>/<more> is recorded as the message processing log
// attachment cpictl-stub-request (JSON: time, rule, method, path, query, headers, body) with the
// custom header properties CpictlStubRule and CpictlStubPath, so test runs find it
// (cpictl: ops.StubCalls). The answer is the response configured for the rule in the Partner
// Directory: PID CPICTL_Stub, string parameter ID = rule, value
// {"status":200,"contentType":"application/json","body":"...","headers":{}}
// (`cpictl test stub set`). Without one the stub answers 200 with {}.
//
// Script API version 1 (com.sap.gateway.ip.core.customdev.util.Message): the Partner Directory
// API (ITApiFactory, PartnerDirectoryService) is documented for it.

def Message processData(Message message) {
    final int MAX_BODY = 65536
    // header names never recorded (the same rule as cpictl's masking)
    final SENSITIVE = ~/(?i)authorization|cookie|password|passwd|secret|token|credential|apikey|api-key/
    def headers = message.getHeaders()
    def method = (headers.get("CamelHttpMethod") ?: "POST").toString()
    def path = stubPath(headers)
    def rule = path.split("/")[0]
    def query = (headers.get("CamelHttpQuery") ?: "").toString()

    def recorded = [:]
    headers.each { k, v ->
        def name = k.toString()
        if (v == null || name.startsWith("Camel") || name.startsWith("SAP_") || name ==~ /(?i)sapauthenticatedusername/ || SENSITIVE.matcher(name).find()) {
            return
        }
        recorded[name] = v.toString().take(1024)
    }
    def body = message.getBody(String) ?: ""
    def truncated = body.length() > MAX_BODY
    def request = [
        time         : java.time.Instant.now().toString(),
        rule         : rule,
        method       : method,
        path         : path,
        query        : query,
        headers      : recorded,
        body         : truncated ? body.take(MAX_BODY) : body,
        bodyTruncated: truncated
    ]
    def log = messageLogFactory.getMessageLog(message)
    if (log != null) {
        log.addAttachmentAsString("cpictl-stub-request", JsonOutput.toJson(request), "application/json")
        if (rule) {
            log.addCustomHeaderProperty("CpictlStubRule", rule)
        }
        if (path) {
            log.addCustomHeaderProperty("CpictlStubPath", path)
        }
    }

    def status = 200
    def contentType = "application/json"
    def answer = "{}"
    def extra = [:]
    def configured = rule ? lookup(rule) : null
    if (configured != null) {
        try {
            def r = new JsonSlurper().parseText(configured)
            status = (r.status ?: 200) as int
            contentType = (r.contentType ?: contentType).toString()
            answer = r.body != null ? r.body.toString() : ""
            extra = r.headers instanceof Map ? r.headers : [:]
        } catch (Exception e) {
            status = 500
            contentType = "text/plain"
            answer = "cpictl stub: the Partner Directory value of rule " + rule + " is not valid JSON: " + e.getMessage()
        }
    }
    message.setBody(answer)
    extra.each { k, v -> message.setHeader(k.toString(), v == null ? "" : v.toString()) }
    message.setHeader("Content-Type", contentType)
    message.setHeader("CamelHttpResponseCode", status)
    return message
}

// stubPath is the request path after /cpictl/stub/ (without the query).
def String stubPath(Map headers) {
    final String MARKER = "/cpictl/stub/"
    for (name in ["CamelHttpUri", "CamelHttpUrl", "CamelHttpPath"]) {
        def v = headers.get(name)?.toString()
        if (!v) {
            continue
        }
        v = v.split("\\?")[0]
        def i = v.indexOf(MARKER)
        if (i >= 0) {
            return v.substring(i + MARKER.length())
        }
    }
    // CamelHttpPath may hold only the part below the sender's address
    def rest = headers.get("CamelHttpPath")?.toString() ?: ""
    return rest.split("\\?")[0].replaceAll("^/+", "")
}

// lookup returns the configured response of a rule, null without one.
def String lookup(String rule) {
    final String PID = "CPICTL_Stub"
    try {
        def service = ITApiFactory.getService(PartnerDirectoryService.class, null)
        def value = service?.getParameter(rule, PID, String.class)
        return value == null ? null : value.toString()
    } catch (Exception e) {
        return null // no PID CPICTL_Stub or no parameter: the default answer
    }
}
