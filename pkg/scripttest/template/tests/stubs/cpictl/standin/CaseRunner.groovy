package cpictl.standin

import groovy.json.JsonOutput
import groovy.json.JsonSlurper

/**
 * Runs script cases on the stand-ins: cpictl writes the plan (one entry per
 * case: script file, function, message, service answers) and reads the
 * results. Started by the Gradle task cpictlScriptCases:
 *
 *   gradle -q cpictlScriptCases -PcpictlPlan=plan.json -PcpictlResults=results.json
 *
 * Each case runs with a fresh message and CaseContext; nothing is sent
 * anywhere.
 */
class CaseRunner {
    static void main(String[] args) {
        def plan = new JsonSlurper().parse(new File(args[0]))
        def results = plan.cases.collect { runCase(it) }
        new File(args[1]).text = JsonOutput.toJson([cases: results])
    }

    static Map runCase(Map c) {
        def out = [id: c.id]
        def ctx = new CaseContext(c.pd ?: [:], c.valueMappings ?: [:], c.secure ?: [:], c.keystore ?: [:])
        CaseContext.set(ctx)
        def started = System.nanoTime()
        try {
            def binding = new Binding(messageLogFactory: new Services.LogFactory())
            def script = new GroovyShell(CaseRunner.classLoader, binding).parse(new File(c.script))
            def msg = c.version == 2 ? new MessageV2() : new MessageV1()
            if (c.body != null) {
                msg.setBody(c.body)
            }
            (c.headers ?: [:]).each { k, v -> msg.setHeader(k, v) }
            (c.properties ?: [:]).each { k, v -> msg.setProperty(k, v) }
            def result
            try {
                result = c.hasArgs ? script.invokeMethod(c.function, (c.args ?: []) as Object[]) : script.invokeMethod(c.function, msg)
            } catch (MissingMethodException e) {
                if (e.method == c.function && e.type == script.getClass()) {
                    out.error = "the script has no function ${c.function} taking ${c.hasArgs ? (c.args?.size() ?: 0) + ' argument(s)' : 'the message'}".toString()
                    return out
                }
                throw e
            }
            if (c.hasArgs) {
                out.returns = plain(result)
                out.hasReturns = true
            } else if (result != null && !(result instanceof com.sap.gateway.ip.core.customdev.util.Message) && !(result instanceof com.sap.it.script.v2.api.Message)) {
                out.returns = plain(result)
                out.hasReturns = true
            }
            out.body = msg.getBody() == null ? null : msg.getBody(String)
            out.headers = msg.getHeaders().collectEntries { k, v -> [(k): v == null ? null : v.toString()] }
            out.properties = msg.getProperties().collectEntries { k, v -> [(k): v == null ? null : v.toString()] }
        } catch (Throwable t) {
            def cause = t instanceof org.codehaus.groovy.runtime.InvokerInvocationException && t.cause ? t.cause : t
            out.throws = [class: cause.getClass().name, message: cause.message ?: '']
            if (cause instanceof UnsupportedInStandInsException) {
                out.unsupported = true
            }
        } finally {
            out.durationMs = (System.nanoTime() - started).intdiv(1_000_000)
            out.log = [customHeaders: ctx.log.customHeaders ?: [:], properties: ctx.log.properties ?: [:]]
            CaseContext.clear()
        }
        return out
    }

    /** A JSON-able copy of a return value (maps, lists, scalars; other objects as text). */
    static Object plain(Object v) {
        switch (v) {
            case null: return null
            case Map: return v.collectEntries { k, x -> [(k.toString()): plain(x)] }
            case Collection: return v.collect { plain(it) }
            case Object[]: return v.collect { plain(it) }
            case Boolean: return v
            case BigDecimal: return v
            case Number: return v
            case CharSequence: return v.toString()
            default: return v.toString()
        }
    }
}
