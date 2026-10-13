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
// {"status":200,"contentType":"application/json","body":"...","headers":{},"delayMs":0,
//  "when":[{"name":"...","pathContains":"...","header":{"X":"v"},"bodyContains":"...", answer fields}]}
// (`cpictl test stub set`): the first "when" entry whose conditions all hold answers, its answer
// fields overriding the rule's; else the rule's answer. Without one the stub answers 200 with {}.
// The recorded request names the answer in "matched" (entry name, when[<i>], default).
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

    def status = 200
    def contentType = "application/json"
    def answer = "{}"
    def extra = [:]
    def delay = 0
    def matched = ""
    def configured = rule ? lookup(rule) : null
    if (configured != null) {
        try {
            def r = new JsonSlurper().parseText(configured)
            def chosen = [:]
            matched = "default"
            if (r.when instanceof List) {
                for (int i = 0; i < r.when.size(); i++) {
                    def w = r.when[i]
                    if (w instanceof Map && matches(w, path, headers, body)) {
                        chosen = w
                        matched = (w.name ?: "when[" + i + "]").toString()
                        break
                    }
                }
            }
            status = (chosen.status ?: r.status ?: 200) as int
            contentType = (chosen.contentType ?: r.contentType ?: contentType).toString()
            def b = chosen.containsKey("body") ? chosen.body : r.body
            answer = b != null ? b.toString() : ""
            def h = chosen.headers instanceof Map ? chosen.headers : r.headers
            extra = h instanceof Map ? h : [:]
            delay = Math.min(Math.max(((chosen.delayMs != null ? chosen.delayMs : r.delayMs) ?: 0) as int, 0), 60000)
        } catch (Exception e) {
            status = 500
            contentType = "text/plain"
            answer = "cpictl stub: the Partner Directory value of rule " + rule + " is not valid JSON: " + e.getMessage()
        }
    }

    def request = [
        time         : java.time.Instant.now().toString(),
        rule         : rule,
        method       : method,
        path         : path,
        query        : query,
        headers      : recorded,
        body         : truncated ? body.take(MAX_BODY) : body,
        bodyTruncated: truncated,
        matched      : matched
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

    if (delay > 0) {
        Thread.sleep(delay as long)
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

// matches reports whether all conditions of a "when" entry hold: pathContains, header (name
// case-insensitive, value equal), bodyContains. An entry without conditions never matches.
def boolean matches(Map w, String path, Map headers, String body) {
    def any = false
    if (w.pathContains) {
        any = true
        if (!path.contains(w.pathContains.toString())) {
            return false
        }
    }
    if (w.header instanceof Map && !w.header.isEmpty()) {
        any = true
        for (e in w.header) {
            def name = e.key.toString()
            def actual = headers.find { k, v -> k.toString().equalsIgnoreCase(name) }?.value
            if (actual == null || actual.toString() != (e.value == null ? "" : e.value.toString())) {
                return false
            }
        }
    }
    if (w.bodyContains) {
        any = true
        if (!body.contains(w.bodyContains.toString())) {
            return false
        }
    }
    return any
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
