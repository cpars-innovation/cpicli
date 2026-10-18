import com.sap.gateway.ip.core.customdev.util.Message
import groovy.json.JsonOutput
import groovy.json.JsonSlurper

// cpictl script test flow: the result of the script under test as JSON
// {"body": ..., "headers": {...}, "properties": {...}}: the headers and properties the case set
// or the script added or changed (a value that is no longer there is left out). What the runtime
// itself writes at every step is left out too.

def Message processData(Message message) {
    def base = new JsonSlurper().parseText(message.getProperty("CpictlBaseline")?.toString() ?: "{}")
    def str = { v -> v == null ? null : (v instanceof byte[] ? new String((byte[]) v, "UTF-8") : v.toString()) }
    def runtime = { String k ->
        k in ["scriptFile", "functionName", "sapFunctionName", "scriptFileType", "SAP_MessageProcessingLog", "SAP_MonitoringStateProperties"] ||
            k.startsWith("SAP_InspectModelStepId")
    }
    def changed = { Map now, Map before ->
        def out = [:]
        now.each { k, v ->
            if (runtime(k.toString())) return
            def s = str(v)
            if (!(before ?: [:]).containsKey(k) || before[k] != s) out[k] = s
        }
        out
    }
    def props = new LinkedHashMap(message.getProperties())
    props.remove("CpictlBaseline")
    def result = [
        body      : message.getBody(String),
        headers   : changed(message.getHeaders(), base.headers),
        properties: changed(props, base.properties),
    ]
    message.setBody(JsonOutput.toJson(result))
    message.setHeader("Content-Type", "application/json")
    return message
}
